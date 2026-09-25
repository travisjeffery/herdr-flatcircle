package main

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const sweepRepo = "/src/backend"

func TestClassifySweep(t *testing.T) {
	wts := []Worktree{
		{Branch: "main", Path: sweepRepo, Linked: false},
		{Branch: "main", Path: sweepRepo + "/", Linked: true},
		{Branch: "tj/merged-thing", Path: "/wt/merged", Linked: true, WorkspaceID: "w3"},
		{Branch: "tj/backend-ab12-sweep-it", Path: "/wt/closed", Linked: true},
		{Branch: "tj/wip", Path: "/wt/gone", Linked: true, Prunable: true},
		{Branch: "tj/wip-2", Path: "/wt/live", Linked: true},
		{Branch: "tj/backend-open1-x", Path: "/wt/open", Linked: true},
	}
	merged := map[string]bool{"tj/merged-thing": true, "main": true}
	closed := []Bead{{ID: "backend-ab12", Title: "Sweep it"}}

	got := map[string][]string{}
	for _, c := range classifySweep(sweepRepo, "tj/", wts, merged, closed) {
		got[c.Path] = c.Reasons
	}
	want := map[string][]string{
		"/wt/merged": {"merged"},
		"/wt/closed": {"backend-ab12 closed"},
		"/wt/gone":   {"prunable"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestBeadForBranchIgnoresSubBeadBranches(t *testing.T) {
	closed := []Bead{{ID: "backend-ep1c", Title: "Epic"}}
	if b, ok := beadForBranch("tj/", "tj/backend-ep1c-7-sweep", closed); ok {
		t.Fatalf("sub-bead branch matched closed parent %s", b.ID)
	}
	if _, ok := beadForBranch("tj/", "tj/backend-ep1c-epic", closed); !ok {
		t.Fatal("the bead's own branch did not match")
	}
}

func TestBeadForBranchMatchesSubBead(t *testing.T) {
	closed := []Bead{{ID: "backend-ep1c", Title: "Epic"}, {ID: "backend-ep1c.7", Title: "Sweep"}}
	b, ok := beadForBranch("tj/", "tj/backend-ep1c-7-sweep", closed)
	if !ok || b.ID != "backend-ep1c.7" {
		t.Fatalf("got %q %v, want backend-ep1c.7", b.ID, ok)
	}
}

func TestKeepReasonGatesUnsafeCandidates(t *testing.T) {
	cases := map[string]sweepCandidate{
		"dirty":             {Dirty: true},
		"2 unpushed":        {Unpushed: 2},
		"agent backend-x-1": {Agent: "backend-x-1"},
		"could not check":   {CheckErr: "git status: exit 128"},
	}
	for want, c := range cases {
		if got := keepReason(c); !strings.Contains(got, want) {
			t.Errorf("keepReason(%+v) = %q, want it to mention %q", c, got, want)
		}
	}
	if got := keepReason(sweepCandidate{}); got != "" {
		t.Errorf("clean candidate kept: %q", got)
	}
}

type fakeSweep struct {
	status   map[string]string
	unpushed map[string]string
	missing  map[string]bool
	calls    []string
}

func (f *fakeSweep) env(wts []Worktree, merged map[string]bool, closed []Bead, agents []Agent) sweepEnv {
	return sweepEnv{
		worktrees:   func(string) ([]Worktree, error) { return wts, nil },
		mergedHeads: func(string) (map[string]bool, error) { return merged, nil },
		closedBeads: func() ([]Bead, error) { return closed, nil },
		agents:      func() ([]Agent, error) { return agents, nil },
		exists:      func(p string) bool { return !f.missing[p] },
		git: func(dir string, args ...string) (string, error) {
			switch args[0] {
			case "status":
				return f.status[dir], nil
			case "rev-list":
				if n, ok := f.unpushed[dir]; ok {
					return n, nil
				}
				return "0\n", nil
			}
			f.calls = append(f.calls, "git -C "+dir+" "+strings.Join(args, " "))
			return "", nil
		},
		removeWorkspace: func(id string) error {
			f.calls = append(f.calls, "herdr worktree remove "+id)
			return nil
		},
	}
}

func sweepFixture() ([]Worktree, map[string]bool, []Bead, []Agent) {
	wts := []Worktree{
		{Branch: "main", Path: sweepRepo},
		{Branch: "tj/a", Path: "/wt/open-ws", Linked: true, WorkspaceID: "w7"},
		{Branch: "tj/b", Path: "/wt/no-ws", Linked: true},
		{Branch: "tj/backend-c1-bead", Path: "/wt/closed-bead", Linked: true},
		{Branch: "tj/d", Path: "/wt/dirty", Linked: true},
		{Branch: "tj/e", Path: "/wt/unpushed", Linked: true},
		{Branch: "tj/f", Path: "/wt/agent", Linked: true},
		{Branch: "tj/g", Path: "/wt/gone", Linked: true, Prunable: true},
	}
	merged := map[string]bool{"tj/a": true, "tj/b": true, "tj/d": true, "tj/e": true, "tj/f": true}
	closed := []Bead{{ID: "backend-c1", Title: "Bead"}}
	agents := []Agent{{Name: "backend-f", Cwd: "/wt/agent/sub/dir"}, {Name: "other", Cwd: "/wt/agentless"}}
	return wts, merged, closed, agents
}

func newFakeSweep() *fakeSweep {
	return &fakeSweep{
		status:   map[string]string{"/wt/dirty": " M file.go\n"},
		unpushed: map[string]string{"/wt/unpushed": "3\n"},
		missing:  map[string]bool{"/wt/gone": true},
	}
}

func TestSweepRemovesOnlySafeCandidates(t *testing.T) {
	f := newFakeSweep()
	var out strings.Builder
	if err := sweep(Config{Repo: sweepRepo, BranchPrefix: "tj/"}, f.env(sweepFixture()), true, &out); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"herdr worktree remove w7",
		"git -C /src/backend branch -D tj/a",
		"git -C /src/backend worktree remove /wt/no-ws",
		"git -C /src/backend branch -D tj/b",
		"git -C /src/backend worktree remove /wt/closed-bead",
		"git -C /src/backend worktree remove /wt/gone",
	}
	if !slices.Equal(f.calls, want) {
		t.Fatalf("calls:\n%s\nwant:\n%s\noutput:\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"), out.String())
	}
	for _, kept := range []string{"kept /wt/dirty: dirty", "kept /wt/unpushed: 3 unpushed", "kept /wt/agent: agent backend-f in it"} {
		if !strings.Contains(out.String(), kept) {
			t.Errorf("output lacks %q:\n%s", kept, out.String())
		}
	}
}

func TestSweepWithoutYesChangesNothing(t *testing.T) {
	f := newFakeSweep()
	var out strings.Builder
	if err := sweep(Config{Repo: sweepRepo, BranchPrefix: "tj/"}, f.env(sweepFixture()), false, &out); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("dry run changed things: %v", f.calls)
	}
	if !strings.Contains(out.String(), "7 candidate(s): 4 removable, 3 kept") {
		t.Fatalf("totals missing:\n%s", out.String())
	}
}

func TestSweepAbortsWhenAgentsCannotBeListed(t *testing.T) {
	f := newFakeSweep()
	env := f.env(sweepFixture())
	env.agents = func() ([]Agent, error) { return nil, errors.New("herdr down") }
	if err := sweep(Config{Repo: sweepRepo, BranchPrefix: "tj/"}, env, true, &strings.Builder{}); err == nil {
		t.Fatal("swept without knowing which worktrees have agents")
	}
	if len(f.calls) != 0 {
		t.Fatalf("removed without an agent list: %v", f.calls)
	}
}
