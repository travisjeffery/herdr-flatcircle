package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type PR struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	Head           string    `json:"headRefName"`
	State          string    `json:"state"` // OPEN, MERGED, CLOSED
	IsDraft        bool      `json:"isDraft"`
	ReviewDecision string    `json:"reviewDecision"`
	HeadSHA        string    `json:"headRefOid"`
	MergeState     string    `json:"mergeStateStatus"`
	Checks         []Check   `json:"statusCheckRollup"`
	Reviews        []Review  `json:"reviews"`
	MergedAt       time.Time `json:"mergedAt"`
}

type Check struct {
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

type Review struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submittedAt"`
	// Bot is set from the REST API: gh pr view drops the [bot] suffix.
	Bot bool `json:"bot,omitempty"`
}

func (r Review) isBot() bool { return r.Bot || strings.HasSuffix(r.Author.Login, "[bot]") }

// listFields stay light: statusCheckRollup across 100 PRs in a repo with many
// checks per PR makes GitHub's GraphQL time out (504).
const listFields = "number,title,url,headRefName,state,isDraft,mergedAt"
const detailFields = listFields + ",reviewDecision,statusCheckRollup,reviews,headRefOid,mergeStateStatus"

type Checks struct {
	Failing []string
	Pending int
	Passing int
}

func (c Check) label() string {
	if c.Name != "" {
		return c.Name
	}
	return c.Context
}

func summarizeChecks(checks []Check) Checks {
	var s Checks
	seen := map[string]bool{}
	for _, c := range checks {
		result := strings.ToUpper(c.Conclusion)
		if result == "" {
			result = strings.ToUpper(c.State)
		}
		switch {
		case strings.ToUpper(c.Status) != "" && strings.ToUpper(c.Status) != "COMPLETED":
			s.Pending++
		case result == "PENDING" || result == "EXPECTED":
			s.Pending++
		case result == "FAILURE" || result == "ERROR" || result == "TIMED_OUT" || result == "STARTUP_FAILURE" || result == "ACTION_REQUIRED":
			if !seen[c.label()] {
				seen[c.label()] = true
				s.Failing = append(s.Failing, c.label())
			}
		default:
			s.Passing++
		}
	}
	sort.Strings(s.Failing)
	return s
}

// readyToMerge reports an open PR that only needs merging: approved or
// needing no review, and GitHub's merge state says branch protection is met.
// CLEAN and HAS_HOOKS mean every check passed; UNSTABLE means only checks
// that aren't required failed, since a required one failing or pending makes
// it BLOCKED. Pending checks still count against it, so a repo without
// required checks doesn't look ready the moment its PR opens.
func readyToMerge(pr *PR, c Checks) bool {
	if pr == nil || pr.State != "OPEN" || pr.IsDraft || c.Pending > 0 {
		return false
	}
	if pr.ReviewDecision != "APPROVED" && pr.ReviewDecision != "" {
		return false
	}
	switch pr.MergeState {
	case "CLEAN", "UNSTABLE", "HAS_HOOKS":
		return true
	}
	return false
}

// reviewsBy counts reviews not written by login, so a thread's own replies
// never look like new feedback.
func reviewsNotBy(reviews []Review, login string) int {
	n := 0
	for _, r := range reviews {
		if r.Author.Login != login && !r.isBot() {
			n++
		}
	}
	return n
}

func botReviews(reviews []Review) []string {
	var out []string
	for _, r := range reviews {
		if r.isBot() {
			out = append(out, r.Author.Login)
		}
	}
	return out
}

type GH struct {
	repo string
}

func (g GH) run(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = g.repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh %s: %v: %s", strings.Join(args[:min(2, len(args))], " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (g GH) Login() string {
	out, err := g.run("api", "user", "--jq", ".login")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Mine lists the user's recent PRs in the repo, newest first.
func (g GH) Mine() ([]PR, error) {
	out, err := g.run("pr", "list", "--author", "@me", "--state", "all", "--limit", "100", "--json", listFields)
	if err != nil {
		return nil, err
	}
	var prs []PR
	return prs, json.Unmarshal(out, &prs)
}

func (g GH) View(url string, detail bool) (PR, error) {
	fields := listFields
	if detail {
		fields = detailFields
	}
	out, err := g.run("pr", "view", url, "--json", fields)
	if err != nil {
		return PR{}, err
	}
	var pr PR
	return pr, json.Unmarshal(out, &pr)
}

// MarkBotReviews flags pr's bot reviews, which only the REST API marks.
func (g GH) MarkBotReviews(pr *PR) error {
	m := prURL.FindStringSubmatch(pr.URL)
	if len(pr.Reviews) == 0 || m == nil {
		return nil
	}
	out, err := g.run("api", "--paginate", fmt.Sprintf("repos/%s/pulls/%s/reviews", m[1], m[2]), "--jq", `.[] | select(.user.type == "Bot") | .user.login`)
	if err != nil {
		return err
	}
	bots := map[string]bool{}
	for _, l := range strings.Fields(string(out)) {
		bots[strings.TrimSuffix(l, "[bot]")] = true
	}
	for i := range pr.Reviews {
		pr.Reviews[i].Bot = bots[pr.Reviews[i].Author.Login]
	}
	return nil
}

// prForBead picks the PR a bead is being delivered through: one on the bead's
// own branch first, else the last PR its notes link to. A bead worked on a
// branch named for its Linear key (tj/eng-1102-…) is found through its notes.
func prForBead(b Bead, branchPrefix string, mine []PR, byURL map[string]PR) (PR, bool) {
	want := branchPrefix + agentName(b.ID)
	for _, pr := range mine {
		if pr.Head == want || strings.HasPrefix(pr.Head, want+"-") {
			return pr, true
		}
	}
	urls := NotedPRs(b.Notes)
	for i := len(urls) - 1; i >= 0; i-- {
		if pr, ok := byURL[urls[i]]; ok {
			return pr, true
		}
	}
	return PR{}, false
}
