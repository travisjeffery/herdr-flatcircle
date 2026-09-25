# shepherd

Run many coding agents in parallel from one conversation.

You talk to one coordinator. It turns what you want into
[beads](https://github.com/steveyegge/beads), starts a worker for each one in
its own git worktree, and keeps checking on them for you, so you're only pulled
in when someone needs a decision. It's a [Herdr](https://herdr.dev) plugin: the
workers are ordinary Claude Code or Codex sessions in Herdr panes, and their
state shows in Herdr's sidebar.

## Why

- **One place to coordinate from.** A single coordinator session plans, splits
  work into beads, dispatches workers and relays their questions. It never
  does the work itself, so it's always free to answer you.
- **Workers get checked on automatically.** A background ticker follows every
  thread on a schedule: it tells a worker when its PR's checks fail, when review
  feedback lands and when it merges, and nudges the coordinator when there's
  news. You don't poll agents; the ones that need you rise to the top of the
  sidebar.
- **Worktrees and workers are handled for you.** One bead gets one branch, one
  worktree and one agent named after the bead. Starting a thread is one key;
  cleaning up after a merge is one command.
- **Beads is the only state.** No task file or database of shepherd's own:
  statuses, notes and memories live in `bd`, which many agents can update at
  once. That's what lets you run many threads without them stepping on each
  other, and what lets any session, including the coordinator after a restart,
  pick up exactly where things are.

## A day with it

1. Open the coordinator (`prefix+shift+j`) and say what you want done.
2. It proposes beads and threads; you say go. Each worker starts in its own
   worktree with a short brief.
3. Carry on. The sidebar shows every thread: **needs you**, **checks failing**,
   **review**, **merged**, **working**, **idle**, sorted so what needs you is
   first.
4. When a worker needs a decision, it marks its bead `needs_me` with the
   question; you get a notification and the coordinator relays it.
5. When a PR fails checks or gets review comments, the ticker sends that back to
   the worker. When it merges, the worker verifies and closes its bead, and
   `shepherd resolve` removes the worktree.

The board (`prefix+j`) lists threads by state plus the beads you've marked
`next` and other ready beads: `↵` focus or start, `c`/`x` start with Claude or
Codex, `n` toggle `next`, `y` copy the id.

## Requirements

Herdr 0.9.1+, [`bd`](https://github.com/steveyegge/beads) with a custom
`needs_me` status (`bd config set status.custom needs_me`), `gh` logged in, Go
to build, and Claude Code and/or Codex.

## Install

```sh
git clone https://github.com/travisjeffery/herdr-shepherd && cd herdr-shepherd
go build -o bin/shepherd .
herdr plugin link "$PWD"
ln -sf "$PWD/bin/shepherd" ~/.local/bin/shepherd
mkdir -p ~/.config/shepherd && echo 'repo = "~/src/myproject"' > ~/.config/shepherd/config.toml
shepherd configure
```

Add the sidebar row and keys to `~/.config/herdr/config.toml`:

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

[[keys.command]]
key = "prefix+shift+j"
type = "plugin_action"
command = "shepherd.coordinator"
```

`shepherd.focus-clipboard` (Claude) and `shepherd.focus-clipboard-codex` focus
or start the bead whose id is on the clipboard, if you want keys for those too.

## Configuration

`~/.config/shepherd/config.toml`; only `repo` is required:

```toml
repo = "~/src/myproject"
branch_prefix = "tj/"     # worker branches are <prefix><bead>-<slug>
worker_agent = "claude"   # or "codex"
coordinator_agent = "claude"
tick_seconds = 15         # how often the ticker checks threads
gh_seconds = 60           # how often it checks PRs
idle_seconds = 60         # it only types into an agent idle this long
nudge = true              # prompt the coordinator when there's news
auto_resolve = false      # remove a merged, closed bead's worktree automatically
```

`~/.config/shepherd/instructions.md` is added to every worker's brief.

## How it works

- **Threads.** `shepherd dispatch <bead>` claims the bead, creates the worktree,
  starts the agent and sends the brief. If the agent opens on a startup prompt
  (folder trust), the brief waits in an outbox until you answer it.
- **PRs** are found by branch name or by a PR link in the bead's notes, so a
  bead worked on a differently named branch is still followed.
- **The ticker** only types into an agent that has been idle for
  `idle_seconds`: on Herdr 0.9.1 a prompt merges with whatever you've
  half-typed.
- **The sidebar sort** is a Herdr agent view owned by `plugin:shepherd`. Herdr
  drops it when the plugin is unlinked, uninstalled or disabled;
  `shepherd unconfigure` stops the ticker and removes the view and sidebar
  tokens by hand.
- **The coordinator** reads `shepherd context` each turn: an inbox of events,
  every active thread and the beads ready to start.

State lives in `~/.local/state/shepherd/` (`state.json`, `inbox/`, `outbox/`,
`ticker.log`).
