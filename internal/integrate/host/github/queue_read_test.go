package github

import (
	"errors"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// How the adapter learns a branch has a merge queue and where a queued PR
// stands. The rules read is REST (it works where GraphQL is refused, so a repo
// with no queue merges as before); the entry read is GraphQL, the only place a
// queue's positions are published.

const (
	rulesWithQueue    = `[{"type":"merge_queue","parameters":{"merge_method":"SQUASH"}},{"type":"deletion"}]`
	rulesWithoutQueue = `[{"type":"pull_request"},{"type":"deletion"}]`
)

func rulesScript(t *testing.T, answer func(path string) ([]byte, error)) (*scripted, *int) {
	t.Helper()
	ResetQueueRules()
	t.Cleanup(ResetQueueRules)
	reads := 0
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		reads++
		return answer(args[len(args)-1])
	}
	return s, &reads
}

func TestHasMergeQueue_ReadsTheBranchsRulesAndReadsThemOncePerRun(t *testing.T) {
	s, reads := rulesScript(t, func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "/main") {
			return []byte(rulesWithQueue), nil
		}
		return []byte(rulesWithoutQueue), nil
	})
	g := s.host("git@github.com:acme/widgets.git")

	for i := 0; i < 3; i++ {
		got, err := g.HasMergeQueue("", "main")
		if err != nil || !got {
			t.Fatalf("main: queue = %v, err = %v; want true", got, err)
		}
	}
	if *reads != 1 {
		t.Errorf("rules read %d times for one branch in one run, want 1", *reads)
	}
	got, err := g.HasMergeQueue("", "release")
	if err != nil || got {
		t.Errorf("release: queue = %v, err = %v; want false (no merge_queue rule)", got, err)
	}
	if *reads != 2 {
		t.Errorf("a second branch must be read of its own: reads = %d, want 2", *reads)
	}
}

func TestHasMergeQueue_AReadFailureIsAnErrorAndIsNotCached(t *testing.T) {
	fail := true
	s, reads := rulesScript(t, func(string) ([]byte, error) {
		if fail {
			return []byte("HTTP 502"), errors.New("exit status 1")
		}
		return []byte(rulesWithoutQueue), nil
	})
	g := s.host("git@github.com:acme/gadgets.git")

	if got, err := g.HasMergeQueue("", "main"); err == nil {
		t.Fatalf("a failed read answered %v, want an error: no queue is a claim, not a default", got)
	}
	fail = false
	if got, err := g.HasMergeQueue("", "main"); err != nil || got {
		t.Fatalf("after the failure cleared: %v, %v; want false, nil", got, err)
	}
	if *reads != 2 {
		t.Errorf("reads = %d, want 2: a failure must not be remembered", *reads)
	}
}

func TestHasMergeQueue_ARepoWithNoGitHubOriginIsAnError(t *testing.T) {
	s, reads := rulesScript(t, func(string) ([]byte, error) { return nil, nil })
	g := s.host("/srv/git/widgets.git")

	if _, err := g.HasMergeQueue("", "main"); err == nil {
		t.Error("a repo with no GitHub origin answered; want the unreadable-remote error")
	}
	if *reads != 0 {
		t.Errorf("rules were read %d times for a repo with no GitHub origin", *reads)
	}
}

// A fork PR lives in its base repository: the rules are read there, not in origin.
func TestHasMergeQueue_ReadsTheBaseRepositoryNotOrigin(t *testing.T) {
	var read string
	s, _ := rulesScript(t, func(path string) ([]byte, error) {
		read = path
		return []byte(rulesWithQueue), nil
	})
	g := s.host("git@github.com:fork-owner/widgets.git")

	if got, err := g.HasMergeQueue("upstream/widgets", "main"); err != nil || !got {
		t.Fatalf("queue = %v, %v", got, err)
	}
	if want := "repos/upstream/widgets/rules/branches/main"; read != want {
		t.Errorf("rules read from %q, want the PR's base repository: %q", read, want)
	}
}

func TestRulesHaveMergeQueue_LooksForTheQueueRuleByTypeAcrossPages(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want bool
	}{
		"queue rule":         {rulesWithQueue, true},
		"no queue rule":      {rulesWithoutQueue, false},
		"empty rule list":    {`[]`, false},
		"slurped page two":   {`[[{"type":"deletion"}],[{"type":"merge_queue"}]]`, true},
		"back to back pages": {`[{"type":"deletion"}][{"type":"merge_queue"}]`, true},
		"back to back, none": {`[{"type":"deletion"}]` + "\n" + `[{"type":"x"}]`, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := RulesHaveMergeQueue([]byte(tc.in))
			if err != nil || got != tc.want {
				t.Errorf("RulesHaveMergeQueue = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	if _, err := RulesHaveMergeQueue([]byte(`not json`)); err == nil {
		t.Error("garbage parsed as rules")
	}
}

// A private repository of a Free organisation has no rulesets: GitHub answers
// the rules read with a 403 saying so (#1203). That is no queue, not a refusal;
// a 403 that is about the token still refuses with the fix.
func TestRulesReadResult_APlanThatLacksRulesetsIsNoQueueButATokenProblemIsNot(t *testing.T) {
	plan := "gh: Upgrade to GitHub Pro or make this repository public to enable this feature. (HTTP 403)"
	out, err := RulesReadResult("main", []byte(plan), errors.New("exit status 1"))
	if err != nil || string(out) != "[]" {
		t.Errorf("plan 403 = %q, %v; want an empty rule list", out, err)
	}
	for _, text := range []string{
		"gh: Resource not accessible by personal access token (HTTP 403)",
		"gh: Resource protected by organization SAML enforcement. (HTTP 403)",
		"gh: Forbidden (HTTP 403)",
		"gh: Bad credentials (HTTP 401)",
		"gh: Bad Gateway (HTTP 502)",
		"dial tcp: no such host",
	} {
		_, err := RulesReadResult("main", []byte(text), errors.New("exit status 1"))
		if err == nil || !strings.Contains(err.Error(), "gh auth status") {
			t.Errorf("%q: error = %v, want a refusal carrying the fix hint", text, err)
		}
	}
}

func TestEnqueueArgs_BindTheHeadAndTakeThePRByNumber(t *testing.T) {
	joined := strings.Join(EnqueueArgs("PR_node", "abc123"), " ")
	for _, want := range []string{"id=PR_node", "sha=abc123", "enqueuePullRequest", "expectedHeadOid:$sha"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv lacks %q: %s", want, joined)
		}
	}
}

func TestMergeBodyArgs_MatchTheJudgedHead(t *testing.T) {
	const head = "0123456789abcdef0123456789abcdef01234567"
	got := strings.Join(MergeBodyArgs("squash", "S (#1)", "B", head, "lane/x"), " ")
	want := "pr merge --squash --subject S (#1) --body B --match-head-commit " + head + " -- lane/x"
	if got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestParseQueueEntry_ReadsPositionTotalAndState(t *testing.T) {
	const queued = `{"data":{"repository":{"pullRequest":{"state":"OPEN",` +
		`"mergeQueueEntry":{"position":2,"state":"AWAITING_CHECKS"},` +
		`"mergeQueue":{"entries":{"totalCount":4}}}}}}`
	e, err := ParseQueueEntry([]byte(queued))
	if err != nil {
		t.Fatal(err)
	}
	if e == nil || e.Position != 2 || e.Total != 4 || e.State != "AWAITING_CHECKS" {
		t.Errorf("entry = %+v, want position 2 of 4, AWAITING_CHECKS", e)
	}

	const none = `{"data":{"repository":{"pullRequest":{"state":"MERGED","mergeQueueEntry":null,"mergeQueue":{"entries":{"totalCount":0}}}}}}`
	if e, err := ParseQueueEntry([]byte(none)); err != nil || e != nil {
		t.Errorf("a PR with no entry parsed as %+v, %v; want nil, nil", e, err)
	}
	if _, err := ParseQueueEntry([]byte(`{"errors":[{"message":"boom"}]}`)); err == nil {
		t.Error("an errors payload parsed as an answer")
	}
}

func TestParseQueueRemoval_OnlyTheNewestQueueEventCounts(t *testing.T) {
	const added = `{"__typename":"AddedToMergeQueueEvent"}`
	removed := func(reason string) string {
		return `{"__typename":"RemovedFromMergeQueueEvent","reason":"` + reason + `"}`
	}
	cases := map[string]struct {
		nodes string
		want  host.QueueRemoval
	}{
		"removed for failed checks":            {"[" + added + "," + removed("failed_checks") + "]", host.QueueRemoval{Removed: true, Reason: "failed_checks", FailedChecks: true}},
		"removed then enqueued again":          {"[" + removed("failed_checks") + "," + added + "]", host.QueueRemoval{FailedChecks: true}},
		"dropped, queued by hand, then merged": {"[" + removed("failed_checks") + "," + added + "," + removed("merged") + "]", host.QueueRemoval{Reason: "merged", FailedChecks: true}},
		"the queue's own merge":                {"[" + added + "," + removed("merged") + "]", host.QueueRemoval{Reason: "merged"}},
		"auto-merge switched off":              {`[{"__typename":"AutoMergeDisabledEvent"}]`, host.QueueRemoval{Removed: true, Reason: "auto-merge disabled"}},
		"nothing yet":                          {`[]`, host.QueueRemoval{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := `{"data":{"repository":{"pullRequest":{"autoMergeRequest":null,"timelineItems":{"nodes":` + tc.nodes + `}}}}}`
			got, err := ParseQueueRemoval([]byte(in))
			if err != nil || got != tc.want {
				t.Errorf("got %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
	on, _ := ParseQueueRemoval([]byte(`{"data":{"repository":{"pullRequest":{"autoMergeRequest":{},"timelineItems":{"nodes":[]}}}}}`))
	if !on.AutoMerge {
		t.Error("an autoMergeRequest was not read as auto-merge on")
	}
}

// A failed Actions job that ran no step is a runner that never started it, not
// a red: markNotStarted asks Actions for the steps of each failed Actions check
// run and flags the ones that ran none. Anything else is left as GitHub said.
func TestMarkNotStarted_FlagsFailedActionsJobsWithNoSteps(t *testing.T) {
	runs := []host.Check{
		{ID: 1, App: "github-actions", Status: "completed", Conclusion: "failure"},
		{ID: 2, App: "github-actions", Status: "completed", Conclusion: "failure"},
		{ID: 3, App: "github-actions", Status: "completed", Conclusion: "success"},
		{ID: 0, App: "", Status: "completed", Conclusion: "failure"}, // a commit status
		{ID: 5, App: "github-actions", Status: "completed", Conclusion: "failure"},
		{ID: 6, App: "github-actions", Status: "in_progress"},
	}
	var asked []string
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		asked = append(asked, args[1])
		switch args[1] {
		case "repos/{owner}/{repo}/actions/jobs/1":
			return []byte("0"), nil
		case "repos/{owner}/{repo}/actions/jobs/2":
			return []byte("3"), nil
		}
		return []byte("down"), errors.New("network down")
	}

	got := s.host(originURL).markNotStarted(runs)

	var flagged []int64
	for _, r := range got {
		if r.NotStarted {
			flagged = append(flagged, r.ID)
		}
	}
	if len(flagged) != 1 || flagged[0] != 1 {
		t.Errorf("flagged %v, want only job 1 (failed, zero steps)", flagged)
	}
	if len(asked) != 3 {
		t.Errorf("asked steps of %v, want jobs 1, 2 and 5 only (failed Actions runs)", asked)
	}
}

func TestJobSteps_ReadsTheCountGhPrintsAndNamesAFailure(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) { return []byte("3\n"), nil }
	if n, err := s.host(originURL).jobSteps(42); err != nil || n != 3 {
		t.Fatalf("jobSteps = %d, %v; want 3, nil", n, err)
	}

	s.reply = func([]string) ([]byte, error) { return []byte("not a number"), nil }
	if _, err := s.host(originURL).jobSteps(42); err == nil {
		t.Error("a non-numeric answer from gh must be an error, not a zero-step job")
	}

	s.reply = func([]string) ([]byte, error) { return []byte("no route"), errors.New("exit status 1") }
	if _, err := s.host(originURL).jobSteps(7); err == nil || !strings.Contains(err.Error(), "actions/jobs/7") {
		t.Fatalf("err = %v, want one naming actions/jobs/7", err)
	}
}
