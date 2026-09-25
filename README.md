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

## How it works

### The pieces

| Piece | What it is | What it owns |
|---|---|---|
| **Beads** (`bd`) | The issue tracker in your repo | All task state: status, notes, questions, lessons. The only record. |
| **Herdr** | The terminal the agents run in | Panes, agent names and states, worktrees, the sidebar, notifications |
| **Coordinator** | One agent named `shepherd`, in `~/.local/state/shepherd/coordinator` | Talking to you, planning, creating beads, dispatching. Never does the work. |
| **Workers** | One agent per bead, named after the bead, in the bead's worktree | Doing the work and keeping their bead current |
| **Ticker** | A background loop (`shepherd ticker`), started with the plugin | Watching threads, following PRs and runs, prompting workers, the inbox, the sidebar |

The coordinator and the ticker never talk to each other directly. The ticker
writes events to an **inbox** (files in `~/.local/state/shepherd/inbox/`) and
nudges the coordinator; the coordinator reads the inbox through
`shepherd context`. Everything a worker wants to say goes into its bead.

### The loop

```
  you ──── talk ────▶ coordinator ──── shepherd dispatch ────▶ worker (own worktree)
   ▲                    ▲     │                                  │  ▲
   │                    │     └── shepherd context ◀─┐           │  │ prompts, once idle 60s
   │ notifications      │ nudge                      │ inbox     │  │ (checks, reviews, merge, runs)
   │ + sidebar          │                            │           ▼  │
   └──────────────────── ticker ─────────────────────┴──── reads bd, herdr, gh
                         every 15s (PRs and runs every 60s)
```

1. **You ask the coordinator for something.** It runs `shepherd context`,
   creates beads for the work (`bd create`, with parents and dependencies), and
   proposes threads. It waits for your go-ahead.
2. **The coordinator dispatches.** `shepherd dispatch <bead>` claims the bead,
   creates a worktree on `<branch_prefix><bead>-<slug>`, starts Claude or Codex
   named after the bead, and sends the **brief**: the bead id and title, the
   worktree and branch, the Linear key if there is one, how to report, and
   your `instructions.md`. If the agent opens on a startup prompt (folder
   trust), the brief goes to the **outbox** and you're notified; the ticker
   delivers it once the agent is ready.
3. **The worker works and writes to its bead.** Findings and decisions go in
   `bd note`. It notes `PR: <url>` when it opens a PR and any Actions run URL
   it's waiting on. When it needs a decision, it writes the question and sets
   the bead to `needs_me`. When it needs you to run a command, it writes
   `RUN: <command>` and sets `needs_me`. After a merge with steps left, it adds
   the `rolling-out` label.
4. **The ticker watches.** Every 15 seconds it reads the active beads
   (`in_progress`, `needs_me`, `blocked`) and Herdr's agents, and joins them by
   name. Every 60 seconds it also asks GitHub about each bead's PR (found by
   branch, or by a PR link in the notes) and any Actions runs linked in the
   notes. It compares each thread with what it saw last time.
5. **The ticker acts on what changed:**
   - **The sidebar:** each worker's row gets its state line, like
     `review · PR #411 approved · ENG-1024`, and the agent view sorts what
     needs you to the top.
   - **The worker:** failing checks, new review feedback, a merge or a finished
     run become a prompt to that worker. Prompts wait until the worker has
     been idle for `idle_seconds`, so they never land in the middle of
     something, and several are sent together.
   - **The inbox:** each change is also an event file for the coordinator.
   - **You:** entering needs-you, a merge, a failed run or a human review
     notifies you.
   - **The coordinator:** when there are new events and the coordinator has
     been idle for `idle_seconds`, the ticker nudges it to run
     `shepherd context`.
6. **The coordinator catches up.** It reads the inbox and threads, relays
   questions and `RUN:` commands to you, passes your answers to the worker
   (`herdr agent prompt`), and marks the items handled with
   `shepherd inbox done`.
7. **The thread finishes.** After the merge the worker verifies and closes its
   bead with a reason. The ticker sees it leave the active set and writes a
   `closed` event, and the coordinator runs `shepherd resolve` to remove the
   worktree.

### Messages

Every automated message says it isn't from you, and none approves anything.

| From → to | When | Starts with |
|---|---|---|
| dispatch → worker | Once, at start | `You are the shepherd worker for bead …` |
| ticker → worker | Checks fail, review feedback, merge, run finished | `[shepherd: automated, not the user] PR #N …` |
| resume → worker | After `shepherd resume` | `[shepherd] You were resumed after your agent exited.` |
| ticker → coordinator | New inbox items and the coordinator is idle | `[shepherd ticker: automated, not the user, approves nothing] N new inbox item(s).` |
| worker → everyone | Any time | A note on its bead (`bd note`), or a status change |
| coordinator → worker | Relaying your answer | Whatever it writes with `herdr agent prompt` |

Inbox event kinds: `needs_you`, `checks_failing`, `new_review`, `merged`,
`run_succeeded`, `run_failed`, `finished` (a worker finished a turn),
`agent_gone` (its agent exited) and `closed`.

### Features in detail

- **Names.** Shepherd ties a worker to its bead by name: the bead id with
  dots as dashes (`backend-ab12.3` → `backend-ab12-3`). Dispatch and resume
  set it. If something else renames a worker, the ticker renames it back once
  a minute, when it's the only agent in an active bead's worktree.
- **Needs you.** A thread needs you when its bead is `needs_me` (the question
  is its latest note) or its agent is stopped at a permission or question
  prompt.
- **Commands only you can run** (a classifier denial, an interactive login, a
  production change) come back as a `RUN: <command>` note on a `needs_me`
  bead. The sidebar shows `needs you · run command`, the coordinator relays it
  ready to paste after `!`, and `p` in the board copies it. When you've run it
  the worker notes `RAN: <command> → <result>`. A `RUN:` only counts while it's
  one of the bead's last two notes, so an answered command is never offered
  again.
- **PRs** are found by branch name or by a PR link in the bead's notes, so a
  bead worked on a differently named branch is still followed.
- **Actions runs** linked in a bead's notes (the latest three) are followed.
  The worker is told when one succeeds and to continue, and when one fails
  you're notified and the worker is told to investigate. A running workflow
  shows on the thread's state line.
- **`rolling-out`.** A worker adds this label when steps remain after its PR
  merges. The thread then shows **rolling out** instead of **merged**, and the
  merge prompt says to carry on rather than close.
- **Bot reviews** (Codex, CodeRabbit and other GitHub Apps) are told apart from
  human ones. Both go to the worker; only a human review notifies you.
- **Resume.** Herdr restarts don't relaunch agents. `shepherd resume` restarts
  each claimed bead's agent in its worktree with `claude --continue` or
  `codex resume --last`, as whichever agent claimed it. `shepherd context`
  lists resumable threads, stale claims (in progress, no agent, worktree or
  PR, untouched 7 days) and other claims separately.
- **Several repos.** Beads labelled `repo:<name>` work in that repo from
  `repos`; everything else uses `repo`. Dispatch refuses a name that isn't
  configured.
- **Linear.** The bead's issue key comes from its title, or from a `Linear:`
  note (any note, if `linear_prefixes` is set). It goes in the brief and at
  the end of the sidebar row. `shepherd context` lists where each issue should
  be (In Review while its PR is open, Done once merged) and flags PR titles
  missing the key.
- **Choosing the agent.** `worker_agent = "auto"` picks Codex only when both
  agents' usage readings are under 6 hours old and Codex has more left;
  otherwise Claude.
- **Sweep** lists linked worktrees whose PR merged, whose bead is closed, or
  that git reports prunable. `--yes` removes only the ones that are clean,
  fully pushed, have no agent in them and whose bead isn't still active. It
  deletes a local branch only when its PR merged.
- **Reporting.** `shepherd report` prints beads closed in the window with
  their close reasons and PRs, your PRs merged without a bead, open PRs in
  flight, and `needs_me` beads with their latest note.
- **The sidebar sort** is a Herdr agent view owned by `plugin:shepherd`: the
  coordinator first, with a row summarising what it has to deal with
  (`coordinator · 2 need you · 1 review · 3 inbox`), then threads by state
  (needs you first), then agents that aren't on a bead. Herdr drops the view
  when the plugin is unlinked, uninstalled or disabled.

State lives in `~/.local/state/shepherd/`: `state.json` (the ticker's memory
of each thread), `inbox/` and `inbox/done/` (events), `outbox/` (briefs
waiting for an agent), `coordinator/` (its folder) and `ticker.log`.

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
Codex, `n` toggle `next`, `y` copy the id, `p` copy a thread's pending
`RUN:` command, `r` refresh, `q` quit.

## Getting started

Once it's installed (see below), the first ten minutes:

1. **Install and configure it** (below). `configure` points shepherd at your
   repo. Beads should already be set up there (`bd init`) with the `needs_me`
   status added.
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

## Requirements

Herdr 0.9.1+, [`bd`](https://github.com/steveyegge/beads) with a custom
`needs_me` status (`bd config set status.custom needs_me`), `gh` logged in, and
Claude Code and/or Codex. Linux or macOS, on amd64 or arm64. Go is only needed
to build from source.

## Install

```sh
herdr plugin install travisjeffery/herdr-shepherd --ref v0.2.0
```

Herdr downloads the prebuilt binary for your machine and checks it against the
release's checksums. Then, from a workspace in the repository you want
shepherd to work in:

```sh
herdr plugin action invoke configure --plugin shepherd
```

`configure` writes `~/.config/shepherd/config.toml` with that repository (or
pass `--repo <path>` when running `shepherd configure` directly), links
`~/.local/bin/shepherd`, and sets up the sidebar sort. It's safe to run again
and never rewrites an existing config.

To build from source instead: clone the repo, run `go build -o bin/shepherd .`,
then `herdr plugin link "$PWD"` and `bin/shepherd configure --repo <path>`.

Add the sidebar row and keys to `~/.config/herdr/config.toml`:

```toml
[ui.sidebar.agents]
rows = [["state_icon", "machine", "workspace", "tab"], ["agent"],
        [{ token = "$sh_state", rules = [{ starts_with = "needs you", fg = "#f38ba8", bold = true },
                                          { starts_with = "checks failing", fg = "#f38ba8" },
                                          { starts_with = "review", fg = "#f9e2af" },
                                          { starts_with = "rolling out", fg = "#f9e2af" },
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
worker_agent = "claude"   # "codex", or "auto": whichever has more quota left
coordinator_agent = "claude"
tick_seconds = 15         # how often the ticker checks threads
gh_seconds = 60           # how often it checks PRs
idle_seconds = 60         # it only types into an agent idle this long
nudge = true              # prompt the coordinator when there's news
auto_resolve = false      # remove a merged, closed bead's worktree automatically
repos = { infra = "~/src/infra", model = "~/src/model" }
linear_prefixes = ["ENG"] # Linear team keys to recognise; empty accepts any KEY-123
```

`repo` is the default repository. A bead whose work is in another one carries
the label `repo:<name>` for a name in `repos` (`bd label add <bead> repo:infra`);
its worktree, branch and PRs then live in that repository, and
`shepherd context` tags its thread `[infra]`. A `repo:` label naming no
configured repository falls back to `repo` and shows as a warning in
`shepherd context`.

`~/.config/shepherd/instructions.md` is added to every worker's brief.

## Commands

| Command | What it does |
|---|---|
| `shepherd coordinator [--agent K]` | Open (or focus) the coordinator agent. Also `prefix+shift+j`. |
| `shepherd dispatch <bead> [--agent K] [--focus]` | Claim a bead, create its worktree and branch, start a worker and brief it. |
| `shepherd focus [<bead>] [--agent K]` | Focus the bead's worker, or dispatch one. With no bead, reads the id from the clipboard. |
| `shepherd context` | The coordinator's digest: inbox, threads by state, Linear moves, `next` and ready beads. |
| `shepherd inbox done [<bead>...]` | Mark inbox items handled (all of them if no bead is given). |
| `shepherd resume [<bead>...] [--agent K]` | Restart exited workers in their worktrees, continuing their last conversation. |
| `shepherd stale [--days N] [--release]` | List claims with no agent, worktree or PR untouched N days (7); `--release` reopens them. |
| `shepherd resolve <bead> [--force]` | Remove a finished bead's worktree, and its branch if the PR merged. |
| `shepherd sweep [--yes]` | List finished worktrees across every repo; `--yes` removes only the safe ones. |
| `shepherd report [--since 24h\|7d\|DATE]` | Markdown summary of what shipped, what's in flight and what needs you. |
| `shepherd board` | The board popup (`prefix+j`). |
| `shepherd ticker run\|start\|stop\|status`, `shepherd tick` | The background loop, or one pass of it in the foreground. |
| `shepherd configure` / `unconfigure` | Install, or remove, the agent view and sidebar tokens. |

`K` is `claude`, `codex` or `auto`.
