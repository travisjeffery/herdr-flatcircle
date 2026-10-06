package main

import (
	"fmt"
	"strings"
	"time"
)

// ago is a short age: 4s, 41s, 21m, 3h, 2d; "never" for a zero time.
func ago(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := max(now.Sub(t), 0)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

// statusAges is `ticker status`'s second line: when each of the ticker's
// passes last ran.
func statusAges(st TickerState, now time.Time) string {
	return fmt.Sprintf("tick %s · gh %s · nudge %s · verify %s",
		ago(st.LastTick, now), ago(st.LastGH, now), ago(st.LastNudge, now), ago(st.LastVerify, now))
}

// lateFactor is how many intervals a pass may miss before status warns: one
// slow gh or herdr call can stretch a single interval.
const lateFactor = 3

// statusWarnings is what `ticker status` flags: a pass that has stopped
// running, gh failing, or verify paused for GitHub budget. Nudges run only
// when there is news, so their age is never a warning.
func statusWarnings(cfg Config, st TickerState, now time.Time) []string {
	var out []string
	late := func(name string, last time.Time, every time.Duration) {
		if every <= 0 || last.IsZero() {
			return
		}
		if age := now.Sub(last); age > lateFactor*every {
			out = append(out, fmt.Sprintf("last %s was %s, more than %d× its %s interval", name, ago(last, now), lateFactor, every))
		}
	}
	if st.LastTick.IsZero() {
		out = append(out, "no tick recorded yet: the ticker predates last_tick or no pass has succeeded")
	}
	late("tick", st.LastTick, cfg.tick())
	late("gh pass", st.LastGH, cfg.ghEvery())
	late("verify pass", st.LastVerify, cfg.verifyEvery())
	if !st.GHFailingFor.IsZero() {
		out = append(out, fmt.Sprintf("gh PR listing failing since %s (%s)", ago(st.GHFailingFor, now), strings.Join(st.GHFailingIn, ", ")))
	}
	// GitHub's GraphQL budget resets hourly; an older reading says nothing.
	if !st.GHLeftAt.IsZero() && now.Sub(st.GHLeftAt) < time.Hour && st.GHLeft < cfg.GHMinRemaining {
		out = append(out, fmt.Sprintf("verify paused: %d GitHub GraphQL points left (%s), below gh_min_remaining %d", st.GHLeft, ago(st.GHLeftAt, now), cfg.GHMinRemaining))
	}
	return out
}

// heartbeatDue reports whether the ticker should log a heartbeat line.
func heartbeatDue(cfg Config, st TickerState, now time.Time) bool {
	every := cfg.heartbeatEvery()
	return every > 0 && now.Sub(st.LastBeat) >= every
}

// heartbeatLine is the periodic log line that shows the ticker is alive when
// nothing else happens: threads by group, inbox items, GitHub budget.
func heartbeatLine(threads []Thread, inbox int, st TickerState, now time.Time) string {
	counts := map[Group]int{}
	for _, t := range threads {
		counts[classify(t)]++
	}
	var groups []string
	for g := GroupNeedsYou; g <= GroupNoAgent; g++ {
		if n := counts[g]; n > 0 {
			groups = append(groups, fmt.Sprintf("%d %s", n, g))
		}
	}
	s := fmt.Sprintf("heartbeat: %d threads", len(threads))
	if len(groups) > 0 {
		s += " (" + strings.Join(groups, ", ") + ")"
	}
	s += fmt.Sprintf(" · %d inbox", inbox)
	if st.GHLeftAt.IsZero() {
		s += " · gh points unknown"
	} else {
		s += fmt.Sprintf(" · %d gh points left (%s)", st.GHLeft, ago(st.GHLeftAt, now))
	}
	return s
}
