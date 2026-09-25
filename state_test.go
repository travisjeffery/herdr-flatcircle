package main

import (
	"flag"
	"slices"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func bead(status string) Bead {
	return Bead{ID: "backend-ab12", Title: "Fix the thing", Status: status}
}
func agent(status string, seq int64) *Agent {
	return &Agent{Name: "backend-ab12", Kind: "claude", Status: status, PaneID: "w1:p1", Seq: seq}
}
func openPR(failing ...string) *PR {
	pr := &PR{Number: 7, State: "OPEN", Head: "tj/backend-ab12-fix-the-thing"}
	for _, f := range failing {
		pr.Checks = append(pr.Checks, Check{Name: f, Status: "COMPLETED", Conclusion: "FAILURE"})
	}
	return pr
}
func thread(b Bead, a *Agent, pr *PR) Thread {
	t := Thread{Bead: b, Agent: a, PR: pr}
	if pr != nil {
		t.Checks = summarizeChecks(pr.Checks)
	}
	return t
}

func TestClassifyPrecedence(t *testing.T) {
	cases := []struct {
		name string
		th   Thread
		want Group
	}{
		{"needs_me beats a failing PR", thread(bead(StatusNeedsMe), agent("idle", 1), openPR("build")), GroupNeedsYou},
		{"blocked agent needs you", thread(bead(StatusInProgress), agent("blocked", 1), nil), GroupNeedsYou},
		{"merged beats review", thread(bead(StatusInProgress), agent("idle", 1), &PR{Number: 7, State: "MERGED"}), GroupMerged},
		{"failing checks while working", thread(bead(StatusInProgress), agent("working", 1), openPR("build")), GroupFailing},
		{"working with green PR", thread(bead(StatusInProgress), agent("working", 1), openPR()), GroupWorking},
		{"idle with open PR is review", thread(bead(StatusInProgress), agent("done", 1), openPR()), GroupReview},
		{"dependency blocked", thread(bead(StatusBlocked), nil, nil), GroupBlocked},
		{"idle without PR", thread(bead(StatusInProgress), agent("idle", 1), nil), GroupIdle},
		{"unknown agent state is idle, not missing", thread(bead(StatusInProgress), agent("unknown", 1), nil), GroupIdle},
		{"claimed but nobody on it", thread(bead(StatusInProgress), nil, nil), GroupNoAgent},
	}
	for _, c := range cases {
		if got := classify(c.th); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestStateLine(t *testing.T) {
	if got := stateLine(thread(bead(StatusInProgress), agent("working", 1), openPR("lint", "build"))); got != "checks failing · PR #7 (build, lint)" {
		t.Errorf("got %q", got)
	}
	pr := openPR()
	pr.ReviewDecision = "CHANGES_REQUESTED"
	if got := stateLine(thread(bead(StatusInProgress), agent("idle", 1), pr)); got != "review · PR #7 changes requested" {
		t.Errorf("got %q", got)
	}
}

func TestFirstSightIsNotAChange(t *testing.T) {
	o := transition(thread(bead(StatusNeedsMe), agent("idle", 1), openPR("build")), Snapshot{}, true, t0)
	if len(o.Events) != 0 || len(o.Prompts) != 0 || o.Notify {
		t.Fatalf("first sight produced %+v", o)
	}
}

func TestFailingChecksPromptOncePerSet(t *testing.T) {
	th := thread(bead(StatusInProgress), agent("idle", 1), openPR("build"))
	prev := Snapshot{Group: GroupReview, PRNumber: 7, PRState: "OPEN"}
	o := transition(th, prev, false, t0)
	if len(o.Prompts) != 1 || !strings.Contains(o.Prompts[0], "build") {
		t.Fatalf("want one prompt naming the check, got %+v", o.Prompts)
	}
	prev = snapshot(th, prev, t0)
	if o := transition(th, prev, false, t0.Add(time.Minute)); len(o.Prompts) != 0 {
		t.Fatalf("same failing set re-prompted: %+v", o.Prompts)
	}
	th2 := thread(bead(StatusInProgress), agent("idle", 1), openPR("build", "test"))
	if o := transition(th2, prev, false, t0.Add(2*time.Minute)); len(o.Prompts) != 1 {
		t.Fatalf("a new failure should prompt again: %+v", o.Prompts)
	}
}

func TestNewReviewPromptsOnlyOnSamePR(t *testing.T) {
	th := thread(bead(StatusInProgress), agent("idle", 1), openPR())
	th.Reviews = 2
	if o := transition(th, Snapshot{PRNumber: 0, Reviews: 0}, false, t0); len(o.Prompts) != 0 {
		t.Fatalf("reviews on a newly found PR are not new: %+v", o.Prompts)
	}
	if o := transition(th, Snapshot{PRNumber: 7, PRState: "OPEN", Reviews: 1, ReviewsSplit: true}, false, t0); len(o.Prompts) != 1 {
		t.Fatalf("want a review prompt, got %+v", o.Prompts)
	}
}

func TestMergedAsksWorkerToClose(t *testing.T) {
	th := thread(bead(StatusInProgress), agent("idle", 1), &PR{Number: 7, State: "MERGED"})
	o := transition(th, Snapshot{PRNumber: 7, PRState: "OPEN"}, false, t0)
	if !o.Notify || len(o.Prompts) != 1 || !strings.Contains(o.Prompts[0], "bd close backend-ab12") {
		t.Fatalf("got %+v", o)
	}
	if o := transition(th, Snapshot{PRNumber: 7, PRState: "MERGED"}, false, t0); len(o.Prompts) != 0 {
		t.Fatalf("merge re-announced: %+v", o)
	}
}

func TestNeedsYouNotifiesOnEntryOnly(t *testing.T) {
	th := thread(bead(StatusNeedsMe), agent("idle", 1), nil)
	if o := transition(th, Snapshot{Group: GroupWorking, AgentStatus: "working"}, false, t0); !o.Notify {
		t.Fatal("entering needs you should notify")
	}
	if o := transition(th, Snapshot{Group: GroupNeedsYou}, false, t0); o.Notify || len(o.Events) != 0 {
		t.Fatalf("staying in needs you re-notified: %+v", o)
	}
}

func TestFinishedEvenWhenTheTurnFitBetweenTicks(t *testing.T) {
	th := thread(bead(StatusInProgress), agent("done", 9), nil)
	o := transition(th, Snapshot{Group: GroupIdle, AgentStatus: "idle", AgentSeq: 5}, false, t0)
	if len(o.Events) != 1 || o.Events[0].Kind != EventFinished {
		t.Fatalf("want a finished event, got %+v", o.Events)
	}
	if o := transition(th, Snapshot{Group: GroupIdle, AgentStatus: "done", AgentSeq: 9}, false, t0); len(o.Events) != 0 {
		t.Fatalf("an untouched idle agent did not finish anything: %+v", o.Events)
	}
}

func TestReadySinceTracksUntouchedIdle(t *testing.T) {
	th := thread(bead(StatusInProgress), agent("idle", 5), nil)
	s1 := snapshot(th, Snapshot{AgentStatus: "working", AgentSeq: 4}, t0)
	if !s1.ReadySince.Equal(t0) {
		t.Fatalf("becoming idle starts the clock: %v", s1.ReadySince)
	}
	s2 := snapshot(th, s1, t0.Add(30*time.Second))
	if !s2.ReadySince.Equal(t0) {
		t.Fatalf("unchanged idle keeps the clock: %v", s2.ReadySince)
	}
	if deliverable(s2, t0.Add(30*time.Second), time.Minute) {
		t.Fatal("deliverable before the idle window")
	}
	if !deliverable(s2, t0.Add(61*time.Second), time.Minute) {
		t.Fatal("not deliverable after the idle window")
	}
	touched := thread(bead(StatusInProgress), agent("idle", 6), nil)
	if s3 := snapshot(touched, s2, t0.Add(90*time.Second)); !s3.ReadySince.Equal(t0.Add(90 * time.Second)) {
		t.Fatalf("a state change (user typing, new turn) restarts the clock: %v", s3.ReadySince)
	}
	if s4 := snapshot(thread(bead(StatusInProgress), agent("working", 7), nil), s2, t0); !s4.ReadySince.IsZero() {
		t.Fatal("a working agent is never ready")
	}
}

func TestBranchFor(t *testing.T) {
	cases := map[string]Bead{
		"tj/backend-ab12-fix-the-thing":           {ID: "backend-ab12", Title: "Fix the thing"},
		"tj/backend-qw12-3-raise-the-worker-pool": {ID: "backend-qw12.3", Title: "Raise the worker pool timeout on every node"},
		"tj/backend-zz99":                         {ID: "backend-zz99", Title: "!!!"},
	}
	for want, b := range cases {
		if got := branchFor("tj/", b); got != want {
			t.Errorf("branchFor(%q) = %q, want %q", b.Title, got, want)
		}
	}
}

func TestPRForBead(t *testing.T) {
	b := Bead{ID: "backend-ab12", Notes: "PR: https://github.com/acme/app/pull/1\nlater PR https://github.com/acme/infra/pull/2"}
	mine := []PR{{Number: 9, Head: "tj/backend-ab12x-other"}, {Number: 3, Head: "tj/backend-ab12-fix"}}
	if pr, ok := prForBead(b, "tj/", mine, nil); !ok || pr.Number != 3 {
		t.Fatalf("own branch should win and not match a longer id: %+v", pr)
	}
	byURL := map[string]PR{
		"https://github.com/acme/app/pull/1":   {Number: 1},
		"https://github.com/acme/infra/pull/2": {Number: 2},
	}
	if pr, ok := prForBead(b, "tj/", nil, byURL); !ok || pr.Number != 2 {
		t.Fatalf("latest noted PR should win: %+v", pr)
	}
}

func TestSummarizeChecks(t *testing.T) {
	s := summarizeChecks([]Check{
		{Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Name: "lint", Status: "COMPLETED", Conclusion: "FAILURE"},
		{Name: "lint", Status: "COMPLETED", Conclusion: "FAILURE"},
		{Name: "e2e", Status: "IN_PROGRESS"},
		{Context: "codecov", State: "ERROR"},
		{Name: "skip", Status: "COMPLETED", Conclusion: "SKIPPED"},
	})
	if !slices.Equal(s.Failing, []string{"codecov", "lint"}) || s.Pending != 1 || s.Passing != 2 {
		t.Fatalf("got %+v", s)
	}
}

func TestInterspersedFlags(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	kind := fs.String("agent", "", "")
	pos, err := interspersed(fs, []string{"backend-ab12", "--agent", "codex"})
	if err != nil || !slices.Equal(pos, []string{"backend-ab12"}) || *kind != "codex" {
		t.Fatalf("pos=%v kind=%q err=%v", pos, *kind, err)
	}
}

func TestParseOpened(t *testing.T) {
	o, err := parseOpened([]byte(`{"workspace":{"workspace_id":"w9","worktree":{"checkout_path":"/w/x"}},"root_pane":{"pane_id":"w9:p1"}}`))
	if err != nil || o.PaneID != "w9:p1" || o.Path != "/w/x" || o.WorkspaceID != "w9" {
		t.Fatalf("%+v %v", o, err)
	}
	if _, err := parseOpened([]byte(`{}`)); err == nil {
		t.Fatal("missing pane must be an error")
	}
}

func TestNotedRuns(t *testing.T) {
	notes := strings.Join([]string{
		"deploy: https://github.com/acme/app/actions/runs/1",
		"canary https://github.com/acme/app/actions/runs/2/job/77",
		"provision https://github.com/acme/infra/actions/runs/3/attempts/2",
		"rollout https://github.com/acme/app/actions/runs/4",
		"retried https://github.com/acme/app/actions/runs/2/attempts/2",
	}, "\n")
	want := []string{
		"https://github.com/acme/infra/actions/runs/3",
		"https://github.com/acme/app/actions/runs/4",
		"https://github.com/acme/app/actions/runs/2",
	}
	if got := NotedRuns(notes); !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := NotedRuns("PR: https://github.com/acme/app/pull/1"); len(got) != 0 {
		t.Fatalf("a PR link is not a run: %v", got)
	}
}

func deployRun(status, conclusion string) Run {
	return Run{ID: 42, Status: status, Conclusion: conclusion, Workflow: "Deploy", URL: "https://github.com/acme/app/actions/runs/42", Repo: "acme/app"}
}

func runThread(r Run) Thread {
	th := thread(bead(StatusInProgress), agent("idle", 1), &PR{Number: 7, State: "MERGED"})
	th.Runs = []Run{r}
	return th
}

func TestRunFirstSightIsNotAChange(t *testing.T) {
	th := runThread(deployRun("completed", "failure"))
	if o := transition(th, Snapshot{PRNumber: 7, PRState: "MERGED"}, false, t0); len(o.Events) != 0 || len(o.Prompts) != 0 || o.Notify {
		t.Fatalf("a newly noted run produced %+v", o)
	}
}

func TestRunSucceededPromptsNextStep(t *testing.T) {
	running := runThread(deployRun("in_progress", ""))
	prev := snapshot(running, Snapshot{PRNumber: 7, PRState: "MERGED"}, t0)
	done := runThread(deployRun("completed", "success"))
	o := transition(done, prev, false, t0)
	if len(o.Events) != 1 || o.Events[0].Kind != EventRunSucceeded || o.Notify {
		t.Fatalf("want one run_succeeded event and no notification, got %+v", o)
	}
	want := "[shepherd: automated, not the user] Deploy run https://github.com/acme/app/actions/runs/42 succeeded. Continue with the next step of the rollout."
	if len(o.Prompts) != 1 || o.Prompts[0] != want {
		t.Fatalf("got prompts %q", o.Prompts)
	}
	if o := transition(done, snapshot(done, prev, t0), false, t0.Add(time.Minute)); len(o.Events) != 0 || len(o.Prompts) != 0 {
		t.Fatalf("a finished run was re-announced: %+v", o)
	}
}

func TestRunFailedNotifiesAndPromptsInvestigation(t *testing.T) {
	for _, conclusion := range []string{"failure", "cancelled", "timed_out"} {
		prev := snapshot(runThread(deployRun("queued", "")), Snapshot{PRNumber: 7, PRState: "MERGED"}, t0)
		o := transition(runThread(deployRun("completed", conclusion)), prev, false, t0)
		if len(o.Events) != 1 || o.Events[0].Kind != EventRunFailed || !o.Notify {
			t.Fatalf("%s: want a notified run_failed event, got %+v", conclusion, o)
		}
		if len(o.Prompts) != 1 || !strings.Contains(o.Prompts[0], "`gh run view 42 -R acme/app --log-failed`") {
			t.Fatalf("%s: got prompts %q", conclusion, o.Prompts)
		}
	}
}

func TestStateLineShowsRunningWorkflow(t *testing.T) {
	th := runThread(deployRun("in_progress", ""))
	if got := stateLine(th); got != "merged · PR #7 · Deploy running" {
		t.Errorf("got %q", got)
	}
	if got := stateLine(runThread(deployRun("completed", "success"))); got != "merged · PR #7" {
		t.Errorf("a finished run is not running: %q", got)
	}
}

func TestRollingOutAfterMerge(t *testing.T) {
	b := bead(StatusInProgress)
	b.Labels = []string{LabelRollingOut}
	th := thread(b, agent("idle", 1), &PR{Number: 7, State: "MERGED"})
	if got := classify(th); got != GroupRollingOut {
		t.Fatalf("got %s, want %s", got, GroupRollingOut)
	}
	if got := classify(thread(b, agent("idle", 1), openPR())); got != GroupReview {
		t.Fatalf("an unmerged rolling-out bead is still in review, got %s", got)
	}
	if !(GroupReview < GroupRollingOut && GroupRollingOut < GroupFailing) {
		t.Fatal("rolling out sorts right after review")
	}
	o := transition(th, Snapshot{PRNumber: 7, PRState: "OPEN"}, false, t0)
	if len(o.Prompts) != 1 {
		t.Fatalf("want one merge prompt, got %q", o.Prompts)
	}
	p := o.Prompts[0]
	if !strings.Contains(p, "Continue the rollout") || !strings.Contains(p, "bd label remove backend-ab12 rolling-out") ||
		!strings.Contains(p, "bd close backend-ab12") || strings.Contains(p, "and stop") {
		t.Fatalf("merge prompt should continue the rollout, not close now: %q", p)
	}
}

func TestBotAndHumanReviews(t *testing.T) {
	prev := Snapshot{PRNumber: 7, PRState: "OPEN", Reviews: 1, BotReviews: 1, ReviewsSplit: true}
	bot := thread(bead(StatusInProgress), agent("idle", 1), openPR())
	bot.Reviews, bot.BotReviews = 1, []string{"chatgpt-codex-connector", "coderabbitai"}
	o := transition(bot, prev, false, t0)
	if o.Notify || len(o.Events) != 1 || o.Events[0].Summary != "PR #7 has a new review from coderabbitai" {
		t.Fatalf("a bot review is an unnotified event naming the bot, got %+v", o)
	}
	if len(o.Prompts) != 1 || !strings.Contains(o.Prompts[0], "new review from coderabbitai") {
		t.Fatalf("got prompts %q", o.Prompts)
	}
	human := thread(bead(StatusInProgress), agent("idle", 1), openPR())
	human.Reviews, human.BotReviews = 2, []string{"chatgpt-codex-connector"}
	o = transition(human, prev, false, t0)
	if !o.Notify || len(o.Events) != 1 || o.Events[0].Summary != "PR #7 has new human review feedback" {
		t.Fatalf("a human review notifies, got %+v", o)
	}
	if len(o.Prompts) != 1 || !strings.Contains(o.Prompts[0], "new human review feedback") {
		t.Fatalf("got prompts %q", o.Prompts)
	}
}

func TestBotReviewsSplitFromHumans(t *testing.T) {
	reviews := []Review{{State: "APPROVED"}, {State: "COMMENTED", Bot: true}, {State: "COMMENTED"}, {State: "COMMENTED"}}
	reviews[0].Author.Login = "alice"
	reviews[1].Author.Login = "chatgpt-codex-connector"
	reviews[2].Author.Login = "travisjeffery"
	reviews[3].Author.Login = "renovate[bot]"
	if n := reviewsNotBy(reviews, "travisjeffery"); n != 1 {
		t.Errorf("human reviews: got %d, want 1", n)
	}
	if got := botReviews(reviews); !slices.Equal(got, []string{"chatgpt-codex-connector", "renovate[bot]"}) {
		t.Errorf("bot reviews: got %v", got)
	}
}

func TestFirstPassAfterUpgradeOnlySetsReviewBaseline(t *testing.T) {
	// Before bots were told apart, 2 humans + 1 bot were saved as Reviews=3.
	// Now there are 3 humans and 1 bot: nothing is attributed on this pass.
	th := thread(bead(StatusInProgress), agent("idle", 1), openPR())
	th.Reviews, th.BotReviews = 3, []string{"codex-connector"}
	legacy := Snapshot{PRNumber: 7, PRState: "OPEN", Reviews: 3}
	if o := transition(th, legacy, false, t0); len(o.Prompts) != 0 || o.Notify {
		t.Fatalf("a legacy snapshot produced review prompts: %+v", o)
	}
	next := snapshot(th, legacy, t0)
	th.Reviews = 4
	if o := transition(th, next, false, t0); len(o.Prompts) != 1 || !o.Notify {
		t.Fatalf("a human review after the baseline should prompt and notify: %+v", o)
	}
}

func TestCoordinatorSortsFirstAndSummarises(t *testing.T) {
	for _, g := range []Group{GroupNeedsYou, GroupNoAgent} {
		if r := rank(g, "backend-ab12"); !(coordinatorRank < r) {
			t.Fatalf("coordinator rank %q must sort before thread rank %q", coordinatorRank, r)
		}
	}
	threads := []Thread{
		thread(bead(StatusNeedsMe), agent("idle", 1), nil),
		thread(bead(StatusNeedsMe), agent("idle", 1), nil),
		thread(bead(StatusInProgress), agent("working", 1), nil),
	}
	if got := coordinatorLine(threads, 3); got != "coordinator · 2 need you · 1 working · 3 inbox" {
		t.Fatalf("got %q", got)
	}
	if got := coordinatorLine(nil, 0); got != "coordinator" {
		t.Fatalf("got %q", got)
	}
}
