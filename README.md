# shepherd

One coordinator agent, a git worktree and agent per bead, and beads (`bd`) as
the only record. A Herdr plugin; replaces herdr-projects, `herdr-bead-focus`
and the herdr-beads board.

- **Thread = bead.** `shepherd dispatch <bead>` claims the bead, creates a
  worktree on `tj/<bead>-<slug>`, starts Claude or Codex named after the bead,
  and briefs it. There is no task file: statuses, notes and `bd remember` are
  the record.
- **Coordinator.** `shepherd coordinator` starts one agent (named `shepherd`) in
  its own folder, primed to plan, create beads, dispatch and follow up, never
  to do the work itself. It reads `shepherd context` every turn.
- **Ticker.** A background loop (started by the plugin) that:
  - writes each thread's state to the sidebar (`$sh_state`): needs you, merged,
    review, checks failing, idle, working, blocked;
  - follows PRs (found by branch, or by a PR URL in the bead's notes), and
    prompts the worker when checks fail, review feedback lands, or it merges;
  - writes inbox events and notifies on needs_me / merge;
  - nudges the coordinator when there are new events;
  - only types into an agent that has sat idle for `idle_seconds`, because on
    Herdr 0.9.1 a prompt merges with half-typed input.
- **Board.** A popup listing threads by state, the `next` beads and ready beads:
  `↵` focus or start, `c`/`x` start with Claude/Codex, `n` toggle `next`,
  `y` copy the id.

## Install

```sh
go build -o bin/shepherd .
herdr plugin link "$PWD"
ln -sf "$PWD/bin/shepherd" ~/.local/bin/shepherd
```

Sidebar row and keys in `~/.config/herdr/config.toml`:

```toml
[ui.sidebar.agents]
rows = [["state_icon", "machine", "workspace", "tab"], ["agent"],
        [{ token = "$sh_state", rules = [{ starts_with = "needs you", fg = "#f38ba8", bold = true },
                                          { starts_with = "checks failing", fg = "#f38ba8" },
                                          { starts_with = "review", fg = "#f9e2af" },
                                          { starts_with = "merged", fg = "#a6e3a1" }] }]]

[[keys.command]]
key = "prefix+j"
type = "plugin_action"
command = "shepherd.board"
```

## Configuration

`~/.config/shepherd/config.toml` (only `repo` is required):

```toml
repo = "~/src/myproject"
branch_prefix = "tj/"
worker_agent = "claude"
coordinator_agent = "claude"
tick_seconds = 15
gh_seconds = 60
idle_seconds = 60
nudge = true
auto_resolve = false   # remove a merged, closed bead's worktree automatically
```

`~/.config/shepherd/instructions.md` is appended to every worker brief.

State lives in `~/.local/state/shepherd/` (`state.json`, `inbox/`, `outbox/`,
`ticker.log`).
