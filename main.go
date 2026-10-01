package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// version is set from the release tag at build time.
var version = "dev"

const usage = `kelpie — one coordinator, a worktree and agent per bead, beads as the only record.

Usage:
  kelpie coordinator [--agent claude|codex]  open (or focus) the coordinator agent
  kelpie dispatch <bead> [--agent K] [--focus]  start a worker on a bead in its own worktree
  kelpie focus [<bead>] [--agent K]           focus the bead's worker, else dispatch it
                                            (no bead: read it from the clipboard)
  kelpie context                              the coordinator's per-turn digest
  kelpie report [--since 24h|7d|YYYY-MM-DD]   Markdown of what shipped, merged, is in flight, needs you
  kelpie inbox done [<bead>...]               mark inbox items handled (all if none given)
  kelpie resolve <bead> [--force]             remove a finished bead's worktree and merged branch
  kelpie sweep [--yes]                        list finished linked worktrees; --yes removes the safe ones
  kelpie resume [<bead>...] [--agent K]       restart exited agents in their worktrees, continuing
                                                their last conversation (all resumable if none given)
  kelpie stale [--days N] [--release]         claims with no agent or worktree untouched N days
                                            (default 7); --release reopens them
  kelpie board                                the board (runs as the plugin popup)
  kelpie ticker run|start|stop|status         the background loop: sidebar, PR follow-up, nudges
  kelpie tick                                 one ticker pass in the foreground
  kelpie configure [--repo PATH]              first-time setup: config, ~/.local/bin link, agent view
  kelpie unconfigure                          stop the ticker, remove the view and sidebar tokens
  kelpie version
`

func main() {
	// shepherd is the old name, kept as a deprecated alias for the transition.
	if filepath.Base(os.Args[0]) == legacyName {
		fmt.Fprintln(os.Stderr, "shepherd is now kelpie; the shepherd name is deprecated and will be removed")
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "kelpie:", err)
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
	if !slices.Contains([]string{"version", "--version", "-V", "help", "--help", "-h"}, args[0]) {
		for _, msg := range migrateDirs() {
			fmt.Fprintln(os.Stderr, "kelpie:", msg)
		}
	}
	cfg, err := loadConfig()
	// configure is how a first-time install gets its config.
	if err != nil && !(errors.Is(err, errNoRepo) && len(args) > 0 && args[0] == "configure") {
		return err
	}
	h := newHerdr()
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	kind := fs.String("agent", "", "agent kind: claude or codex")
	focus := fs.Bool("focus", false, "focus the new workspace")
	repoFlag := fs.String("repo", "", "configure: the repository kelpie works in, written to config.toml if it has none")
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
		fmt.Println("kelpie", version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	case "coordinator":
		msg, err := openCoordinator(cfg, h, *kind)
		if err != nil {
			h.Notify("kelpie: coordinator failed", err.Error())
			return err
		}
		fmt.Println(msg)
	case "dispatch":
		if len(pos) != 1 {
			return fmt.Errorf("usage: kelpie dispatch <bead> [--agent claude|codex] [--focus]")
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
			h.Notify("kelpie: no bead", err.Error())
			return err
		}
		msg, err := dispatch(cfg, h, id, DispatchOpts{Kind: *kind, Focus: true})
		if err != nil {
			h.Notify("kelpie: "+id, err.Error())
			return err
		}
		h.Notify("kelpie: "+id, msg)
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
			return fmt.Errorf("usage: kelpie inbox done [<bead>...]")
		}
		n, err := inboxDone(pos[1:])
		if err != nil {
			return err
		}
		fmt.Printf("%d item(s) marked done\n", n)
	case "resolve":
		if len(pos) != 1 {
			return fmt.Errorf("usage: kelpie resolve <bead> [--force]")
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
			return fmt.Errorf("usage: kelpie action board")
		}
		return h.OpenPane(pos[0])
	case "ticker":
		if len(pos) != 1 {
			return fmt.Errorf("usage: kelpie ticker run|start|stop|status")
		}
		switch pos[0] {
		case "run":
			return tickerRun(cfg)
		case "start":
			return tickerStart(cfg)
		case "stop":
			return tickerStop()
		case "status":
			tickerStatus(cfg)
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
			fmt.Fprintln(os.Stderr, "kelpie: agent view:", err)
		}
		return tickerStart(cfg)
	case "configure":
		lines, err := configure(cfg, *repoFlag)
		fmt.Print(strings.Join(append(lines, ""), "\n"))
		// Run as a plugin action, stdout only reaches herdr's plugin log.
		if os.Getenv("HERDR_PLUGIN_CONTEXT_JSON") != "" {
			if err != nil {
				h.Notify("kelpie: setup failed", err.Error())
			} else {
				h.Notify("kelpie: set up", strings.Join(lines, "\n"))
			}
		}
		return err
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
