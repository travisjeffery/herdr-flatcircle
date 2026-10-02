package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateDirMovesAndLinks(t *testing.T) {
	root := t.TempDir()
	legacy, dir := filepath.Join(root, "shepherd"), filepath.Join(root, "flatcircle")
	if err := os.MkdirAll(filepath.Join(legacy, "inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	item := filepath.Join("inbox", "1-backend-x-finished.json")
	if err := os.WriteFile(filepath.Join(legacy, item), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if msgs := migrateDir(legacy, dir); len(msgs) != 1 || !strings.HasPrefix(msgs[0], "moved ") {
		t.Fatalf("first run: %q", msgs)
	}
	if _, err := os.Stat(filepath.Join(dir, item)); err != nil {
		t.Fatalf("inbox item not at the new path: %v", err)
	}
	// The old path still reaches the same files, for an older running ticker.
	if _, err := os.Stat(filepath.Join(legacy, item)); err != nil {
		t.Fatalf("old path no longer reaches the inbox: %v", err)
	}
	if fi, err := os.Lstat(legacy); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("old path is not a link: %v", err)
	}
	if msgs := migrateDir(legacy, dir); len(msgs) != 0 {
		t.Fatalf("second run is not a no-op: %q", msgs)
	}
}

func TestMigrateDirLeavesBothWhenNewExists(t *testing.T) {
	root := t.TempDir()
	legacy, dir := filepath.Join(root, "shepherd"), filepath.Join(root, "flatcircle")
	for _, d := range []string{legacy, dir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if msgs := migrateDir(legacy, dir); len(msgs) != 1 || !strings.HasPrefix(msgs[0], "both ") {
		t.Fatalf("got %q", msgs)
	}
	if fi, err := os.Lstat(legacy); err != nil || !fi.IsDir() {
		t.Fatalf("legacy dir was touched: %v", err)
	}
	if migrateDir(filepath.Join(root, "missing"), filepath.Join(root, "new")) != nil {
		t.Fatal("a missing legacy dir produced a message")
	}
}

func TestStateDirReadsTheLegacyPathUntilMigrated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// migrateDirs moves the config dir too, found through XDG_CONFIG_HOME.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	base := filepath.Join(home, ".local", "state")
	if got, want := stateDir(), filepath.Join(base, "flatcircle"); got != want {
		t.Fatalf("fresh install: got %s, want %s", got, want)
	}
	if err := os.MkdirAll(filepath.Join(base, "shepherd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := stateDir(), filepath.Join(base, "shepherd"); got != want {
		t.Fatalf("only the legacy dir: got %s, want %s", got, want)
	}
	migrateDirs()
	if got, want := stateDir(), filepath.Join(base, "flatcircle"); got != want {
		t.Fatalf("after migrating: got %s, want %s", got, want)
	}
	t.Setenv("SHEPHERD_STATE_DIR", "/legacy/env")
	if got := stateDir(); got != "/legacy/env" {
		t.Fatalf("SHEPHERD_STATE_DIR ignored: %s", got)
	}
}

// The kelpie migration left kelpie as the real dir and shepherd linked to it.
// Migrating to flatcircle moves kelpie, and both old paths still reach the same
// inbox, for a kelpie or shepherd ticker still running.
func TestMigrateDirsFromKelpie(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	item := filepath.Join("inbox", "1-backend-x-finished.json")
	for _, base := range []string{filepath.Join(home, ".config"), filepath.Join(home, ".local", "state")} {
		if err := os.MkdirAll(filepath.Join(base, "kelpie", "inbox"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "kelpie", item), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(base, "kelpie"), filepath.Join(base, "shepherd")); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := stateDir(), filepath.Join(home, ".local", "state", "kelpie"); got != want {
		t.Fatalf("before migrating: got %s, want %s", got, want)
	}
	if msgs := migrateDirs(); len(msgs) != 2 {
		t.Fatalf("want one move each for config and state, got %q", msgs)
	}
	if got, want := configDir(), filepath.Join(home, ".config", "flatcircle"); got != want {
		t.Fatalf("config dir: got %s, want %s", got, want)
	}
	if got, want := stateDir(), filepath.Join(home, ".local", "state", "flatcircle"); got != want {
		t.Fatalf("state dir: got %s, want %s", got, want)
	}
	for _, old := range []string{"kelpie", "shepherd"} {
		p := filepath.Join(home, ".local", "state", old)
		if fi, err := os.Lstat(p); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s is not a link: %v", p, err)
		}
		if _, err := os.Stat(filepath.Join(p, item)); err != nil {
			t.Fatalf("%s no longer reaches the inbox: %v", old, err)
		}
	}
	if msgs := migrateDirs(); len(msgs) != 0 {
		t.Fatalf("second run is not a no-op: %q", msgs)
	}
}

func TestStateDirEnvNames(t *testing.T) {
	t.Setenv("KELPIE_STATE_DIR", "/kelpie/env")
	if got := stateDir(); got != "/kelpie/env" {
		t.Fatalf("KELPIE_STATE_DIR ignored: %s", got)
	}
	t.Setenv("FLATCIRCLE_STATE_DIR", "/flatcircle/env")
	if got := stateDir(); got != "/flatcircle/env" {
		t.Fatalf("FLATCIRCLE_STATE_DIR does not win: %s", got)
	}
}
