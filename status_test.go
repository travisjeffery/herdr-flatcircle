package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAgo(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		since time.Duration
		want  string
	}{
		{0, "0s ago"},
		{4 * time.Second, "4s ago"},
		{59 * time.Second, "59s ago"},
		{time.Minute, "1m ago"},
		{21*time.Minute + 30*time.Second, "21m ago"},
		{3*time.Hour + 59*time.Minute, "3h ago"},
		{47 * time.Hour, "47h ago"},
		{49 * time.Hour, "2d ago"},
		{-5 * time.Second, "0s ago"}, // a clock step must not print a negative age
	}
	for _, c := range cases {
		if got := ago(now.Add(-c.since), now); got != c.want {
			t.Errorf("ago(%s) = %q, want %q", c.since, got, c.want)
		}
	}
	if got := ago(time.Time{}, now); got != "never" {
		t.Errorf("ago(zero) = %q, want never", got)
	}
}

func TestStatusAges(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	st := TickerState{LastTick: now.Add(-4 * time.Second), LastGH: now.Add(-41 * time.Second), LastVerify: now.Add(-21 * time.Minute)}
	want := "tick 4s ago · gh 41s ago · nudge never · verify 21m ago"
	if got := statusAges(st, now); got != want {
		t.Errorf("statusAges = %q, want %q", got, want)
	}
}

func TestStatusWarnings(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	cfg := defaultConfig() // tick 15s, gh 60s, verify 60m, gh_min_remaining 1000
	fresh := TickerState{LastTick: now.Add(-45 * time.Second), LastGH: now.Add(-3 * time.Minute), LastVerify: now.Add(-3 * time.Hour)}
	has := func(ws []string, sub string) bool {
		return slices.ContainsFunc(ws, func(w string) bool { return strings.Contains(w, sub) })
	}

	if ws := statusWarnings(cfg, fresh, now); len(ws) != 0 {
		t.Errorf("exactly 3× each interval: want no warnings, got %q", ws)
	}

	late := fresh
	late.LastTick = now.Add(-46 * time.Second)
	late.LastGH = now.Add(-181 * time.Second)
	late.LastVerify = now.Add(-3*time.Hour - time.Second)
	ws := statusWarnings(cfg, late, now)
	for _, sub := range []string{"last tick", "last gh pass", "last verify pass"} {
		if !has(ws, sub) {
			t.Errorf("past 3×: want a %q warning, got %q", sub, ws)
		}
	}

	off := cfg
	off.VerifyMinutes = 0
	if ws := statusWarnings(off, late, now); has(ws, "verify pass") {
		t.Errorf("verify off: want no verify warning, got %q", ws)
	}

	if ws := statusWarnings(cfg, TickerState{}, now); !has(ws, "no tick recorded") || len(ws) != 1 {
		t.Errorf("empty state: want only the no-tick warning, got %q", ws)
	}

	failing := fresh
	failing.GHFailingFor, failing.GHFailingIn = now.Add(-5*time.Minute), []string{"/src/a"}
	if ws := statusWarnings(cfg, failing, now); !has(ws, "failing since 5m ago (/src/a)") {
		t.Errorf("gh failing: got %q", ws)
	}

	low := fresh
	low.GHLeft, low.GHLeftAt = 812, now.Add(-10*time.Minute)
	if ws := statusWarnings(cfg, low, now); !has(ws, "verify paused: 812 GitHub GraphQL points left (10m ago), below gh_min_remaining 1000") {
		t.Errorf("low budget: got %q", ws)
	}
	low.GHLeftAt = now.Add(-61 * time.Minute)
	if ws := statusWarnings(cfg, low, now); has(ws, "verify paused") {
		t.Errorf("budget reading older than its hourly reset: want no warning, got %q", ws)
	}
	low.GHLeft, low.GHLeftAt = 1000, now
	if ws := statusWarnings(cfg, low, now); has(ws, "verify paused") {
		t.Errorf("at the floor: want no warning, got %q", ws)
	}
}

func TestHeartbeat(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	cfg := defaultConfig()
	if heartbeatDue(cfg, TickerState{}, now) {
		t.Error("heartbeat is off by default")
	}
	cfg.HeartbeatMinutes = 10
	if !heartbeatDue(cfg, TickerState{}, now) {
		t.Error("first heartbeat: want due")
	}
	if heartbeatDue(cfg, TickerState{LastBeat: now.Add(-9 * time.Minute)}, now) {
		t.Error("9m after the last: want not due")
	}
	if !heartbeatDue(cfg, TickerState{LastBeat: now.Add(-10 * time.Minute)}, now) {
		t.Error("10m after the last: want due")
	}

	threads := []Thread{
		thread(bead(StatusNeedsMe), agent("idle", 1), nil),
		thread(bead(StatusInProgress), agent("working", 1), nil),
		thread(bead(StatusInProgress), agent("working", 1), nil),
	}
	st := TickerState{GHLeft: 4210, GHLeftAt: now.Add(-21 * time.Minute)}
	want := "heartbeat: 3 threads (1 needs you, 2 working) · 2 inbox · 4210 gh points left (21m ago)"
	if got := heartbeatLine(threads, 2, st, now); got != want {
		t.Errorf("heartbeatLine = %q, want %q", got, want)
	}
	want = "heartbeat: 0 threads · 0 inbox · gh points unknown"
	if got := heartbeatLine(nil, 0, TickerState{}, now); got != want {
		t.Errorf("heartbeatLine(empty) = %q, want %q", got, want)
	}
}
