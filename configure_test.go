package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flatcircle", "config.toml")
	top := func(d string) string {
		if d == "/ws/app/sub" {
			return "/ws/app"
		}
		return ""
	}

	if _, err := ensureConfig(path, "", "", func(string) string { return "" }); err == nil {
		t.Fatal("no repo anywhere must be an error")
	}
	msg, err := ensureConfig(path, "", "/ws/app/sub", top)
	if err != nil || !strings.Contains(msg, "repo = /ws/app") {
		t.Fatalf("the workspace's repo should be used: %q %v", msg, err)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), `repo = "/ws/app"`) {
		t.Fatalf("config: %s", b)
	}
	if msg, _ := ensureConfig(path, "/other", "", top); !strings.Contains(msg, "left it alone") {
		t.Fatalf("an existing config must not be rewritten: %q", msg)
	}
	if b2, _ := os.ReadFile(path); string(b2) != string(b) {
		t.Fatal("config changed")
	}

	flagged := filepath.Join(dir, "flag", "config.toml")
	if msg, err := ensureConfig(flagged, "/ws/flag", "/ws/app/sub", top); err != nil || !strings.Contains(msg, "/ws/flag") {
		t.Fatalf("--repo wins over the workspace: %q %v", msg, err)
	}
}

func TestLinkCLI(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "plugin", "bin", "flatcircle")
	os.MkdirAll(filepath.Dir(self), 0o755)
	os.WriteFile(self, []byte("x"), 0o755)
	target := filepath.Join(dir, "bin", "flatcircle")

	if msg, err := linkCLI(self, target); err != nil || !strings.HasPrefix(msg, "linked") {
		t.Fatalf("%q %v", msg, err)
	}
	if msg, _ := linkCLI(self, target); !strings.Contains(msg, "already links") {
		t.Fatalf("repeat should be a no-op: %q", msg)
	}

	old := filepath.Join(dir, "old", "flatcircle")
	os.MkdirAll(filepath.Dir(old), 0o755)
	os.WriteFile(old, []byte("y"), 0o755)
	os.Remove(target)
	os.Symlink(old, target)
	if msg, err := linkCLI(self, target); err != nil || !strings.HasPrefix(msg, "linked") {
		t.Fatalf("an old link should be replaced: %q %v", msg, err)
	}
	if dest, _ := filepath.EvalSymlinks(target); dest != self {
		t.Fatalf("points at %s", dest)
	}

	os.Remove(target)
	os.WriteFile(target, []byte("mine"), 0o755)
	if msg, _ := linkCLI(self, target); !strings.Contains(msg, "real file") {
		t.Fatalf("a real file must be left alone: %q", msg)
	}
	if b, _ := os.ReadFile(target); string(b) != "mine" {
		t.Fatal("overwrote a real file")
	}
}
