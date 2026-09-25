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

## Getting started

Once it's installed (see below), the first ten minutes:

1. **Point it at a repo.** `~/.config/shepherd/config.toml` needs one line,
   `repo = "~/src/myproject"`. Beads should already be set up there
   (`bd init`) with the `needs_me` status added.
2. **Open the coordinator** with `prefix+shift+j`. It starts in a folder of its
   own, reads `shepherd context` and gives you a status. The first time, Claude
   asks whether to trust that folder; say yes.
3. **Give it something small.** "Add a `--json` flag to the `export` command."
   It creates a bead, proposes one thread and waits.
4. **Say go.** A new workspace opens with a worktree on
   `<prefix><bead>-add-a-json-flag`, and an agent named after the bead starts
   and gets its brief. Its row appears in the sidebar as **working**.
5. **Leave it.** When the worker opens a PR it notes the link on the bead, and
   from then on the ticker follows it. You'll see **review · PR #N** when it's
   ready for you.

## A day with shepherd

What this looks like on a real working day for someone who owns a service and
its infrastructure. Bead ids, PR numbers and names are made up.

**9:00: catching up.** You open the coordinator and ask where things stand. It
runs `shepherd context`, which starts like this (trimmed):

```
1 needs you · 1 checks failing · 2 idle

## Inbox (unhandled; `shepherd inbox done [bead...]` when dealt with)
- 02:14 app-8zk4 [closed] app-8zk4 closed after PR #402 merged; run `shepherd resolve app-8zk4` to remove its worktree
- 04:40 app-c71m [checks_failing] PR #405 checks failing: integration-tests

## Threads (bead · state · agent)
- app-3fq1 · needs you · claude idle · Roll the new retry policy out to the worker fleet
  question: Staging has been clean for 12h. Roll to all regions at once, or one region first?
- app-c71m · checks failing · PR #405 (integration-tests) · claude working · Upgrade the HTTP client library
```

Overnight, #405's integration tests failed. The ticker already told that
worker, and it's fixing them, so there's nothing for you to do there. #402
merged, and its worker verified the change and closed its bead. You answer the
one question: "One region first, then the rest after an hour of clean
metrics." The coordinator passes that to the worker, which resets its bead to
`in_progress` and carries on. Then the coordinator runs
`shepherd resolve app-8zk4`, which removes the merged thread's worktree and
branch.

**9:20: new work from yesterday's incident.** You paste your incident notes: a
DNS resolver saturated under load, requests timed out, and the existing alerts
said nothing because they watch error rates, not demand. You want an alert on
resolver request rate, the connection timeout in the API service raised to
match its retry budget, and a timeline written up for the postmortem.

The coordinator creates a parent bead with three children. It notes that the
postmortem should cite the alert's threshold, so it adds a dependency and
proposes:

> Start now: **app-q2d8** (resolver request-rate alert) and **app-q2d9**
> (API connection timeout). **app-q2da** (postmortem timeline) waits on
> app-q2d8. Claude for both? Go?

You say go. Two workspaces open, each on its own branch and worktree, and both
rows show **working**.

**10:30: the ticker does the chasing.** The alert worker opens PR #411 and
notes the link on its bead. The PR's lint check fails, and the row turns red:
**checks failing · PR #411 (lint)**. You don't touch it. Once the worker has sat
idle for a minute, the ticker sends it the failing check. It fixes the lint
error and pushes, and the row goes back to **working**, then
**review · PR #411 checks running**.

On the timeout PR a teammate asks for the new value to be configurable rather
than hard-coded. The ticker sees the new review and hands it to that worker,
which makes the change, replies on the thread and pushes. You found out from
the sidebar, not by refreshing GitHub.

**11:15: a worker needs you.** The alert worker wants to run the dry run of the
alert rules against production metrics, and its agent stops at a permission
prompt. That's **needs you** at the top of the sidebar. You press `↵` on its
row in the board, look at the command, approve it, and go back to what you
were doing.

**13:00: one-offs.** A colleague posts a bead id in chat: an intermittent test
failure that needs a look. You copy it and press your focus-clipboard key
(`shepherd.focus-clipboard`), and a worker starts on it without going through
the coordinator. Anything with a bead can
be started from the clipboard or from the board (`prefix+j`, then `c` for
Claude or `x` for Codex).

**15:30: things land.** #411 is approved and merged. Its row shows **merged**,
and the ticker tells the worker, which checks that the rule actually loaded and
then closes `app-q2d8`. That unblocks the postmortem bead; the coordinator
mentions it in its next status and, when you say so, dispatches it with the
alert's final threshold already in the bead notes.

**17:30: wrapping up.** You ask the coordinator what's left. Two threads are in
review and one is idle waiting on a teammate. On the board you press `n` on two
ready beads to mark them `next` for tomorrow. One lesson from the day, that the
resolver needs alerts on demand as well as errors, goes into `bd remember`,
so every future worker that runs `bd prime` at start-up sees it.

Nothing about the day lived in your head or a scratch file: every thread's
state, question, PR and lesson is in beads, and the next morning starts with
the same `shepherd context`.

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
- **Sweep.** `shepherd sweep` lists linked worktrees whose PR merged, whose
  bead is closed, or that git reports prunable, across every repo; `--yes`
  removes only the ones that are clean, fully pushed and have no agent in them,
  and deletes a local branch only when its PR merged.
- **The coordinator** reads `shepherd context` each turn: an inbox of events,
  every active thread and the beads ready to start.

State lives in `~/.local/state/shepherd/` (`state.json`, `inbox/`, `outbox/`,
`ticker.log`).
