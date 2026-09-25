package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

var validKinds = map[string]bool{"claude": true, "codex": true}

// brief is the first prompt a worker gets. It stays short: the bead holds the
// task, bd prime (SessionStart hook) holds the workflow and memories.
func brief(b Bead, branch, path, instructions string) string {
	var s strings.Builder
	fmt.Fprintf(&s, "You are the shepherd worker for bead %s: %s\n", b.ID, b.Title)
	fmt.Fprintf(&s, "It is already claimed for you. Worktree %s on branch %s. Start with `bd show %s`.\n\n", path, branch, b.ID)
	s.WriteString(`How this thread reports (the shepherd ticker and coordinator read these, not your chat):
- Keep the bead current with ` + "`bd note`" + `: findings, decisions, blockers.
- When you open a PR, ` + "`bd note " + b.ID + " \"PR: <url>\"`" + ` so the ticker follows it. It will prompt you when checks fail, review feedback lands, or it merges.
- If you need a human decision: put the exact question and options in a note, run ` + "`bd update " + b.ID + " --status needs_me`" + `, and stop.
- If a step needs the user to run a command themselves (a permission or classifier denial, an interactive login, a production change they must make): ` + "`bd note " + b.ID + " \"RUN: <exact command>\"`" + `, a one-line note of why, ` + "`bd update " + b.ID + " --status needs_me`" + `, and stop. Once they report the result, continue and ` + "`bd update " + b.ID + " --status in_progress`" + `.
- Don't close the bead before the PR merges; after it merges, verify and ` + "`bd close " + b.ID + " --reason \"...\"`" + `.
- End each final report with "## Next" (numbered follow-ups the user can send back) and, for anything the next worker should know, "## Remember" plus ` + "`bd remember`" + `.
`)
	if strings.TrimSpace(instructions) != "" {
		s.WriteString("\nStanding instructions:\n")
		s.WriteString(strings.TrimSpace(instructions))
		s.WriteString("\n")
	}
	return s.String()
}

type DispatchOpts struct {
	Kind  string
	Focus bool
}

// dispatch starts a worker on a bead, or focuses the one already on it.
func dispatch(cfg Config, h Herdr, id string, o DispatchOpts) (string, error) {
	name := agentName(id)
	if agents, err := h.Agents(); err == nil {
		for _, a := range agents {
			if a.Name == name {
				if o.Focus {
					return "focused the agent already on " + id, h.Focus(name)
				}
				return id + " already has an agent (" + a.PaneID + ")", nil
			}
		}
	}
	kind := o.Kind
	if kind == "" {
		kind = cfg.WorkerAgent
	}
	if kind == "auto" {
		kind = autoAgent(time.Now())
	}
	if !validKinds[kind] {
		return "", fmt.Errorf("unknown agent kind %q (claude, codex or auto)", kind)
	}
	bd := Beads{actor: kind}
	b, err := bd.Show(id)
	if err != nil {
		return "", err
	}
	if b.Status == StatusClosed {
		return "", fmt.Errorf("%s is closed", id)
	}
	branch := branchFor(cfg.BranchPrefix, b)
	opened, err := h.WorktreeCreate(cfg.Repo, branch, cfg.BaseBranch, name, o.Focus)
	if err != nil {
		// A resumed bead already has its branch.
		var err2 error
		if opened, err2 = h.WorktreeOpen(cfg.Repo, branch, name, o.Focus); err2 != nil {
			return "", fmt.Errorf("worktree for %s: %v; open: %v", branch, err, err2)
		}
	}
	if err := bd.Claim(id); err != nil && b.Status != StatusInProgress {
		return "", fmt.Errorf("claim %s: %w", id, err)
	}
	tab := opened.PaneID[:strings.LastIndex(opened.PaneID, ":")] + ":t1"
	_ = h.TabRename(tab, shortTitle(b.Title, 34))
	text := brief(b, branch, opened.Path, readInstructions())
	if err := h.AgentStart(name, kind, opened.PaneID); err != nil {
		// A new worktree often opens on the agent's folder-trust prompt. The
		// ticker delivers the brief once someone answers it.
		if !strings.Contains(err.Error(), "agent_not_ready") {
			return "", fmt.Errorf("start %s in %s: %w", kind, opened.PaneID, err)
		}
		if err := queueBrief(name, text); err != nil {
			return "", err
		}
		h.Notify("shepherd: "+id+" is waiting for you", "Answer its startup prompt (folder trust?) in "+opened.PaneID+"; the brief follows.")
		return fmt.Sprintf("started %s on %s (%s, branch %s); it is at a startup prompt, the brief is queued", kind, id, opened.PaneID, branch), nil
	}
	if err := h.Prompt(name, text); err != nil {
		return "", fmt.Errorf("brief %s: %w", name, err)
	}
	return fmt.Sprintf("started %s on %s (%s, branch %s)", kind, id, opened.PaneID, branch), nil
}

// resolve removes a finished bead's linked worktree and, once its PR merged,
// its local branch. It only ever touches a linked worktree herdr lists for the
// bead's branch, never the main checkout or a workspace other agents share.
func resolve(cfg Config, h Herdr, id string, force bool) (string, error) {
	b, err := Beads{}.Show(id)
	if err != nil {
		return "", err
	}
	if b.Status != StatusClosed && !force {
		return "", fmt.Errorf("%s is %s; close it first or pass --force", id, b.Status)
	}
	if agents, err := h.Agents(); err == nil {
		for _, a := range agents {
			if a.Name == agentName(id) && a.Status == "working" && !force {
				return "", fmt.Errorf("%s's agent is still working", id)
			}
		}
	}
	branches := []string{branchFor(cfg.BranchPrefix, b)}
	if pr, ok := loadState().PRs[id]; ok && pr.Head != "" && pr.Head != branches[0] {
		branches = append(branches, pr.Head)
	}
	wts, err := h.Worktrees(cfg.Repo)
	if err != nil {
		return "", err
	}
	var target *Worktree
	for i, w := range wts {
		if w.Linked && slices.Contains(branches, w.Branch) {
			target = &wts[i]
			break
		}
	}
	if target == nil {
		return "nothing to remove: no linked worktree on " + strings.Join(branches, " or "), nil
	}
	if filepath.Clean(target.Path) == filepath.Clean(cfg.Repo) {
		return "", fmt.Errorf("refusing to remove the main checkout %s", target.Path)
	}
	if target.WorkspaceID != "" {
		if err := h.WorktreeRemove(target.WorkspaceID); err != nil {
			return "", err
		}
	} else if out, err := exec.Command("git", "-C", cfg.Repo, "worktree", "remove", target.Path).CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree remove: %v: %s", err, strings.TrimSpace(string(out)))
	}
	done := []string{"removed worktree " + target.Path}
	if merged(cfg, target.Branch) {
		if exec.Command("git", "-C", cfg.Repo, "branch", "-D", target.Branch).Run() == nil {
			done = append(done, "deleted merged branch "+target.Branch)
		}
	} else {
		done = append(done, "kept branch "+target.Branch+" (no merged PR)")
	}
	return strings.Join(done, "; "), nil
}

func merged(cfg Config, branch string) bool {
	out, err := GH{repo: cfg.Repo}.run("pr", "list", "--head", branch, "--state", "merged", "--json", "number", "--jq", "length")
	return err == nil && strings.TrimSpace(string(out)) != "0"
}

// clipboardBead reads a bead id from the Wayland clipboard (y in the board).
func clipboardBead() (string, error) {
	out, err := exec.Command("wl-paste", "-n").Output()
	if err != nil {
		return "", errors.New("clipboard is empty")
	}
	id := strings.TrimSpace(string(out))
	if id == "" || strings.ContainsAny(id, " \t\n/") {
		return "", fmt.Errorf("clipboard does not hold a bead id: %q", shortTitle(id, 40))
	}
	return id, nil
}

// coordinatorHome is where the coordinator agent runs: a folder of its own so
// it never edits the repository, primed by AGENTS.md and CLAUDE.md.
func coordinatorHome() string { return filepath.Join(stateDir(), "coordinator") }

func openCoordinator(cfg Config, h Herdr, kind string) (string, error) {
	if agents, err := h.Agents(); err == nil {
		for _, a := range agents {
			if a.Name == cfg.CoordinatorName {
				return "focused the coordinator", h.Focus(a.Name)
			}
		}
	}
	if kind == "" {
		kind = cfg.CoordinatorAgent
	}
	if !validKinds[kind] {
		return "", fmt.Errorf("unknown agent kind %q", kind)
	}
	home := coordinatorHome()
	if err := os.MkdirAll(home, 0o755); err != nil {
		return "", err
	}
	for _, f := range []string{"AGENTS.md", "CLAUDE.md"} {
		if err := os.WriteFile(filepath.Join(home, f), []byte(coordinatorGuide(cfg)), 0o644); err != nil {
			return "", err
		}
	}
	opened, err := h.WorkspaceCreate(home, "shepherd", true)
	if err != nil {
		return "", err
	}
	_ = tickerStart()
	first := "Run `shepherd context` and give me a short status: what needs me, what's in review, what's ready to start."
	if err := h.AgentStart(cfg.CoordinatorName, kind, opened.PaneID); err != nil {
		if !strings.Contains(err.Error(), "agent_not_ready") {
			return "", err
		}
		if err := queueBrief(cfg.CoordinatorName, first); err != nil {
			return "", err
		}
		h.Notify("shepherd: coordinator is waiting for you", "Answer its startup prompt (folder trust?); its first prompt follows.")
		return "started the coordinator in " + opened.PaneID + "; it is at a startup prompt", nil
	}
	if err := h.Prompt(cfg.CoordinatorName, first); err != nil {
		return "", err
	}
	return "started the coordinator in " + opened.PaneID, nil
}

// The outbox holds briefs for agents that were not ready when dispatched, one
// file per agent name.
func outboxDir() string { return filepath.Join(stateDir(), "outbox") }

func queueBrief(agent, text string) error {
	return writeAtomic(filepath.Join(outboxDir(), agent+".md"), []byte(text))
}

// deliverOutbox sends queued briefs to agents that have become ready.
func deliverOutbox(h Herdr, agents map[string]Agent) []string {
	entries, err := os.ReadDir(outboxDir())
	if err != nil {
		return nil
	}
	var sent []string
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".md")
		a, ok := agents[name]
		if !ok || !agentReady(&a) {
			continue
		}
		path := filepath.Join(outboxDir(), e.Name())
		text, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if h.Prompt(a.PaneID, string(text)) == nil {
			os.Remove(path)
			sent = append(sent, name)
		}
	}
	return sent
}
