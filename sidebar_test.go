package main

import (
	"bytes"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type reported struct {
	pane string
	ttl  time.Duration
}

func testSidebar(tick time.Duration) (*sidebar, *[]reported, *bytes.Buffer) {
	var calls []reported
	var buf bytes.Buffer
	s := &sidebar{
		report: func(pane string, _ map[string]string, ttl time.Duration) error {
			calls = append(calls, reported{pane, ttl})
			return nil
		},
		log:  log.New(&buf, "", 0),
		tick: tick,
	}
	return s, &calls, &buf
}

// A pass that outlasts the TTL must not blank the sidebar: between passes the
// last tokens are re-reported before they lapse.
func TestSidebarOutlivesSlowPass(t *testing.T) {
	tick := 15 * time.Second
	s, calls, buf := testSidebar(tick)
	start := time.Date(2026, 10, 2, 10, 47, 0, 0, time.UTC)
	s.set([]sidebarRow{{Name: "a", Pane: "p1"}, {Name: "b", Pane: "p2"}}, start)
	if len(*calls) != 2 {
		t.Fatalf("set reported %d rows, want 2", len(*calls))
	}

	// A fast pass reports its own rows; the keeper stays out of the way.
	s.keepAlive(start.Add(tick))
	if len(*calls) != 2 {
		t.Fatalf("keepAlive refreshed rows a tick old: %d calls", len(*calls))
	}

	// The next pass is slow (75s, as on 2026-10-02): the keeper fires every
	// tick and no report may be older than the TTL when the next one lands.
	last := start
	for at := start.Add(tick); at.Sub(start) <= 75*time.Second; at = at.Add(tick) {
		n := len(*calls)
		s.keepAlive(at)
		if len(*calls) > n {
			last = at
		}
		if at.Sub(last) >= s.ttl() {
			t.Fatalf("at +%s the last report was +%s, past the %s ttl", at.Sub(start), last.Sub(start), s.ttl())
		}
	}
	for _, c := range *calls {
		if c.ttl != 4*tick {
			t.Fatalf("reported ttl %s, want %s", c.ttl, 4*tick)
		}
	}
	if got := strings.Count(buf.String(), "slow pass"); got != 1 {
		t.Fatalf("logged %d slow-pass warnings, want 1:\n%s", got, buf)
	}

	// The slow pass finishes; its rows replace the old ones and re-arm the
	// warning.
	end := start.Add(80 * time.Second)
	*calls = nil
	s.set([]sidebarRow{{Name: "a", Pane: "p1"}}, end)
	s.keepAlive(end.Add(tick))
	if len(*calls) != 1 || (*calls)[0].pane != "p1" {
		t.Fatalf("after set: calls %v, want one report of p1", *calls)
	}
	s.keepAlive(end.Add(5 * tick))
	if got := strings.Count(buf.String(), "slow pass"); got != 2 {
		t.Fatalf("logged %d slow-pass warnings after a second slow pass, want 2", got)
	}
}

// Before the first pass there is nothing to keep alive.
func TestSidebarKeepAliveBeforeFirstPass(t *testing.T) {
	s, calls, _ := testSidebar(15 * time.Second)
	s.keepAlive(time.Now())
	if len(*calls) != 0 {
		t.Fatalf("keepAlive reported %d rows before any pass", len(*calls))
	}
}

func TestEachLimit(t *testing.T) {
	var running, peak, done atomic.Int32
	eachLimit(20, 3, func(int) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		running.Add(-1)
		done.Add(1)
	})
	if done.Load() != 20 {
		t.Fatalf("ran %d calls, want 20", done.Load())
	}
	if peak.Load() > 3 {
		t.Fatalf("ran %d at once, limit 3", peak.Load())
	}
}
