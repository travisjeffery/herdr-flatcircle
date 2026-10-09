package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// socketPath is a herdr.sock path short enough to bind: macOS caps unix socket
// paths at 104 bytes, and t.TempDir's long per-test path exceeds that.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "qm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "herdr.sock")
}

// fakeHerdr models herdr 0.9.1's view semantics (src/app/api/agent_view.rs):
// one view per server, set replaces it, clear with a source removes it only
// when that source owns it.
type fakeHerdr struct {
	view  *viewSpec
	calls []string
}

func (f *fakeHerdr) Call(method string, params any) (json.RawMessage, error) {
	f.calls = append(f.calls, method)
	b, _ := json.Marshal(params)
	switch method {
	case "agent.view.set":
		var v viewSpec
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		f.view = &v
	case "agent.view.clear":
		var p struct{ Source string }
		_ = json.Unmarshal(b, &p)
		if f.view != nil && f.view.Source == p.Source {
			f.view = nil
		}
	default:
		return nil, errors.New("unexpected method " + method)
	}
	return json.RawMessage(`{"type":"agent_view"}`), nil
}

func TestSetViewSortsByThreadRank(t *testing.T) {
	f := &fakeHerdr{}
	if err := setView(f); err != nil {
		t.Fatal(err)
	}
	if f.view == nil || f.view.Source != "plugin:quartermaster" || f.view.Label != "quartermaster" {
		t.Fatalf("view not installed as plugin:quartermaster/quartermaster: %+v", f.view)
	}
	first := f.view.Sort[0]
	if !reflect.DeepEqual(first.Field, map[string]any{"token": "sh_rank"}) || first.Order != "asc" {
		t.Fatalf("first sort key must be sh_rank ascending, got %+v", first)
	}
}

func TestSetViewIsIdempotent(t *testing.T) {
	f := &fakeHerdr{}
	_ = setView(f)
	once := *f.view
	_ = setView(f)
	_ = setView(f)
	if !reflect.DeepEqual(once, *f.view) {
		t.Fatalf("re-setting changed the view:\n%+v\n%+v", once, *f.view)
	}
}

func TestUnconfigureClearsOnlyQuartermastersView(t *testing.T) {
	f := &fakeHerdr{}
	_ = setView(f)
	var cleared []string
	agents := func() ([]Agent, error) { return []Agent{{PaneID: "w1:p1"}, {PaneID: "w2:p3"}}, nil }
	if err := unconfigure(f, agents, func(p string) { cleared = append(cleared, p) }); err != nil {
		t.Fatal(err)
	}
	if f.view != nil {
		t.Fatalf("view outlived unconfigure: %+v", f.view)
	}
	if !slices.Equal(cleared, []string{"w1:p1", "w2:p3"}) {
		t.Fatalf("tokens cleared on %v", cleared)
	}

	other := &fakeHerdr{view: &viewSpec{Source: "plugin:other", Label: "other"}}
	if err := unconfigure(other, agents, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if other.view == nil {
		t.Fatal("unconfigure removed another plugin's view")
	}
}

func TestSocketRPCWireFormat(t *testing.T) {
	path := socketPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan map[string]any, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		var req map[string]any
		_ = json.Unmarshal(line, &req)
		got <- req
		conn.Write([]byte(`{"id":"quartermaster","result":{"type":"agent_view","active":true,"source":"plugin:quartermaster"}}` + "\n"))
	}()
	if err := setView(socketRPC{path}); err != nil {
		t.Fatal(err)
	}
	req := <-got
	if req["method"] != "agent.view.set" {
		t.Fatalf("method %v", req["method"])
	}
	params := req["params"].(map[string]any)
	if params["source"] != "plugin:quartermaster" || params["label"] != "quartermaster" {
		t.Fatalf("params %v", params)
	}
}

func TestSocketRPCSurfacesHerdrErrors(t *testing.T) {
	path := socketPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		conn.Write([]byte(`{"id":"quartermaster","error":{"code":"plugin_not_found","message":"plugin not found"}}` + "\n"))
	}()
	if err := setView(socketRPC{path}); err == nil {
		t.Fatal("a herdr error response must be an error")
	}
}
