package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var multi = Config{
	Repo:         "/src/app",
	BranchPrefix: "tj/",
	Repos:        map[string]string{"infra": "/src/infra/", "model": "/src/model", "app": "/src/app"},
}

func labelled(id string, labels ...string) Bead {
	return Bead{ID: id, Title: "Fix the thing", Status: StatusInProgress, Labels: labels}
}

func TestRepoFor(t *testing.T) {
	cases := []struct {
		name string
		b    Bead
		want string
	}{
		{"no label is the default", labelled("b-1", "next"), "/src/app"},
		{"a configured label selects its repo", labelled("b-1", "next", "repo:model"), "/src/model"},
		{"an unknown label falls back to the default", labelled("b-1", "repo:web"), "/src/app"},
	}
	for _, c := range cases {
		if got := multi.repoFor(c.b); got != c.want {
			t.Errorf("%s: repoFor = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRepoNameReportsUnknownLabels(t *testing.T) {
	if name, ok := multi.repoName(labelled("b-1", "repo:web")); name != "web" || ok {
		t.Errorf("repoName = %q, %v; want web, false", name, ok)
	}
	if name, ok := multi.repoName(labelled("b-1")); name != "" || !ok {
		t.Errorf("repoName of an unlabelled bead = %q, %v; want \"\", true", name, ok)
	}
}

func TestAllReposDefaultFirstDeduped(t *testing.T) {
	want := []string{"/src/app", "/src/infra", "/src/model"}
	if got := multi.allRepos(); !slices.Equal(got, want) {
		t.Errorf("allRepos = %v, want %v", got, want)
	}
}

func TestRepoPRsMatchesOnlyTheBeadsRepo(t *testing.T) {
	b := labelled("b-1", "repo:infra")
	onBranch := PR{Number: 3, URL: "https://github.com/o/app/pull/3", Head: "tj/b-1-fix"}
	mine := map[string][]PR{"/src/app": {onBranch}, "/src/infra": nil}
	own, listed := repoPRs(multi, b, mine)
	if !listed {
		t.Fatal("infra was listed but repoPRs reports it failed")
	}
	if pr, _, ok := prForBead(b, multi.BranchPrefix, own, nil, nil); ok {
		t.Errorf("matched %s from another repo's listing", pr.URL)
	}
	if _, listed := repoPRs(multi, b, map[string][]PR{"/src/app": {onBranch}}); listed {
		t.Error("infra's listing failed but repoPRs reports it listed")
	}
}

func TestPROwnerIsScopedToTheBeadsRepo(t *testing.T) {
	b := labelled("b-1", "repo:infra")
	pr := PR{Number: 3, URL: "https://github.com/o/app/pull/3", Head: "tj/b-1-fix"}
	owner := prOwner(multi, []Bead{b}, map[string][]PR{"/src/app": {pr}, "/src/infra": nil})
	if id := owner(pr); id != "" {
		t.Errorf("an app PR on infra bead b-1's branch name went to %s", id)
	}
	pr.URL = "https://github.com/o/infra/pull/3"
	owner = prOwner(multi, []Bead{b}, map[string][]PR{"/src/infra": {pr}})
	if id := owner(pr); id != "b-1" {
		t.Errorf("owner = %q, want b-1", id)
	}
}

func TestContextTagsNonDefaultRepoAndWarnsOnUnknown(t *testing.T) {
	t.Setenv("QUARTERMASTER_STATE_DIR", t.TempDir())
	threads := []Thread{
		{Bead: labelled("b-1")},
		{Bead: labelled("b-2", "repo:infra")},
	}
	agentOn := &Agent{Kind: "claude", Status: "working"}
	for i := range threads {
		threads[i].Agent = agentOn
	}
	ready := []Bead{{ID: "b-3", Status: StatusOpen, Labels: []string{"repo:web"}}}
	out := renderContext(multi, threads, nil, ready, nil, TickerState{}, nil, t0)
	for _, want := range []string{
		"- b-1 · ",
		"- b-2 [infra] · ",
		"WARNING: b-3 is labelled repo:web, which is not in repos; it would use /src/app.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("context lacks %q:\n%s", want, out)
		}
	}
}

func TestLoadConfigExpandsRepos(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QUARTERMASTER_CONFIG_DIR", dir)
	t.Setenv("BEADS_DIR", dir)
	t.Setenv("HOME", "/home/me")
	toml := "repo = \"~/src/app\"\nrepos = { infra = \"~/src/infra\", model = \"/opt/model\" }\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.repoFor(labelled("b-1", "repo:infra")); got != "/home/me/src/infra" {
		t.Errorf("repo:infra resolves to %q, want /home/me/src/infra", got)
	}
	if got := cfg.repoFor(labelled("b-1", "repo:model")); got != "/opt/model" {
		t.Errorf("repo:model resolves to %q, want /opt/model", got)
	}
}
