package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// A "plugin:<id>" source makes the view plugin-owned: herdr clears it itself
// when the plugin is unlinked, uninstalled or disabled, so it cannot outlive
// quartermaster the way herdr-projects' plain-source view did.
const (
	viewSource = "plugin:" + toolName
	viewLabel  = toolName
)

// legacyViewSources are cleared too: flatcircle's, kelpie's and shepherd's plugin views from
// before the renames, and a view set under a bare name by hand.
var legacyViewSources = func() []string {
	out := []string{toolName}
	for _, n := range legacyNames {
		out = append(out, "plugin:"+n, n)
	}
	return out
}()

// rpc is herdr's socket API: one JSON request per line, one response per line.
// Views have no CLI subcommand.
type rpc interface {
	Call(method string, params any) (json.RawMessage, error)
}

type socketRPC struct{ path string }

// herdrSocket is the socket of the herdr server this process runs under.
func herdrSocket() string {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return p
	}
	return defaultHerdrSocket()
}

// defaultHerdrSocket is the socket of herdr's default (unnamed) session.
func defaultHerdrSocket() string {
	return filepath.Join(configBase(), "herdr", "herdr.sock")
}

func (s socketRPC) Call(method string, params any) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", s.path, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	req, err := json.Marshal(map[string]any{"id": toolName, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("herdr %s: %s: %s", method, resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

type viewSort struct {
	Field any    `json:"field"`
	Order string `json:"order"`
}

type viewSpec struct {
	Source string     `json:"source"`
	Label  string     `json:"label"`
	Sort   []viewSort `json:"sort"`
}

// quartermasterView sorts threads by the ticker's sh_rank token ("<group>-<bead>",
// needs you first). Herdr orders an agent without the token after every agent
// with it, so non-thread agents follow the threads and nothing is hidden. A
// view's sort replaces agent_panel_sort, so attention and recency come back as
// tie-breakers.
func quartermasterView() viewSpec {
	return viewSpec{
		Source: viewSource,
		Label:  viewLabel,
		Sort: []viewSort{
			{Field: map[string]string{"token": "sh_rank"}, Order: "asc"},
			{Field: "attention", Order: "desc"},
			{Field: "state_change_seq", Order: "desc"},
		},
	}
}

// setView installs quartermaster's view. Herdr holds a single view and a set
// replaces it, so calling this on every startup or configure is idempotent.
func setView(c rpc) error {
	_, err := c.Call("agent.view.set", quartermasterView())
	return err
}

// clearView removes quartermaster's view and leaves any other source's view alone.
func clearView(c rpc) error {
	for _, source := range append([]string{viewSource}, legacyViewSources...) {
		if _, err := c.Call("agent.view.clear", map[string]string{"source": source}); err != nil {
			return err
		}
	}
	return nil
}

// unconfigure removes everything quartermaster put into herdr: its view and the
// sidebar tokens on every agent pane.
func unconfigure(c rpc, agents func() ([]Agent, error), clearTokens func(pane string)) error {
	if err := clearView(c); err != nil {
		return err
	}
	list, err := agents()
	if err != nil {
		return err
	}
	for _, a := range list {
		clearTokens(a.PaneID)
	}
	return nil
}
