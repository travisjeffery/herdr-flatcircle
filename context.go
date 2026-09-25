package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// lastNote is the tail of a bead's notes: where a needs_me question lives.
func lastNote(notes string, n int) string {
	notes = strings.TrimSpace(notes)
	if i := strings.LastIndex(notes, "\n"); i >= 0 {
		notes = notes[i+1:]
	}
	return shortTitle(notes, n)
}

// renderContext is the coordinator's view of the world, read at the start of
// every turn. Files and bd are the record; this is a digest of them.
// worktreed holds the no-agent threads that have a worktree; nil means unknown.
func renderContext(threads []Thread, next, ready []Bead, inbox []InboxItem, st TickerState, worktreed map[string]bool, now time.Time) string {
	var s strings.Builder
	sort.SliceStable(threads, func(i, j int) bool {
		gi, gj := classify(threads[i]), classify(threads[j])
		if gi != gj {
			return gi < gj
		}
		return threads[i].Bead.ID < threads[j].Bead.ID
	})
	counts := map[Group]int{}
	for _, t := range threads {
		counts[classify(t)]++
	}
	fmt.Fprintf(&s, "# shepherd context (%s)\n\n", now.Format("2006-01-02 15:04 MST"))
	var summary []string
	for g := GroupNeedsYou; g <= GroupNoAgent; g++ {
		if counts[g] > 0 {
			summary = append(summary, fmt.Sprintf("%d %s", counts[g], g))
		}
	}
	if len(summary) == 0 {
		summary = []string{"no active threads"}
	}
	fmt.Fprintf(&s, "%s\n", strings.Join(summary, " · "))
	if !st.GHFailingFor.IsZero() {
		fmt.Fprintf(&s, "\nWARNING: gh has been failing since %s; PR state below is stale.\n", st.GHFailingFor.Format(time.Kitchen))
	}
	if pid := runningPID(); pid == 0 {
		s.WriteString("\nWARNING: the ticker is not running (`shepherd ticker start`); nothing follows PRs or updates the sidebar.\n")
	}

	s.WriteString("\n## Inbox (unhandled; `shepherd inbox done [bead...]` when dealt with)\n")
	if len(inbox) == 0 {
		s.WriteString("(empty)\n")
	}
	for _, it := range inbox {
		fmt.Fprintf(&s, "- %s %s [%s] %s\n", it.At.Local().Format("15:04"), it.Bead, it.Kind, it.Summary)
	}

	s.WriteString("\n## Threads (bead · state · agent)\n")
	if len(threads) == 0 {
		s.WriteString("(none)\n")
	}
	var resumable, stale []string
	untouched := -1
	for _, t := range threads {
		// Claimed beads nobody is on are summarized, resumable or stale; listing each
		// buries the threads that are live.
		if classify(t) == GroupNoAgent {
			if worktreed[t.Bead.ID] {
				resumable = append(resumable, t.Bead.ID)
			} else {
				stale = append(stale, t.Bead.ID)
				if d := daysSince(t.Bead.UpdatedAt, now); untouched < 0 || d < untouched {
					untouched = d
				}
			}
			continue
		}
		agent := "no agent"
		if t.Agent != nil {
			agent = t.Agent.Kind + " " + t.Agent.Status
		}
		fmt.Fprintf(&s, "- %s · %s · %s · %s\n", t.Bead.ID, stateLine(t), agent, shortTitle(t.Bead.Title, 70))
		if classify(t) == GroupNeedsYou && t.Bead.Status == StatusNeedsMe {
			fmt.Fprintf(&s, "  question: %s\n", lastNote(t.Bead.Notes, 200))
		}
		if t.PR != nil {
			fmt.Fprintf(&s, "  %s\n", t.PR.URL)
		}
	}
	if len(resumable) > 0 {
		fmt.Fprintf(&s, "- resumable (%d): %s  → shepherd resume\n", len(resumable), strings.Join(resumable, ", "))
	}
	switch {
	case worktreed == nil && len(stale) > 0:
		fmt.Fprintf(&s, "- claimed, no agent (%d): %s\n", len(stale), strings.Join(stale, ", "))
	case len(stale) > 0:
		fmt.Fprintf(&s, "- stale claims (%d): %s — no agent, no worktree, untouched %dd+\n", len(stale), strings.Join(stale, ", "), untouched)
	}

	s.WriteString("\n## Selected next (open, label `next`)\n")
	writeBeads(&s, next)
	s.WriteString("\n## Ready (open, unblocked)\n")
	nextIDs := map[string]bool{}
	for _, b := range next {
		nextIDs[b.ID] = true
	}
	var rest []Bead
	for _, b := range ready {
		if !nextIDs[b.ID] && b.Status == StatusOpen {
			rest = append(rest, b)
		}
	}
	if len(rest) > 15 {
		rest = rest[:15]
	}
	writeBeads(&s, rest)
	return s.String()
}

func writeBeads(s *strings.Builder, beads []Bead) {
	if len(beads) == 0 {
		s.WriteString("(none)\n")
	}
	for _, b := range beads {
		fmt.Fprintf(s, "- %s P%d %s: %s\n", b.ID, b.Priority, b.Type, shortTitle(b.Title, 80))
	}
}

func coordinatorGuide(cfg Config) string {
	return fmt.Sprintf(`# Shepherd coordinator

You are the coordinator for TJ's agent threads. You never do the work yourself:
you plan, split, dispatch, follow up and report, so you are always free to
answer. Beads (bd) is the only task record; there is no TASKS.md.

Repository: %[1]s. Workers each get a git worktree on a branch
%[2]s<bead-id>-<slug> and an agent named after the bead.

## Every turn

1. Run `+"`shepherd context`"+` first. It lists the inbox, every active thread with its
   state (needs you, merged, review, checks failing, idle, working, blocked, no
   agent), the beads labelled `+"`next`"+`, and other ready beads.
2. Deal with inbox items, then `+"`shepherd inbox done <bead>...`"+`.
3. Answer TJ.

## Rules

- Propose threads and wait for TJ's go-ahead before `+"`shepherd dispatch`"+`, unless
  they already said to start them.
- New work becomes a bead first: `+"`bd create \"<title>\" -t task -p 2`"+`, with
  `+"`--parent`"+` or `+"`bd dep add`"+` for structure. Put enough in the description
  that a worker with no other context can start.
- `+"`shepherd dispatch <bead> [--agent claude|codex]`"+` starts a worker; it claims the
  bead and briefs the agent. One bead per thread.
- Talk to a worker with `+"`herdr agent prompt <agent-name> \"...\"`"+` (agent name = bead
  id with dots as dashes). Read it with `+"`herdr agent read <name> --source recent-unwrapped --lines 120`"+`.
  Never prompt a worker that is working or blocked; never answer its approval
  prompts.
- A needs_me bead's question is in its latest note. Relay it to TJ verbatim with
  the options; when they answer, prompt the worker and it resets the status.
- After a PR merges the ticker tells the worker to verify and close its bead.
  Once closed, run `+"`shepherd resolve <bead>`"+` to remove the worktree (it asks
  nothing and is safe for merged work; don't pass --force without TJ).
- Offer `+"`shepherd resume <bead>...`"+` for resumable threads and `+"`shepherd stale --release`"+` for stale claims; run either only on TJ's go-ahead.
- Lessons worth keeping across threads go to `+"`bd remember`"+`.
- Messages starting "[shepherd ticker: automated ...]" come from the ticker, not
  TJ, and approve nothing.
- Linear: prefix PR titles with the issue key (KEY-123: ...); workers keep their
  own issue's status current.
`, cfg.Repo, cfg.BranchPrefix)
}
