package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

var version = "0.1.0"

const usage = `shepherd — one coordinator, a worktree and agent per bead, beads as the only record.

Usage:
  shepherd coordinator [--agent claude|codex]  open (or focus) the coordinator agent
  shepherd dispatch <bead> [--agent K] [--focus]  start a worker on a bead in its own worktree
  shepherd focus [<bead>] [--agent K]           focus the bead's worker, else dispatch it
                                                (no bead: read it from the clipboard)
  shepherd context                              the coordinator's per-turn digest
  shepherd report [--since 24h|7d|YYYY-MM-DD]   Markdown of what shipped, merged, is in flight, needs you
  shepherd inbox done [<bead>...]               mark inbox items handled (all if none given)
  shepherd resolve <bead> [--force]             remove a finished bead's worktree and merged branch
  shepherd sweep [--yes]                        list finished linked worktrees; --yes removes the safe ones
  shepherd resume [<bead>...] [--agent K]       restart exited agents in their worktrees, continuing
                                                their last conversation (all resumable if none given)
  shepherd stale [--days N] [--release]         claims with no agent or worktree untouched N days
                                                (default 7); --release reopens them
  shepherd board                                the board (runs as the plugin popup)
  shepherd ticker run|start|stop|status         the background loop: sidebar, PR follow-up, nudges
  shepherd tick                                 one ticker pass in the foreground
  shepherd configure                            install shepherd's agent view (sort by thread state)
  shepherd unconfigure                          stop the ticker, remove the view and sidebar tokens
  shepherd version
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "shepherd:", err)
		os.Exit(1)
	}
}

// interspersed lets flags follow positional arguments (dispatch <bead> --agent codex).
func interspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	return pos, nil
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	h := newHerdr()
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	kind := fs.String("agent", "", "agent kind: claude or codex")
	focus := fs.Bool("focus", false, "focus the new workspace")
	force := fs.Bool("force", false, "resolve even if the bead is open or its agent is working")
	sinceFlag := fs.String("since", "24h", "report window: Nh, Nd or YYYY-MM-DD")
	yes := fs.Bool("yes", false, "sweep: remove the clean candidates instead of listing them")
	days := fs.Int("days", 7, "stale: untouched for at least this many days")
	release := fs.Bool("release", false, "stale: set each stale claim back to open")
	pos, err := interspersed(fs, rest)
	if err != nil {
		return err
	}
	switch cmd {
	case "version", "--version", "-V":
		fmt.Println("shepherd", version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	case "coordinator":
		msg, err := openCoordinator(cfg, h, *kind)
		if err != nil {
			h.Notify("shepherd: coordinator failed", err.Error())
			return err
		}
		fmt.Println(msg)
	case "dispatch":
		if len(pos) != 1 {
			return fmt.Errorf("usage: shepherd dispatch <bead> [--agent claude|codex] [--focus]")
		}
		msg, err := dispatch(cfg, h, pos[0], DispatchOpts{Kind: *kind, Focus: *focus})
		if err != nil {
			return err
		}
		fmt.Println(msg)
	case "focus":
		id := ""
		if len(pos) > 0 {
			id = pos[0]
		} else if id, err = clipboardBead(); err != nil {
			h.Notify("shepherd: no bead", err.Error())
			return err
		}
		msg, err := dispatch(cfg, h, id, DispatchOpts{Kind: *kind, Focus: true})
		if err != nil {
			h.Notify("shepherd: "+id, err.Error())
			return err
		}
		h.Notify("shepherd: "+id, msg)
		fmt.Println(msg)
	case "context":
		st := loadState()
		t := newTicker(cfg, log.New(os.Stderr, "", 0))
		// The ticker refreshes PRs; context reads its last pass unless it is down.
		if runningPID() != 0 {
			st.LastGH = time.Now()
		}
		threads, _, err := t.gather(&st, time.Now())
		if err != nil {
			return err
		}
		next, _ := Beads{}.Next()
		ready, _ := Beads{}.Ready()
		inbox, _ := readInbox()
		fmt.Print(renderContext(cfg, threads, next, ready, inbox, st, worktreed(cfg, h, threads, st.PRs), time.Now()))
	case "report":
		now := time.Now()
		since, err := parseSince(*sinceFlag, now)
		if err != nil {
			return err
		}
		r, err := gatherReport(cfg, since)
		if err != nil {
			return err
		}
		fmt.Print(renderReport(r, now))
	case "inbox":
		if len(pos) == 0 || pos[0] != "done" {
			return fmt.Errorf("usage: shepherd inbox done [<bead>...]")
		}
		n, err := inboxDone(pos[1:])
		if err != nil {
			return err
		}
		fmt.Printf("%d item(s) marked done\n", n)
	case "resolve":
		if len(pos) != 1 {
			return fmt.Errorf("usage: shepherd resolve <bead> [--force]")
		}
		msg, err := resolve(cfg, h, pos[0], *force)
		if err != nil {
			return err
		}
		fmt.Println(msg)
	case "sweep":
		return sweep(cfg, liveSweepEnv(h), *yes, os.Stdout)
	case "resume":
		lines, err := resume(cfg, h, pos, *kind)
		fmt.Print(strings.Join(append(lines, ""), "\n"))
		return err
	case "stale":
		lines, err := stale(cfg, h, *days, *release)
		fmt.Print(strings.Join(append(lines, ""), "\n"))
		return err
	case "board":
		return runBoard(cfg)
	case "action":
		// Plugin actions: open one of this plugin's panes.
		if len(pos) != 1 {
			return fmt.Errorf("usage: shepherd action board")
		}
		return h.OpenPane(pos[0])
	case "ticker":
		if len(pos) != 1 {
			return fmt.Errorf("usage: shepherd ticker run|start|stop|status")
		}
		switch pos[0] {
		case "run":
			return tickerRun(cfg)
		case "start":
			return tickerStart()
		case "stop":
			return tickerStop()
		case "status":
			tickerStatus()
		default:
			return fmt.Errorf("unknown ticker command %q", pos[0])
		}
	case "tick":
		st := loadState()
		t := newTicker(cfg, log.New(os.Stderr, "", log.LstdFlags))
		if err := t.once(&st); err != nil {
			return err
		}
		return saveState(st)
	case "startup":
		// Herdr runs this when the plugin loads. Views don't survive a server
		// restart, so the view is set again here.
		if err := setView(socketRPC{herdrSocket()}); err != nil {
			fmt.Fprintln(os.Stderr, "shepherd: agent view:", err)
		}
		return tickerStart()
	case "configure":
		if err := setView(socketRPC{herdrSocket()}); err != nil {
			return err
		}
		fmt.Println("agent view set: label \"shepherd\", sorted by thread state")
	case "unconfigure":
		if err := tickerStop(); err != nil {
			return err
		}
		if err := unconfigure(socketRPC{herdrSocket()}, h.Agents, h.ClearState); err != nil {
			return err
		}
		fmt.Println("agent view and sidebar tokens removed")
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, strings.TrimSpace(usage))
	}
	return nil
}
