package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunningLegacy(t *testing.T) {
	t.Setenv("QUARTERMASTER_STATE_DIR", t.TempDir())
	if !runningLegacy() {
		t.Fatal("a ticker without ticker.name (shepherd's) was taken as current")
	}
	for name, legacy := range map[string]bool{"quartermaster\n": false, "flatcircle\n": true, "kelpie\n": true, "shepherd": true} {
		if err := os.WriteFile(nameFile(), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := runningLegacy(); got != legacy {
			t.Errorf("ticker.name %q: legacy = %v, want %v", name, got, legacy)
		}
	}
}

func TestMoveQueuedBrief(t *testing.T) {
	t.Setenv("QUARTERMASTER_STATE_DIR", t.TempDir())
	if err := queueBrief("shepherd", "first prompt"); err != nil {
		t.Fatal(err)
	}
	moveQueuedBrief("shepherd", "quartermaster")
	if got, _ := os.ReadFile(filepath.Join(outboxDir(), "quartermaster.md")); string(got) != "first prompt" {
		t.Fatalf("brief not moved: %q", got)
	}
	if _, err := os.Stat(filepath.Join(outboxDir(), "shepherd.md")); err == nil {
		t.Fatal("old brief left behind")
	}
	// A brief already queued under the new name is never overwritten.
	if err := queueBrief("shepherd", "stale"); err != nil {
		t.Fatal(err)
	}
	moveQueuedBrief("shepherd", "quartermaster")
	if got, _ := os.ReadFile(filepath.Join(outboxDir(), "quartermaster.md")); string(got) != "first prompt" {
		t.Fatalf("new brief overwritten: %q", got)
	}
}

func TestRunningTool(t *testing.T) {
	t.Setenv("QUARTERMASTER_STATE_DIR", t.TempDir())
	if got := runningTool(); got != "shepherd" {
		t.Fatalf("no ticker.name: %q", got)
	}
	if err := os.WriteFile(nameFile(), []byte("kelpie"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := runningTool(); got != "kelpie" {
		t.Fatalf("got %q", got)
	}
}

// A kelpie coordinator is adopted before a shepherd one: kelpie is the newer
// name, so a shepherd agent beside it is not the live coordinator.
func TestLegacyCoordinatorPrefersKelpie(t *testing.T) {
	cfg := Config{CoordinatorName: "quartermaster"}
	agents := []Agent{{PaneID: "w1:p1", Name: "shepherd"}, {PaneID: "w2:p1", Name: "kelpie"}}
	if a, ok := legacyCoordinator(cfg, agents); !ok || a.PaneID != "w2:p1" {
		t.Fatalf("got %+v %v", a, ok)
	}
	if _, ok := legacyCoordinator(Config{CoordinatorName: "kelpie"}, agents); ok {
		t.Fatal("coordinator_name = kelpie is not legacy")
	}
	fixes := nameFixes(cfg, nil, agents, nil, nil)
	if len(fixes) != 1 || fixes[0] != (rename{Pane: "w2:p1", From: "kelpie", To: "quartermaster"}) {
		t.Fatalf("name fixes %+v", fixes)
	}
}

func TestLegacyCoordinator(t *testing.T) {
	cfg := Config{CoordinatorName: "quartermaster"}
	old := Agent{PaneID: "w9:p1", Name: "shepherd"}
	if a, ok := legacyCoordinator(cfg, []Agent{{Name: "backend-ab12"}, old}); !ok || a.PaneID != "w9:p1" {
		t.Fatalf("got %+v %v", a, ok)
	}
	if _, ok := legacyCoordinator(cfg, []Agent{old, {Name: "quartermaster"}}); ok {
		t.Fatal("adopted shepherd with a quartermaster coordinator present")
	}
	if _, ok := legacyCoordinator(Config{CoordinatorName: "shepherd"}, []Agent{old}); ok {
		t.Fatal("coordinator_name = shepherd is not legacy")
	}
}

// openCoordinator adopts a coordinator still named shepherd instead of
// starting a second one.
func TestOpenCoordinatorAdoptsShepherd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QUARTERMASTER_STATE_DIR", dir)
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "herdr")
	script := `#!/bin/sh
echo "$*" >> ` + calls + `
case "$1 $2" in
"agent list") echo '{"result":{"agents":[{"name":"shepherd","pane_id":"w9:p1"}]}}' ;;
*) echo '{"result":{}}' ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := queueBrief("shepherd", "first prompt"); err != nil {
		t.Fatal(err)
	}
	msg, err := openCoordinator(Config{CoordinatorName: "quartermaster", CoordinatorAgent: "claude"}, Herdr{bin: bin}, "")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(calls)
	want := "agent list\nagent rename w9:p1 quartermaster\nagent focus quartermaster\n"
	if string(got) != want {
		t.Fatalf("herdr calls:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(msg, "renamed the shepherd coordinator") {
		t.Errorf("message %q", msg)
	}
	if _, err := os.Stat(filepath.Join(outboxDir(), "quartermaster.md")); err != nil {
		t.Errorf("queued brief did not follow the rename: %v", err)
	}
}

// fakeHerdrBin writes a herdr stand-in that logs its calls and answers agent
// list with agents and workspace get with label.
func fakeHerdrBin(t *testing.T, dir, agents, label string) (bin, calls string) {
	calls = filepath.Join(dir, "calls")
	bin = filepath.Join(dir, "herdr")
	script := `#!/bin/sh
echo "$*" >> ` + calls + `
case "$1 $2" in
"agent list") echo '{"result":{"agents":` + agents + `}}' ;;
"workspace get") echo '{"result":{"workspace":{"label":"` + label + `"}}}' ;;
*) echo '{"result":{}}' ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

// openCoordinator adopts a kelpie coordinator and relabels its workspace, which
// the kelpie rename left under the old name.
func TestOpenCoordinatorAdoptsKelpie(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QUARTERMASTER_STATE_DIR", dir)
	bin, calls := fakeHerdrBin(t, dir, `[{"name":"kelpie","pane_id":"w7:p1","workspace_id":"w7"}]`, "kelpie")
	msg, err := openCoordinator(Config{CoordinatorName: "quartermaster", CoordinatorAgent: "claude"}, Herdr{bin: bin}, "")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(calls)
	want := "agent list\nagent rename w7:p1 quartermaster\nworkspace get w7\nworkspace rename w7 quartermaster\nagent focus quartermaster\n"
	if string(got) != want {
		t.Fatalf("herdr calls:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(msg, "renamed the kelpie coordinator") {
		t.Errorf("message %q", msg)
	}
}

func TestRelabelCoordinatorWorkspace(t *testing.T) {
	for label, renamed := range map[string]bool{"flatcircle": true, "kelpie": true, "shepherd": true, "quartermaster": false, "my coordinator": false} {
		dir := t.TempDir()
		bin, calls := fakeHerdrBin(t, dir, `[]`, label)
		if got := relabelCoordinatorWorkspace(Herdr{bin: bin}, "w7"); got != renamed {
			t.Errorf("label %q: renamed = %v, want %v", label, got, renamed)
		}
		log, _ := os.ReadFile(calls)
		if strings.Contains(string(log), "workspace rename") != renamed {
			t.Errorf("label %q: calls %q", label, log)
		}
	}
	if relabelCoordinatorWorkspace(Herdr{bin: "/nonexistent"}, "") {
		t.Error("relabelled a coordinator with no workspace")
	}
}

// A flatcircle coordinator, the newest old name, is adopted before a kelpie or
// shepherd one and renamed quartermaster.
func TestLegacyCoordinatorPrefersFlatcircle(t *testing.T) {
	cfg := Config{CoordinatorName: "quartermaster"}
	agents := []Agent{{PaneID: "w1:p1", Name: "kelpie"}, {PaneID: "w3:p1", Name: "flatcircle"}, {PaneID: "w2:p1", Name: "shepherd"}}
	fixes := nameFixes(cfg, nil, agents, nil, nil)
	if len(fixes) != 1 || fixes[0] != (rename{Pane: "w3:p1", From: "flatcircle", To: "quartermaster"}) {
		t.Fatalf("name fixes %+v", fixes)
	}
	if _, ok := legacyCoordinator(Config{CoordinatorName: "flatcircle"}, agents); ok {
		t.Fatal("coordinator_name = flatcircle is not legacy")
	}
}

func TestAliasNotice(t *testing.T) {
	for argv0, want := range map[string]string{
		"quartermaster":          "",
		"/home/u/.local/bin/qm":  "",
		"/plugin/bin/flatcircle": "flatcircle is now quartermaster; the flatcircle name is deprecated and will be removed",
		"kelpie":                 "kelpie is now quartermaster; the kelpie name is deprecated and will be removed",
	} {
		if got := aliasNotice(argv0); got != want {
			t.Errorf("aliasNotice(%q) = %q, want %q", argv0, got, want)
		}
	}
}
