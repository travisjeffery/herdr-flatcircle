package main

import (
	"log"
	"sync"
	"time"
)

// sidebarRow is one pane's sidebar tokens, named by the bead or agent it shows
// for logging.
type sidebarRow struct {
	Name   string
	Pane   string
	Tokens map[string]string
}

// sidebar holds the tokens the last pass reported and keeps them alive while
// a pass runs longer than a tick. Herdr drops tokens at their TTL so a dead
// ticker shows as gone, but a pass blocked on gh, Linear or a verify sweep can
// run far past any fixed TTL; refreshing between passes keeps a slow ticker
// from looking dead. A ticker that has exited still lets them lapse.
type sidebar struct {
	report func(pane string, tokens map[string]string, ttl time.Duration) error
	log    *log.Logger
	tick   time.Duration

	mu   sync.Mutex
	rows []sidebarRow
	at   time.Time // when rows were last reported
	// warned marks the pass already logged as outlasting the TTL.
	warned bool
}

func newSidebar(h Herdr, tick time.Duration, logger *log.Logger) *sidebar {
	return &sidebar{report: h.ReportState, log: logger, tick: tick}
}

// ttl outlives the refresh interval by a margin, so one late refresh never
// blanks the sidebar.
func (s *sidebar) ttl() time.Duration { return 4 * s.tick }

// set replaces the rows with a pass's and reports them.
func (s *sidebar) set(rows []sidebarRow, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = rows
	s.warned = false
	s.flush(now)
}

// keepAlive re-reports the last rows when no pass has reported them for two
// ticks, and warns once per pass that outlasts the TTL. Called every tick, it
// refreshes rows by 3 ticks old, inside the 4-tick TTL.
func (s *sidebar) keepAlive(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.at.IsZero() || now.Sub(s.at) < 2*s.tick {
		return
	}
	if took := now.Sub(s.at); took > s.ttl() && !s.warned {
		s.warned = true
		s.log.Printf("slow pass: no sidebar update for %s, past the %s ttl; refreshing the last tokens", took.Round(time.Second), s.ttl())
	}
	s.flushRows()
}

func (s *sidebar) flush(now time.Time) {
	s.flushRows()
	s.at = now
}

func (s *sidebar) flushRows() {
	for _, r := range s.rows {
		if err := s.report(r.Pane, r.Tokens, s.ttl()); err != nil {
			s.log.Printf("sidebar %s: %v", r.Name, err)
		}
	}
}

// run calls keepAlive every tick until done is closed.
func (s *sidebar) run(done <-chan struct{}) {
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-t.C:
			s.keepAlive(now)
		}
	}
}
