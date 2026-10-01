package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunningLegacy(t *testing.T) {
	t.Setenv("KELPIE_STATE_DIR", t.TempDir())
	if !runningLegacy() {
		t.Fatal("a ticker without ticker.name (shepherd's) was taken as current")
	}
	for name, legacy := range map[string]bool{"kelpie\n": false, "shepherd": true} {
		if err := os.WriteFile(nameFile(), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := runningLegacy(); got != legacy {
			t.Errorf("ticker.name %q: legacy = %v, want %v", name, got, legacy)
		}
	}
}

func TestMoveQueuedBrief(t *testing.T) {
	t.Setenv("KELPIE_STATE_DIR", t.TempDir())
	if err := queueBrief("shepherd", "first prompt"); err != nil {
		t.Fatal(err)
	}
	moveQueuedBrief("shepherd", "kelpie")
	if got, _ := os.ReadFile(filepath.Join(outboxDir(), "kelpie.md")); string(got) != "first prompt" {
		t.Fatalf("brief not moved: %q", got)
	}
	if _, err := os.Stat(filepath.Join(outboxDir(), "shepherd.md")); err == nil {
		t.Fatal("old brief left behind")
	}
	// A brief already queued under the new name is never overwritten.
	if err := queueBrief("shepherd", "stale"); err != nil {
		t.Fatal(err)
	}
	moveQueuedBrief("shepherd", "kelpie")
	if got, _ := os.ReadFile(filepath.Join(outboxDir(), "kelpie.md")); string(got) != "first prompt" {
		t.Fatalf("new brief overwritten: %q", got)
	}
}

func TestLegacyCoordinator(t *testing.T) {
	cfg := Config{CoordinatorName: "kelpie"}
	old := Agent{PaneID: "w9:p1", Name: "shepherd"}
	if a, ok := legacyCoordinator(cfg, []Agent{{Name: "backend-ab12"}, old}); !ok || a.PaneID != "w9:p1" {
		t.Fatalf("got %+v %v", a, ok)
	}
	if _, ok := legacyCoordinator(cfg, []Agent{old, {Name: "kelpie"}}); ok {
		t.Fatal("adopted shepherd with a kelpie coordinator present")
	}
	if _, ok := legacyCoordinator(Config{CoordinatorName: "shepherd"}, []Agent{old}); ok {
		t.Fatal("coordinator_name = shepherd is not legacy")
	}
}

// openCoordinator adopts a coordinator still named shepherd instead of
// starting a second one.
func TestOpenCoordinatorAdoptsShepherd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KELPIE_STATE_DIR", dir)
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
	msg, err := openCoordinator(Config{CoordinatorName: "kelpie", CoordinatorAgent: "claude"}, Herdr{bin: bin}, "")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(calls)
	want := "agent list\nagent rename w9:p1 kelpie\nagent focus kelpie\n"
	if string(got) != want {
		t.Fatalf("herdr calls:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(msg, "renamed the shepherd coordinator") {
		t.Errorf("message %q", msg)
	}
	if _, err := os.Stat(filepath.Join(outboxDir(), "kelpie.md")); err != nil {
		t.Errorf("queued brief did not follow the rename: %v", err)
	}
}
