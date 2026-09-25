package main

import (
	"strings"
	"testing"
)

func TestPendingCommand(t *testing.T) {
	cases := []struct {
		name, notes, want string
	}{
		{"none", "looked at the logs\nPR: https://x/pull/1", ""},
		{"latest RUN wins", "RUN: aws sso login\nwhy: expired\nRUN:  aws iam create-policy-version --policy-arn a  \nwhy: classifier blocked it", "aws iam create-policy-version --policy-arn a"},
		{"RAN answers it", "RUN: aws sso login\nRAN: logged in", ""},
		{"DONE answers it", "RUN: aws sso login\nDONE: ok", ""},
		{"a RUN after RAN is pending again", "RUN: a\nRAN: a\nRUN: b", "b"},
		{"only a prefix at line start counts", "I will RUN: x later", ""},
	}
	for _, c := range cases {
		if got := pendingCommand(c.notes); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestStateLineRunCommand(t *testing.T) {
	b := bead(StatusNeedsMe)
	b.Notes = "RUN: aws sso login --profile admin"
	if got := stateLine(thread(b, agent("idle", 1), nil)); got != "needs you · run command" {
		t.Errorf("got %q", got)
	}
	b.Status = StatusInProgress
	if got := stateLine(thread(b, agent("idle", 1), nil)); got != "idle" {
		t.Errorf("a RUN note on a bead that is not needs_me is history, got %q", got)
	}
}

func TestContextShowsTheCommandToRun(t *testing.T) {
	b := bead(StatusNeedsMe)
	b.Notes = "RUN: aws sso login --profile admin"
	out := renderContext(Config{Repo: "/r"}, []Thread{thread(b, agent("idle", 1), nil)}, nil, nil, nil, TickerState{}, nil, t0)
	if !strings.Contains(out, "\n  run: aws sso login --profile admin\n") || strings.Contains(out, "question:") {
		t.Fatalf("want the run line alone when the RUN note is the latest:\n%s", out)
	}
	b.Notes += "\nwhy: the token expired"
	out = renderContext(Config{Repo: "/r"}, []Thread{thread(b, agent("idle", 1), nil)}, nil, nil, nil, TickerState{}, nil, t0)
	if !strings.Contains(out, "  run: aws sso login --profile admin\n  question: why: the token expired\n") {
		t.Fatalf("want the run line then the latest note:\n%s", out)
	}
}

func TestBoardStatusShowsTheSelectedCommand(t *testing.T) {
	rows := []boardRow{{header: "threads"}, {bead: bead(StatusNeedsMe), run: "aws sso login"}, {bead: bead(StatusInProgress)}}
	if got := statusFor(rows, 1, ""); got != "run: aws sso login  (p copies)" {
		t.Errorf("got %q", got)
	}
	if got := statusFor(rows, 1, "copied the command"); got != "copied the command" {
		t.Errorf("an action's message wins, got %q", got)
	}
	if got := statusFor(rows, 2, ""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestBriefAsksForRunNotes(t *testing.T) {
	s := brief(bead(StatusInProgress), "tj/x", "/w/x", "", "")
	if !strings.Contains(s, `bd note backend-ab12 "RUN: <exact command>"`) || !strings.Contains(s, "bd update backend-ab12 --status in_progress") {
		t.Fatalf("brief lacks the RUN rule:\n%s", s)
	}
}

func TestAnsweredRunCommandIsNotOfferedAgain(t *testing.T) {
	notes := "RUN: terraform apply -auto-approve\nneeds the SSO-only production role\nprovision applied, verified alerts load\nQuestion: roll to all regions or one first?"
	if got := pendingCommand(notes); got != "" {
		t.Fatalf("an old RUN: resurfaced after the thread moved on: %q", got)
	}
	if got := pendingCommand("earlier note\nRUN: gh auth login\nneeds an interactive browser login"); got != "gh auth login" {
		t.Fatalf("RUN: plus its why should be pending, got %q", got)
	}
}
