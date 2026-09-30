package main

import (
	"slices"
	"testing"
)

func testBoard() []boardRow {
	th := thread(bead(StatusInProgress), agent("working", 1), openPR())
	th.PR.URL = "https://github.com/acme/widgets/pull/7"
	th.Linear = "INF-42"
	r := boardRow{bead: th.Bead, agent: th.Agent, line: stateLine(th)}
	r.search = searchText("threads", th.Bead.ID, th.Bead.Title, th.Agent.Name, r.line, "#7 "+th.PR.URL, th.Linear)
	next := Bead{ID: "backend-cd34", Title: "Rotate the keys", Priority: 1, Type: "task"}
	ready := Bead{ID: "backend-ef56", Title: "Tidy the docs", Priority: 3, Type: "chore"}
	return []boardRow{
		{header: "threads"},
		r,
		{header: "next"},
		{bead: next, search: searchText("next", next.ID, next.Title, "P1 task")},
		{header: "ready"},
		{bead: ready, search: searchText("ready", ready.ID, ready.Title, "P3 chore")},
	}
}

func shown(rows []boardRow) []string {
	var out []string
	for _, r := range rows {
		if r.header != "" {
			out = append(out, "["+r.header+"]")
		} else {
			out = append(out, r.bead.ID)
		}
	}
	return out
}

func TestFilterRows(t *testing.T) {
	cases := []struct {
		query string
		want  []string
	}{
		{"", []string{"[threads]", "backend-ab12", "[next]", "backend-cd34", "[ready]", "backend-ef56"}},
		{"ROTATE", []string{"[next]", "backend-cd34"}},     // title, any case
		{"ef56", []string{"[ready]", "backend-ef56"}},      // id
		{"inf-42", []string{"[threads]", "backend-ab12"}},  // Linear key
		{"#7", []string{"[threads]", "backend-ab12"}},      // PR number
		{"widgets", []string{"[threads]", "backend-ab12"}}, // PR repo
		{"working", []string{"[threads]", "backend-ab12"}}, // group
		{"ready", []string{"[ready]", "backend-ef56"}},     // section
		{"the next", []string{"[next]", "backend-cd34"}},   // every word, anywhere
		{"nomatch", nil},
	}
	for _, c := range cases {
		if got := shown(filterRows(testBoard(), c.query)); !slices.Equal(got, c.want) {
			t.Errorf("filter %q = %v, want %v", c.query, got, c.want)
		}
	}
}

func TestFilteredSelectionSkipsHeaders(t *testing.T) {
	rows := filterRows(testBoard(), "tidy")
	if cur := nextSelectable(rows, 0, 1); cur != 1 || rows[cur].bead.ID != "backend-ef56" {
		t.Fatalf("selection = %d, want the only match", cur)
	}
	if cur := nextSelectable(filterRows(testBoard(), "nomatch"), 0, 1); cur != -1 {
		t.Fatalf("selection on an empty board = %d, want -1", cur)
	}
}

func TestBoardFilterKeys(t *testing.T) {
	f := boardFilter{typing: true}
	for _, k := range []string{"a", "b", "É", "\x1b[A", "\x7f"} {
		f.key(k)
	}
	if f.query != "ab" || !f.typing {
		t.Fatalf("after typing = %+v, want query ab still typing", f)
	}
	f.key("\r")
	if f.query != "ab" || f.typing {
		t.Fatalf("enter = %+v, want query kept and typing off", f)
	}
	f = boardFilter{query: "ab", typing: true}
	if !f.key("\x1b") || f.query != "" || f.typing {
		t.Fatalf("esc = %+v, want cleared", f)
	}
	f = boardFilter{query: "a", typing: true}
	if f.key("\x7f"); f.key("\x7f") {
		t.Fatal("backspace on an empty query reported a change")
	}
}
