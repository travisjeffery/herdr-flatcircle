package main

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"time"
)

// Run is a GitHub Actions run a bead's notes point at: the deploy, canary or
// provision that follows a merge.
type Run struct {
	ID         int64  `json:"databaseId"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Workflow   string `json:"workflowName"`
	URL        string `json:"url"`
	Title      string `json:"displayTitle"`
	Repo       string `json:"repo"`
	// CreatedAt orders a failed run against a later one of its workflow.
	CreatedAt time.Time `json:"createdAt"`
}

const runFields = "databaseId,status,conclusion,workflowName,url,displayTitle,createdAt"

const followedRuns = 3

var runURL = regexp.MustCompile(`https://github\.com/([\w.-]+/[\w.-]+)/actions/runs/(\d+)`)

// NotedRuns returns the Actions runs a bead's notes mention, without any
// /job or /attempts suffix, ordered by latest mention and capped to the ones
// the ticker follows.
func NotedRuns(notes string) []string {
	var out []string
	for _, m := range runURL.FindAllString(notes, -1) {
		out = slices.DeleteFunc(out, func(u string) bool { return u == m })
		out = append(out, m)
	}
	return out[max(0, len(out)-followedRuns):]
}

func (g GH) RunView(repo, id string) (Run, error) {
	out, err := g.run("run", "view", id, "-R", repo, "--json", runFields)
	if err != nil {
		return Run{}, err
	}
	r := Run{Repo: repo}
	return r, json.Unmarshal(out, &r)
}

func (t Ticker) refreshRuns(st *TickerState, active []Bead) {
	type ref struct{ bead, url string }
	var refs []ref
	for _, b := range active {
		for _, u := range NotedRuns(b.Notes) {
			refs = append(refs, ref{b.ID, u})
		}
	}
	found := make([]*Run, len(refs))
	eachLimit(len(refs), ghParallel, func(i int) {
		id, u := refs[i].bead, refs[i].url
		m := runURL.FindStringSubmatch(u)
		r, err := t.gh.RunView(m[1], m[2])
		if err != nil {
			t.log.Printf("gh run view %s: %v", u, err)
			j := slices.IndexFunc(st.Runs[id], func(old Run) bool { return runKey(old) == m[2] })
			if j < 0 {
				return
			}
			r = st.Runs[id][j]
		}
		found[i] = &r
	})
	runs := map[string][]Run{}
	for i, r := range found {
		if r != nil {
			runs[refs[i].bead] = append(runs[refs[i].bead], *r)
		}
	}
	st.Runs = runs
}

func runKey(r Run) string   { return strconv.FormatInt(r.ID, 10) }
func runState(r Run) string { return r.Status + "/" + r.Conclusion }

func runStates(runs []Run) map[string]string {
	if len(runs) == 0 {
		return nil
	}
	m := map[string]string{}
	for _, r := range runs {
		m[runKey(r)] = runState(r)
	}
	return m
}

// finishedRuns are the runs that completed since the last look. A run seen for
// the first time is not a change, like a newly found PR.
func finishedRuns(runs []Run, prev map[string]string) []Run {
	var out []Run
	for _, r := range runs {
		was, seen := prev[runKey(r)]
		if seen && r.Status == "completed" && was != runState(r) {
			out = append(out, r)
		}
	}
	return out
}

func runFailed(r Run) bool {
	return r.Conclusion == "failure" || r.Conclusion == "cancelled" || r.Conclusion == "timed_out"
}
