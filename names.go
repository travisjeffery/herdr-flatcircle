package main

import (
	"path/filepath"
	"strings"
)

type rename struct {
	Pane, From, To string
}

// nameFixes finds agents working an active bead under the wrong name: the only
// agent inside the bead's linked worktree, while no agent carries the bead's
// name. Quartermaster ties an agent to its bead by name alone, so a misnamed
// worker drops out of the sidebar, the prompts and the inbox. An agent already
// named after another active bead, or the coordinator, is never renamed. A
// coordinator still named flatcircle, kelpie or shepherd, from before a rename,
// becomes quartermaster's.
func nameFixes(cfg Config, active []Bead, agents []Agent, prs map[string]PR, wts map[string][]Worktree) []rename {
	named := map[string]bool{}
	for _, a := range agents {
		named[a.Name] = true
	}
	claimed := map[string]bool{cfg.CoordinatorName: true}
	for _, n := range legacyNames {
		claimed[n] = true
	}
	for _, b := range active {
		claimed[agentName(b.ID)] = true
	}
	var out []rename
	if a, ok := legacyCoordinator(cfg, agents); ok {
		out = append(out, rename{Pane: a.PaneID, From: a.Name, To: cfg.CoordinatorName})
		named[cfg.CoordinatorName] = true
	}
	for _, b := range active {
		want := agentName(b.ID)
		if named[want] {
			continue
		}
		wt, ok := beadWorktree(cfg, b, prs, wts[filepath.Clean(cfg.repoFor(b))])
		if !ok {
			continue
		}
		var inside []Agent
		for _, a := range agents {
			if a.Cwd == wt.Path || strings.HasPrefix(a.Cwd, wt.Path+string(filepath.Separator)) {
				inside = append(inside, a)
			}
		}
		if len(inside) != 1 || claimed[inside[0].Name] {
			continue
		}
		out = append(out, rename{Pane: inside[0].PaneID, From: inside[0].Name, To: want})
		named[want] = true
	}
	return out
}

// legacyCoordinator is the coordinator agent still under an old name (newest
// first: flatcircle, kelpie, shepherd), from before a rename, while no agent carries the
// coordinator's name. A coordinator_name set to an old name is not legacy.
func legacyCoordinator(cfg Config, agents []Agent) (Agent, bool) {
	if isLegacyName(cfg.CoordinatorName) {
		return Agent{}, false
	}
	byName := map[string]Agent{}
	for _, a := range agents {
		if _, seen := byName[a.Name]; !seen {
			byName[a.Name] = a
		}
	}
	if _, ok := byName[cfg.CoordinatorName]; ok {
		return Agent{}, false
	}
	for _, n := range legacyNames {
		if a, ok := byName[n]; ok {
			return a, true
		}
	}
	return Agent{}, false
}
