package main

import (
	"slices"
	"testing"
)

func TestNameFixes(t *testing.T) {
	cfg := Config{Repo: "/r", BranchPrefix: "tj/", CoordinatorName: "quartermaster"}
	a := Bead{ID: "backend-ab12", Title: "Fix the thing", Status: StatusInProgress}
	b := Bead{ID: "backend-cd34.2", Title: "Other", Status: StatusInProgress}
	c := Bead{ID: "backend-ef56", Title: "Shared", Status: StatusInProgress}
	d := Bead{ID: "backend-gh78", Title: "Coordinated", Status: StatusInProgress}
	wts := map[string][]Worktree{"/r": {
		{Branch: "tj/backend-ab12-fix-the-thing", Path: "/w/ab12", Linked: true},
		{Branch: "tj/eng-900-renamed-branch", Path: "/w/cd34", Linked: true},
		{Branch: "tj/backend-ef56-shared", Path: "/w/ef56", Linked: true},
		{Branch: "tj/backend-gh78-coordinated", Path: "/w/gh78", Linked: true},
	}}
	prs := map[string]PR{"backend-cd34.2": {Head: "tj/eng-900-renamed-branch"}}
	agents := []Agent{
		{PaneID: "w1:p1", Name: "herdr-quartermaster", Cwd: "/w/ab12/sub"}, // drifted name: fixed
		{PaneID: "w2:p1", Name: "", Cwd: "/w/cd34"},                        // unnamed, found via PR head: fixed
		{PaneID: "w3:p1", Name: "x", Cwd: "/w/ef56"},                       // two agents in one worktree: ambiguous
		{PaneID: "w3:p2", Name: "y", Cwd: "/w/ef56"},
		{PaneID: "w4:p1", Name: "quartermaster", Cwd: "/w/gh78"}, // the coordinator is never renamed
	}
	got := nameFixes(cfg, []Bead{a, b, c, d}, agents, prs, wts)
	want := []rename{{Pane: "w1:p1", From: "herdr-quartermaster", To: "backend-ab12"}, {Pane: "w2:p1", From: "", To: "backend-cd34-2"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}

	correct := []Agent{{PaneID: "w1:p1", Name: "backend-ab12", Cwd: "/w/ab12"}}
	if got := nameFixes(cfg, []Bead{a}, correct, nil, wts); len(got) != 0 {
		t.Fatalf("a correctly named agent was renamed: %+v", got)
	}
	// An agent named after another active bead is working that bead; leave it.
	other := []Agent{{PaneID: "w1:p1", Name: "backend-ef56", Cwd: "/w/ab12"}}
	if got := nameFixes(cfg, []Bead{a, c}, other, nil, wts); len(got) != 0 {
		t.Fatalf("stole another bead's agent: %+v", got)
	}
}

func TestNameFixesRenamesTheShepherdCoordinator(t *testing.T) {
	cfg := Config{Repo: "/r", CoordinatorName: "quartermaster"}
	old := []Agent{{PaneID: "w9:p1", Name: "shepherd", Cwd: "/s/coordinator"}}
	want := []rename{{Pane: "w9:p1", From: "shepherd", To: "quartermaster"}}
	if got := nameFixes(cfg, nil, old, nil, nil); !slices.Equal(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	// Once a quartermaster coordinator exists, a leftover shepherd agent is left alone.
	both := append(old, Agent{PaneID: "w9:p2", Name: "quartermaster"})
	if got := nameFixes(cfg, nil, both, nil, nil); len(got) != 0 {
		t.Fatalf("renamed with a quartermaster coordinator present: %+v", got)
	}
}
