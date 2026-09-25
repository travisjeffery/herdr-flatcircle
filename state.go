package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Group is where a thread sits from the user's point of view. The order is the
// sidebar and board order: what needs the user first.
type Group int

const (
	GroupNeedsYou Group = iota
	GroupMerged
	GroupReview
	GroupFailing
	GroupIdle
	GroupWorking
	GroupBlocked
	GroupNoAgent
)

var groupNames = map[Group]string{
	GroupNeedsYou: "needs you",
	GroupMerged:   "merged",
	GroupReview:   "review",
	GroupFailing:  "checks failing",
	GroupIdle:     "idle",
	GroupWorking:  "working",
	GroupBlocked:  "blocked",
	GroupNoAgent:  "no agent",
}

func (g Group) String() string { return groupNames[g] }

// Thread is one bead with whatever is working on it and delivering it.
type Thread struct {
	Bead    Bead
	Agent   *Agent
	PR      *PR
	Checks  Checks
	Reviews int // reviews by someone other than the user
}

func agentReady(a *Agent) bool {
	return a != nil && (a.Status == "idle" || a.Status == "done")
}

// classify decides a thread's group. Precedence matters: a human owed an
// answer outranks everything, and a merged PR outranks review because the
// only thing left is closing up.
func classify(t Thread) Group {
	switch {
	case t.Bead.Status == StatusNeedsMe, t.Agent != nil && t.Agent.Status == "blocked":
		return GroupNeedsYou
	case t.PR != nil && t.PR.State == "MERGED":
		return GroupMerged
	case t.PR != nil && t.PR.State == "OPEN" && len(t.Checks.Failing) > 0:
		return GroupFailing
	case t.Agent != nil && t.Agent.Status == "working":
		return GroupWorking
	case t.PR != nil && t.PR.State == "OPEN" && agentReady(t.Agent):
		return GroupReview
	case t.Bead.Status == StatusBlocked:
		return GroupBlocked
	case agentReady(t.Agent), t.Agent != nil:
		return GroupIdle
	default:
		return GroupNoAgent
	}
}

// pendingCommand is the command a worker asked the user to run, from its
// latest "RUN:" note, until a later "RAN:" or "DONE:" note answers it.
func pendingCommand(notes string) string {
	lines := strings.Split(notes, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "RAN:") || strings.HasPrefix(l, "DONE:") {
			return ""
		}
		if cmd, ok := strings.CutPrefix(l, "RUN:"); ok {
			return strings.TrimSpace(cmd)
		}
	}
	return ""
}

// runCommand is the pending command of a needs_me thread, the only state in
// which one is still waiting on the user.
func runCommand(t Thread) string {
	if t.Bead.Status != StatusNeedsMe {
		return ""
	}
	return pendingCommand(t.Bead.Notes)
}

func stateLine(t Thread) string {
	g := classify(t)
	s := g.String()
	if runCommand(t) != "" {
		s += " · run command"
	}
	if t.PR != nil {
		s += fmt.Sprintf(" · PR #%d", t.PR.Number)
		switch {
		case g == GroupFailing:
			s += " (" + strings.Join(t.Checks.Failing, ", ") + ")"
		case t.PR.State == "OPEN" && t.PR.ReviewDecision == "CHANGES_REQUESTED":
			s += " changes requested"
		case t.PR.State == "OPEN" && t.PR.ReviewDecision == "APPROVED":
			s += " approved"
		case t.PR.State == "OPEN" && t.Checks.Pending > 0:
			s += " checks running"
		}
	}
	return s
}

// Snapshot is what the ticker remembers about a bead between ticks.
type Snapshot struct {
	Group       Group     `json:"group"`
	AgentStatus string    `json:"agent_status"`
	AgentSeq    int64     `json:"agent_seq"`
	ReadySince  time.Time `json:"ready_since"`
	PRNumber    int       `json:"pr_number"`
	PRState     string    `json:"pr_state"`
	Failing     []string  `json:"failing"`
	Reviews     int       `json:"reviews"`
	Pending     []string  `json:"pending_prompts"`
}

type EventKind string

const (
	EventNeedsYou  EventKind = "needs_you"
	EventFailing   EventKind = "checks_failing"
	EventReview    EventKind = "new_review"
	EventMerged    EventKind = "merged"
	EventFinished  EventKind = "finished"
	EventAgentGone EventKind = "agent_gone"
)

type Event struct {
	Bead    string    `json:"bead"`
	Kind    EventKind `json:"kind"`
	Summary string    `json:"summary"`
	At      time.Time `json:"at"`
}

// Outcome is what one thread's transition asks the ticker to do.
type Outcome struct {
	Events []Event
	// Prompts go to the thread's agent once it has been ready long enough.
	Prompts []string
	Notify  bool
}

func snapshot(t Thread, prev Snapshot, now time.Time) Snapshot {
	s := Snapshot{Group: classify(t), Reviews: t.Reviews, Failing: t.Checks.Failing, Pending: prev.Pending}
	if t.Agent != nil {
		s.AgentStatus, s.AgentSeq = t.Agent.Status, t.Agent.Seq
		switch {
		case !agentReady(t.Agent):
		case agentReady(&Agent{Status: prev.AgentStatus}) && prev.AgentSeq == t.Agent.Seq && !prev.ReadySince.IsZero():
			s.ReadySince = prev.ReadySince
		default:
			s.ReadySince = now
		}
	}
	if t.PR != nil {
		s.PRNumber, s.PRState = t.PR.Number, t.PR.State
	}
	return s
}

// transition compares a thread against its last snapshot. first is true the
// first time the ticker sees a bead, when nothing counts as a change.
func transition(t Thread, prev Snapshot, first bool, now time.Time) Outcome {
	var o Outcome
	if first {
		return o
	}
	id := t.Bead.ID
	g := classify(t)
	ev := func(k EventKind, format string, a ...any) {
		o.Events = append(o.Events, Event{Bead: id, Kind: k, Summary: fmt.Sprintf(format, a...), At: now})
	}
	if g == GroupNeedsYou && prev.Group != GroupNeedsYou {
		o.Notify = true
		if t.Agent != nil && t.Agent.Status == "blocked" {
			ev(EventNeedsYou, "%s is waiting at an approval or question prompt", id)
		} else {
			ev(EventNeedsYou, "%s is marked needs_me; its latest note holds the question", id)
		}
	}
	if t.PR != nil && t.PR.State == "OPEN" {
		if len(t.Checks.Failing) > 0 && !slices.Equal(t.Checks.Failing, prev.Failing) {
			ev(EventFailing, "PR #%d checks failing: %s", t.PR.Number, strings.Join(t.Checks.Failing, ", "))
			o.Prompts = append(o.Prompts, fmt.Sprintf(
				"[shepherd: automated, not the user] PR #%d has failing checks: %s. Investigate with `gh pr checks %d`, fix them on this branch, and push.",
				t.PR.Number, strings.Join(t.Checks.Failing, ", "), t.PR.Number))
		}
		if t.Reviews > prev.Reviews && prev.PRNumber == t.PR.Number {
			ev(EventReview, "PR #%d has new review feedback", t.PR.Number)
			o.Prompts = append(o.Prompts, fmt.Sprintf(
				"[shepherd: automated, not the user] PR #%d has new review feedback. Read it with `gh pr view %d --comments` and the review threads; fix what is valid, reply to what is not, and push.",
				t.PR.Number, t.PR.Number))
		}
	}
	if t.PR != nil && t.PR.State == "MERGED" && prev.PRState != "MERGED" {
		o.Notify = true
		ev(EventMerged, "PR #%d merged", t.PR.Number)
		if t.Bead.Status != StatusClosed {
			o.Prompts = append(o.Prompts, fmt.Sprintf(
				"[shepherd: automated, not the user] PR #%d merged. Verify what needs verifying after merge, then close the bead with `bd close %s --reason \"<what shipped and how it was verified>\"` and stop.",
				t.PR.Number, id))
		}
	}
	// A whole turn can fit between two ticks; a moved state counter on a ready
	// agent means it worked since the last look.
	turned := prev.AgentStatus == "working" || (prev.AgentSeq != 0 && t.Agent != nil && t.Agent.Seq != prev.AgentSeq)
	if t.Agent != nil && agentReady(t.Agent) && turned && g != GroupNeedsYou {
		ev(EventFinished, "%s finished a turn (%s)", id, stateLine(t))
	}
	if t.Agent == nil && prev.AgentStatus != "" {
		ev(EventAgentGone, "%s's agent exited while the bead is still %s", id, t.Bead.Status)
	}
	return o
}

// deliverable reports whether the ticker may type into an agent now: it has
// been ready, untouched, for at least idle.
func deliverable(s Snapshot, now time.Time, idle time.Duration) bool {
	return !s.ReadySince.IsZero() && now.Sub(s.ReadySince) >= idle
}
