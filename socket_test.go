package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The routines server runs plugin startup too; its environment must not decide
// which server the ticker follows.
func routinesEnv(t *testing.T) string {
	sock := filepath.Join(t.TempDir(), "sessions", "routines", "herdr.sock")
	t.Setenv("HERDR_SOCKET_PATH", sock)
	t.Setenv("HERDR_SESSION", "routines")
	return sock
}

func TestCoordSocketIgnoresCallerServer(t *testing.T) {
	routinesEnv(t)
	if got := (Config{}).coordSocket(); got != defaultHerdrSocket() || !strings.HasSuffix(got, "/herdr/herdr.sock") {
		t.Fatalf("default: got %s", got)
	}
	if got := (Config{HerdrSocket: "/run/coord.sock"}).coordSocket(); got != "/run/coord.sock" {
		t.Fatalf("configured: got %s", got)
	}
}

func TestPinSocket(t *testing.T) {
	env := []string{"PATH=/bin", "HERDR_SOCKET_PATH=/routines.sock", "HERDR_SESSION=routines", "HERDR_PANE_ID=w1:p1"}
	got := pinSocket(env, "/coord.sock")
	want := []string{"PATH=/bin", "HERDR_PANE_ID=w1:p1", "HERDR_SOCKET_PATH=/coord.sock"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestTickerHerdrCallsFollowConfiguredSocket(t *testing.T) {
	routinesEnv(t)
	dir := t.TempDir()
	seen := filepath.Join(dir, "env")
	bin := filepath.Join(dir, "herdr")
	script := "#!/bin/sh\necho \"$HERDR_SOCKET_PATH|$HERDR_SESSION\" > " + seen + "\necho '{\"result\":{\"agents\":[]}}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", bin)
	tk := newTicker(Config{HerdrSocket: "/coord.sock"}, nil)
	if _, err := tk.herdr.Agents(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(seen)
	if s := strings.TrimSpace(string(got)); s != "/coord.sock|" {
		t.Fatalf("herdr ran with %q, want the coordinator's socket and no session", s)
	}
}

func TestRunningSocket(t *testing.T) {
	t.Setenv("FLATCIRCLE_STATE_DIR", t.TempDir())
	if err := os.WriteFile(socketFile(), []byte("/coord.sock\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := runningSocket(); got != "" {
		t.Fatalf("no ticker running, got %q", got)
	}
	if err := os.WriteFile(pidPath(), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := runningSocket(); got != "/coord.sock" {
		t.Fatalf("got %q", got)
	}
}

func TestSocketWarning(t *testing.T) {
	for _, c := range []struct {
		name, ticker, configured, here, want string
	}{
		{"unknown ticker socket", "", "/coord.sock", "/routines.sock", ""},
		{"all agree", "/coord.sock", "/coord.sock", "/coord.sock", ""},
		{"outside herdr", "/coord.sock", "/coord.sock", "", ""},
		{"ticker on another server", "/routines.sock", "/routines.sock", "/coord.sock", "not this one (/coord.sock)"},
		{"config moved", "/old.sock", "/coord.sock", "", "herdr_socket is /coord.sock"},
	} {
		got := socketWarning(c.ticker, c.configured, c.here)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRunningPIDIgnoresReusedPID(t *testing.T) {
	t.Setenv("FLATCIRCLE_STATE_DIR", t.TempDir())
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer other.Process.Kill()
	if err := os.WriteFile(pidPath(), []byte(strconv.Itoa(other.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	if pid := runningPID(); pid != 0 {
		t.Fatalf("took pid %d of an unrelated process for the ticker", pid)
	}
}

func TestTickerArgs(t *testing.T) {
	for args, want := range map[string]bool{
		"/home/tj/.config/herdr/plugins/flatcircle/bin/flatcircle ticker run\n": true,
		"flatcircle ticker run":    true,
		"sleep 30":                 false,
		"flatcircle ticker status": false,
	} {
		if got := tickerArgs(args); got != want {
			t.Errorf("tickerArgs(%q) = %v", args, got)
		}
	}
}
