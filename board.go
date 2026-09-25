package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"
)

type boardRow struct {
	header string
	bead   Bead
	agent  *Agent
	line   string
	color  string
	run    string
}

const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiBold   = "\x1b[1m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiCyan   = "\x1b[36m"
	ansiInvert = "\x1b[7m"
)

func groupColor(g Group) string {
	switch g {
	case GroupNeedsYou, GroupFailing:
		return ansiRed
	case GroupReview, GroupMerged:
		return ansiYellow
	case GroupWorking:
		return ansiGreen
	default:
		return ansiDim
	}
}

// loadBoard builds the rows from live bd and herdr state plus the ticker's
// last PR pass, so opening the board never waits on GitHub.
func loadBoard(cfg Config) ([]boardRow, error) {
	st := loadState()
	t := newTicker(cfg, nil)
	st.LastGH = time.Now() // never call gh from the board
	threads, _, err := t.gather(&st, time.Now())
	if err != nil {
		return nil, err
	}
	sort.SliceStable(threads, func(i, j int) bool {
		gi, gj := classify(threads[i]), classify(threads[j])
		if gi != gj {
			return gi < gj
		}
		return threads[i].Bead.ID < threads[j].Bead.ID
	})
	var rows []boardRow
	rows = append(rows, boardRow{header: "threads"})
	for _, th := range threads {
		rows = append(rows, boardRow{bead: th.Bead, agent: th.Agent, line: stateLine(th), color: groupColor(classify(th)), run: runCommand(th)})
	}
	next, _ := Beads{}.Next()
	rows = append(rows, boardRow{header: "next"})
	seen := map[string]bool{}
	for _, b := range next {
		seen[b.ID] = true
		rows = append(rows, boardRow{bead: b, line: fmt.Sprintf("P%d %s", b.Priority, b.Type), color: ansiCyan})
	}
	ready, _ := Beads{}.Ready()
	rows = append(rows, boardRow{header: "ready"})
	n := 0
	for _, b := range ready {
		if seen[b.ID] || b.Status != StatusOpen || n >= 25 {
			continue
		}
		n++
		rows = append(rows, boardRow{bead: b, line: fmt.Sprintf("P%d %s", b.Priority, b.Type), color: ansiDim})
	}
	return rows, nil
}

func nextSelectable(rows []boardRow, from, dir int) int {
	for i := from; i >= 0 && i < len(rows); i += dir {
		if rows[i].header == "" {
			return i
		}
	}
	return -1
}

func runBoard(cfg Config) error {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer term.Restore(fd, old)
	fmt.Print("\x1b[?1049h\x1b[?25l")
	defer fmt.Print("\x1b[?25h\x1b[?1049l")

	h := newHerdr()
	rows, err := loadBoard(cfg)
	status := ""
	if err != nil {
		status = err.Error()
	}
	cur := nextSelectable(rows, 0, 1)
	buf := make([]byte, 8)
	for {
		drawBoard(rows, cur, status)
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return err
		}
		key := string(buf[:n])
		status = ""
		sel := func() *boardRow {
			if cur < 0 || cur >= len(rows) {
				return nil
			}
			return &rows[cur]
		}
		reload := func() {
			if r, err := loadBoard(cfg); err == nil {
				rows = r
				if cur >= len(rows) || cur < 0 || rows[cur].header != "" {
					cur = nextSelectable(rows, 0, 1)
				}
			} else {
				status = err.Error()
			}
		}
		switch key {
		case "q", "\x1b", "\x03":
			return nil
		case "j", "\x1b[B":
			if i := nextSelectable(rows, cur+1, 1); i >= 0 {
				cur = i
			}
		case "k", "\x1b[A":
			if i := nextSelectable(rows, cur-1, -1); i >= 0 {
				cur = i
			}
		case "r":
			reload()
		case "\r", "c", "x":
			r := sel()
			if r == nil {
				continue
			}
			if key == "\r" && r.agent != nil {
				_ = h.Focus(r.agent.Name)
				return nil
			}
			kind := ""
			switch key {
			case "c":
				kind = "claude"
			case "x":
				kind = "codex"
			}
			status = "starting " + r.bead.ID + "…"
			drawBoard(rows, cur, status)
			msg, err := dispatch(cfg, h, r.bead.ID, DispatchOpts{Kind: kind, Focus: true})
			if err != nil {
				status = err.Error()
				continue
			}
			_ = msg
			return nil
		case "n":
			if r := sel(); r != nil {
				on := !r.bead.HasLabel(LabelNext)
				if err := (Beads{}).SetLabel(r.bead.ID, LabelNext, on); err != nil {
					status = err.Error()
				}
				reload()
			}
		case "y":
			if r := sel(); r != nil {
				status = copyText(r.bead.ID, "copied "+r.bead.ID)
			}
		case "p":
			if r := sel(); r != nil && r.run != "" {
				status = copyText(r.run, "copied the command")
			}
		}
	}
}

func copyText(text, done string) string {
	if err := exec.Command("wl-copy", text).Run(); err != nil {
		return "wl-copy: " + err.Error()
	}
	return done
}

// statusFor is the status line: the last action's message, else the command
// the selected thread is waiting on the user to run.
func statusFor(rows []boardRow, cur int, status string) string {
	if status != "" || cur < 0 || cur >= len(rows) || rows[cur].run == "" {
		return status
	}
	return "run: " + rows[cur].run + "  (p copies)"
}

func drawBoard(rows []boardRow, cur int, status string) {
	w, hgt, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, hgt = 100, 30
	}
	var s strings.Builder
	s.WriteString("\x1b[H\x1b[2J")
	body := hgt - 3
	start := 0
	if cur >= body {
		start = cur - body + 1
	}
	stateW := min(34, max(12, w/3))
	titleW := w - 2 - 17 - stateW - 1
	for i := start; i < len(rows) && i-start < body; i++ {
		r := rows[i]
		if r.header != "" {
			fmt.Fprintf(&s, "%s%s%s\r\n", ansiBold, fit(r.header, w), ansiReset)
			continue
		}
		id := fit(r.bead.ID, 16) + " "
		state := fit(r.line, stateW)
		title := ""
		if titleW >= 8 {
			title = " " + fit(r.bead.Title, titleW)
		}
		if i == cur {
			fmt.Fprintf(&s, "%s  %s%s%s%s\r\n", ansiInvert, id, state, title, ansiReset)
		} else {
			fmt.Fprintf(&s, "  %s%s%s%s%s\r\n", id, r.color, state, ansiReset, title)
		}
	}
	fmt.Fprintf(&s, "\x1b[%d;1H%s%s%s", hgt-1, ansiRed, fit(statusFor(rows, cur, status), w-1), ansiReset)
	fmt.Fprintf(&s, "\x1b[%d;1H%s%s%s", hgt, ansiDim, fit("↵ focus/start  c claude  x codex  n next  y copy  p copy command  r refresh  q quit", w-1), ansiReset)
	fmt.Print(s.String())
}

// fit pads or cuts s to exactly n characters (runes, not bytes: the state
// lines carry "·" and "…").
func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) > n {
		if n == 1 {
			return "…"
		}
		return string(r[:n-1]) + "…"
	}
	return s + strings.Repeat(" ", n-len(r))
}
