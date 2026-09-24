package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type TickerState struct {
	Threads      map[string]Snapshot `json:"threads"`
	LastGH       time.Time           `json:"last_gh"`
	PRs          map[string]PR       `json:"prs"` // by bead id, from the last gh pass
	LastNudge    time.Time           `json:"last_nudge"`
	Login        string              `json:"login"`
	CoordReady   time.Time           `json:"coord_ready"`
	CoordSeq     int64               `json:"coord_seq"`
	GHFailingFor time.Time           `json:"gh_failing_since"`
}

func statePath() string { return filepath.Join(stateDir(), "state.json") }

func loadState() TickerState {
	s := TickerState{Threads: map[string]Snapshot{}, PRs: map[string]PR{}}
	b, err := os.ReadFile(statePath())
	if err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.Threads == nil {
		s.Threads = map[string]Snapshot{}
	}
	if s.PRs == nil {
		s.PRs = map[string]PR{}
	}
	return s
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func saveState(s TickerState) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(statePath(), b)
}

type Ticker struct {
	cfg   Config
	herdr Herdr
	beads Beads
	gh    GH
	log   *log.Logger
}

func newTicker(cfg Config, logger *log.Logger) Ticker {
	return Ticker{cfg: cfg, herdr: newHerdr(), beads: Beads{}, gh: GH{repo: cfg.Repo}, log: logger}
}

// gather joins beads, agents and PRs into threads. Only beads a worker could
// be on (in_progress, needs_me, blocked) are threads.
func (t Ticker) gather(st *TickerState, now time.Time) ([]Thread, map[string]Agent, error) {
	active, err := t.beads.Active()
	if err != nil {
		return nil, nil, err
	}
	agents, err := t.herdr.Agents()
	if err != nil {
		return nil, nil, err
	}
	byName := map[string]Agent{}
	for _, a := range agents {
		if a.Name != "" {
			byName[a.Name] = a
		}
	}
	if now.Sub(st.LastGH) >= t.cfg.ghEvery() {
		t.refreshPRs(st, active)
		st.LastGH = now
	}
	threads := make([]Thread, 0, len(active))
	for _, b := range active {
		th := Thread{Bead: b}
		if a, ok := byName[agentName(b.ID)]; ok {
			th.Agent = &a
		}
		if pr, ok := st.PRs[b.ID]; ok {
			th.PR = &pr
			th.Checks = summarizeChecks(pr.Checks)
			th.Reviews = reviewsNotBy(pr.Reviews, st.Login)
		}
		threads = append(threads, th)
	}
	return threads, byName, nil
}

func (t Ticker) refreshPRs(st *TickerState, active []Bead) {
	if st.Login == "" {
		st.Login = t.gh.Login()
	}
	mine, err := t.gh.Mine()
	if err != nil {
		t.log.Printf("gh: %v", err)
		if st.GHFailingFor.IsZero() {
			st.GHFailingFor = time.Now()
		}
		return
	}
	st.GHFailingFor = time.Time{}
	inList := map[string]PR{}
	for _, pr := range mine {
		inList[pr.URL] = pr
	}
	prs := map[string]PR{}
	for _, b := range active {
		byURL := map[string]PR{}
		for _, u := range NotedPRs(b.Notes) {
			if pr, ok := inList[u]; ok {
				byURL[u] = pr
			} else if pr, err := t.gh.View(u, false); err == nil {
				byURL[u] = pr
			}
		}
		pr, ok := prForBead(b, t.cfg.BranchPrefix, mine, byURL)
		if !ok {
			continue
		}
		if pr.State == "OPEN" {
			if full, err := t.gh.View(pr.URL, true); err == nil {
				pr = full
			} else {
				t.log.Printf("gh pr view %d: %v", pr.Number, err)
				if old, ok := st.PRs[b.ID]; ok && old.Number == pr.Number {
					pr = old
				}
			}
		}
		prs[b.ID] = pr
	}
	st.PRs = prs
}

func rank(g Group, id string) string { return fmt.Sprintf("%d-%s", int(g), id) }

// once runs one pass: sidebar rows, events, prompts to threads, a nudge to the
// coordinator, and closing up beads that left the active set.
func (t Ticker) once(st *TickerState) error {
	now := time.Now()
	threads, agents, err := t.gather(st, now)
	if err != nil {
		return err
	}
	for _, name := range deliverOutbox(t.herdr, agents) {
		t.log.Printf("delivered queued brief to %s", name)
	}
	ttl := 4 * t.cfg.tick()
	current := map[string]bool{}
	for _, th := range threads {
		id := th.Bead.ID
		current[id] = true
		prev, seen := st.Threads[id]
		out := transition(th, prev, !seen, now)
		snap := snapshot(th, prev, now)
		snap.Pending = append(snap.Pending, out.Prompts...)
		for _, e := range out.Events {
			if err := writeEvent(e); err != nil {
				t.log.Printf("inbox: %v", err)
			}
		}
		if out.Notify {
			t.herdr.Notify(fmt.Sprintf("%s: %s", id, classify(th)), shortTitle(th.Bead.Title, 60))
		}
		if th.Agent != nil {
			if len(snap.Pending) > 0 && deliverable(snap, now, t.cfg.idle()) {
				text := strings.Join(snap.Pending, "\n\n")
				if err := t.herdr.Prompt(th.Agent.PaneID, text); err != nil {
					t.log.Printf("prompt %s: %v", id, err)
				} else {
					t.log.Printf("prompted %s (%d item(s))", id, len(snap.Pending))
					snap.Pending = nil
					snap.ReadySince = time.Time{}
				}
			}
			tokens := map[string]string{"sh_state": stateLine(th), "sh_rank": rank(classify(th), id)}
			if err := t.herdr.ReportState(th.Agent.PaneID, tokens, ttl); err != nil {
				t.log.Printf("sidebar %s: %v", id, err)
			}
		}
		st.Threads[id] = snap
	}
	for id, prev := range st.Threads {
		if current[id] {
			continue
		}
		t.leftActive(st, id, prev, agents, now)
	}
	t.nudgeCoordinator(st, agents, now)
	return nil
}

// leftActive handles a bead that is no longer in_progress/needs_me/blocked:
// usually closed by its thread after the PR merged.
func (t Ticker) leftActive(st *TickerState, id string, prev Snapshot, agents map[string]Agent, now time.Time) {
	b, err := t.beads.Show(id)
	if err != nil {
		t.log.Printf("show %s: %v", id, err)
		delete(st.Threads, id)
		return
	}
	a, hasAgent := agents[agentName(id)]
	if hasAgent {
		t.herdr.ClearState(a.PaneID)
	}
	if b.Status == StatusClosed {
		summary := fmt.Sprintf("%s closed", id)
		if prev.PRState == "MERGED" {
			summary += fmt.Sprintf(" after PR #%d merged", prev.PRNumber)
		}
		if t.cfg.AutoResolve && prev.PRState == "MERGED" && (!hasAgent || agentReady(&a)) {
			if res, err := resolve(t.cfg, t.herdr, id, false); err != nil {
				summary += "; auto-resolve failed: " + err.Error()
			} else {
				summary += "; " + res
			}
		} else {
			summary += fmt.Sprintf("; run `shepherd resolve %s` to remove its worktree", id)
		}
		_ = writeEvent(Event{Bead: id, Kind: "closed", Summary: summary, At: now})
	}
	delete(st.Threads, id)
}

func (t Ticker) nudgeCoordinator(st *TickerState, agents map[string]Agent, now time.Time) {
	coord, ok := agents[t.cfg.CoordinatorName]
	if !ok {
		st.CoordReady = time.Time{}
		return
	}
	if !agentReady(&coord) {
		st.CoordReady = time.Time{}
	} else if st.CoordReady.IsZero() || coord.Seq != st.CoordSeq {
		st.CoordReady = now
	}
	st.CoordSeq = coord.Seq
	if !t.cfg.Nudge || st.CoordReady.IsZero() || now.Sub(st.CoordReady) < t.cfg.idle() {
		return
	}
	events, _ := readInbox()
	fresh := 0
	for _, e := range events {
		if e.At.After(st.LastNudge) {
			fresh++
		}
	}
	if fresh == 0 {
		return
	}
	msg := fmt.Sprintf("[shepherd ticker: automated, not the user, approves nothing] %d new inbox item(s). Run `shepherd context`.", fresh)
	if err := t.herdr.Prompt(coord.PaneID, msg); err != nil {
		t.log.Printf("nudge: %v", err)
		return
	}
	st.LastNudge = now
	st.CoordReady = time.Time{}
}

func pidPath() string { return filepath.Join(stateDir(), "ticker.pid") }
func logPath() string { return filepath.Join(stateDir(), "ticker.log") }

func runningPID() int {
	b, err := os.ReadFile(pidPath())
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return 0
	}
	return pid
}

func tickerRun(cfg Config) error {
	if pid := runningPID(); pid != 0 && pid != os.Getpid() {
		return fmt.Errorf("ticker already running (pid %d)", pid)
	}
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(pidPath(), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return err
	}
	defer os.Remove(pidPath())
	f, err := os.OpenFile(logPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	logger := log.New(f, "", log.LstdFlags)
	logger.Printf("ticker %s started, every %s", version, cfg.tick())
	t := newTicker(cfg, logger)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	timer := time.NewTicker(cfg.tick())
	defer timer.Stop()
	for {
		st := loadState()
		if err := t.once(&st); err != nil {
			logger.Printf("tick: %v", err)
		}
		if err := saveState(st); err != nil {
			logger.Printf("state: %v", err)
		}
		select {
		case <-sig:
			logger.Printf("ticker stopping")
			return nil
		case <-timer.C:
		}
	}
}

func tickerStart() error {
	if pid := runningPID(); pid != 0 {
		fmt.Printf("ticker already running (pid %d)\n", pid)
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "ticker", "run")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Printf("ticker started (pid %d), log %s\n", cmd.Process.Pid, logPath())
	return cmd.Process.Release()
}

func tickerStop() error {
	pid := runningPID()
	if pid == 0 {
		fmt.Println("ticker not running")
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	for i := 0; i < 50 && runningPID() != 0; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Printf("ticker stopped (pid %d)\n", pid)
	return nil
}

func tickerStatus() {
	if pid := runningPID(); pid != 0 {
		fmt.Printf("ticker running (pid %d), log %s\n", pid, logPath())
	} else {
		fmt.Println("ticker not running")
	}
}

// Inbox: one JSON file per event; handled ones move to inbox/done.
func inboxDir() string { return filepath.Join(stateDir(), "inbox") }

func writeEvent(e Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%d-%s-%s.json", e.At.UnixNano(), agentName(e.Bead), e.Kind)
	return writeAtomic(filepath.Join(inboxDir(), name), b)
}

type InboxItem struct {
	Event
	File string `json:"-"`
}

func readInbox() ([]InboxItem, error) {
	entries, err := os.ReadDir(inboxDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []InboxItem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(inboxDir(), e.Name()))
		if err != nil {
			continue
		}
		var it InboxItem
		if json.Unmarshal(b, &it.Event) == nil {
			it.File = e.Name()
			items = append(items, it)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].File < items[j].File })
	return items, nil
}

// inboxDone marks items handled: all of them, or those for the given beads.
func inboxDone(beads []string) (int, error) {
	items, err := readInbox()
	if err != nil {
		return 0, err
	}
	want := map[string]bool{}
	for _, b := range beads {
		want[b] = true
	}
	done := filepath.Join(inboxDir(), "done")
	if err := os.MkdirAll(done, 0o755); err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		if len(want) > 0 && !want[it.Bead] {
			continue
		}
		if err := os.Rename(filepath.Join(inboxDir(), it.File), filepath.Join(done, it.File)); err == nil {
			n++
		}
	}
	return n, nil
}
