package workspace

import (
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// recordSettledCI writes the one ci event a commit earns: its first settled
// result, green or red. A pending read says nothing and is never recorded, and
// a commit read again by a later verb (a re-push, merge after wait) is not
// counted twice, so "CI green on first run" is the first ci event of a lane's
// commits and nothing else. sha is the commit the checks belong to; pr is 0
// when no PR is known; cause says why a red was red (see ciCause) and is ""
// for a green.
func recordSettledCI(wt, sha string, pr int, state, cause string) {
	recordSettledCIBy(wt, sha, pr, state, tdd.CIGithub, cause)
}

// recordSettledCIBy is recordSettledCI with the CI that judged the commit
// (tdd.CIGithub or tdd.CILocal) carried in the event's "ci" field.
func recordSettledCIBy(wt, sha string, pr int, state, by, cause string) {
	if sha == "" || (state != "green" && state != "red") {
		return
	}
	detail := map[string]string{"sha": sha, "ci": by}
	if pr > 0 {
		detail["pr"] = strconv.Itoa(pr)
	}
	if cause != "" {
		detail["cause"] = cause
	}
	tdd.AppendEventOnce(tdd.Event{Kind: "ci", Root: wt, Verdict: state, Detail: detail}, "sha")
}

// ciCause says why a first CI run was red, from the names of the checks that
// failed: "test" when any test check failed, else "mutation" when a mutation
// check did, else "other". A check named for mutation counts as one even when
// "test" is also in its name.
func ciCause(failed []string) string {
	test, mutation := false, false
	for _, name := range failed {
		name = strings.ToLower(name)
		switch {
		case strings.Contains(name, "mutant"), strings.Contains(name, "mutation"):
			mutation = true
		case strings.Contains(name, "test"):
			test = true
		}
	}
	switch {
	case test:
		return "test"
	case mutation:
		return "mutation"
	}
	return "other"
}

// checkNames are the names of runs.
func checkNames(runs []CheckRun) []string {
	names := make([]string, len(runs))
	for i, r := range runs {
		names[i] = r.Name
	}
	return names
}

// ciQueue is the "ci" a ci event carries when the merge queue's own run of the
// PR judged it, apart from the PR's checks.
const ciQueue = "queue"

// recordQueueRed writes the red of a PR the merge queue removed for failed
// checks. The pass the PR's own checks gave it was recorded already; this is
// the red the queue's run added, which the first-run measure counts for the
// lane. Once per head, so a wait that is resumed does not count it twice.
func (m *Merge) recordQueueRed(q *Enqueued) {
	sha := "pr-" + strconv.Itoa(q.PR)
	if head, err := ghPRHead(m.Target.Worktree, strconv.Itoa(q.PR)); err == nil && head.HeadSHA != "" {
		sha = head.HeadSHA
	}
	recordSettledCIBy(m.Target.Worktree, ciQueue+":"+sha, q.PR, "red", ciQueue, ciQueue)
}
