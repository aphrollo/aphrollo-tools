package workspace

import (
	"errors"
	"strings"
	"testing"
)

// How the verb learns a branch has a merge queue and where a queued PR stands.
// The rules read is REST (it works where GraphQL is refused, so a repo with no
// queue merges as before); the entry read is GraphQL, the only place a queue's
// positions are published.

const (
	rulesWithQueue    = `[{"type":"merge_queue","parameters":{"merge_method":"SQUASH"}},{"type":"deletion"}]`
	rulesWithoutQueue = `[{"type":"pull_request"},{"type":"deletion"}]`
)

func stubBranchRules(t *testing.T, answer func(branch string) ([]byte, error)) *int {
	t.Helper()
	reads := 0
	prev := ghBranchRules
	ghBranchRules = func(_, _, _, branch string) ([]byte, error) { reads++; return answer(branch) }
	queueRulesCache.reset()
	t.Cleanup(func() { ghBranchRules = prev; queueRulesCache.reset() })
	return &reads
}

func TestHasMergeQueue_ReadsTheBranchsRulesAndReadsThemOncePerRun(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	reads := stubBranchRules(t, func(branch string) ([]byte, error) {
		if branch == "main" {
			return []byte(rulesWithQueue), nil
		}
		return []byte(rulesWithoutQueue), nil
	})

	for i := 0; i < 3; i++ {
		got, err := ghHasMergeQueueReal(repo, "", "main")
		if err != nil || !got {
			t.Fatalf("main: queue = %v, err = %v; want true", got, err)
		}
	}
	if *reads != 1 {
		t.Errorf("rules read %d times for one branch in one run, want 1", *reads)
	}
	got, err := ghHasMergeQueueReal(repo, "", "release")
	if err != nil || got {
		t.Errorf("release: queue = %v, err = %v; want false (no merge_queue rule)", got, err)
	}
	if *reads != 2 {
		t.Errorf("a second branch must be read of its own: reads = %d, want 2", *reads)
	}
}

func TestHasMergeQueue_AReadFailureIsAnErrorAndIsNotCached(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "gadgets")
	fail := true
	reads := stubBranchRules(t, func(string) ([]byte, error) {
		if fail {
			return nil, errors.New("HTTP 502")
		}
		return []byte(rulesWithoutQueue), nil
	})

	if got, err := ghHasMergeQueueReal(repo, "", "main"); err == nil {
		t.Fatalf("a failed read answered %v, want an error: no queue is a claim, not a default", got)
	}
	fail = false
	if got, err := ghHasMergeQueueReal(repo, "", "main"); err != nil || got {
		t.Fatalf("after the failure cleared: %v, %v; want false, nil", got, err)
	}
	if *reads != 2 {
		t.Errorf("reads = %d, want 2: a failure must not be remembered", *reads)
	}
}

func TestHasMergeQueue_ARepoWithNoGitHubOriginIsAnError(t *testing.T) {
	repo := initRepo(t)
	stubBranchRules(t, func(string) ([]byte, error) {
		t.Error("rules were read for a repo with no GitHub origin")
		return nil, nil
	})
	if _, err := ghHasMergeQueueReal(repo, "", "main"); err == nil {
		t.Error("a repo with no GitHub origin answered; want the unreadable-remote error")
	}
}

func TestRulesHaveMergeQueue_LooksForTheQueueRuleByType(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want bool
	}{
		"queue rule":      {rulesWithQueue, true},
		"no queue rule":   {rulesWithoutQueue, false},
		"empty rule list": {`[]`, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := rulesHaveMergeQueue([]byte(tc.in))
			if err != nil || got != tc.want {
				t.Errorf("rulesHaveMergeQueue = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	if _, err := rulesHaveMergeQueue([]byte(`not json`)); err == nil {
		t.Error("garbage parsed as rules")
	}
}

func TestEnqueueArgs_BindTheHeadAndTakeThePRByNumber(t *testing.T) {
	args := enqueueArgs("PR_node", "abc123")
	joined := strings.Join(args, " ")
	for _, want := range []string{"id=PR_node", "sha=abc123", "enqueuePullRequest", "expectedHeadOid:$sha"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv lacks %q: %v", want, args)
		}
	}
}

func TestParseQueueEntry_ReadsPositionTotalAndState(t *testing.T) {
	const queued = `{"data":{"repository":{"pullRequest":{"state":"OPEN",` +
		`"mergeQueueEntry":{"position":2,"state":"AWAITING_CHECKS"},` +
		`"mergeQueue":{"entries":{"totalCount":4}}}}}}`
	e, err := parseQueueEntry([]byte(queued))
	if err != nil {
		t.Fatal(err)
	}
	if e == nil || e.Position != 2 || e.Total != 4 || e.State != "AWAITING_CHECKS" {
		t.Errorf("entry = %+v, want position 2 of 4, AWAITING_CHECKS", e)
	}

	const none = `{"data":{"repository":{"pullRequest":{"state":"MERGED","mergeQueueEntry":null,"mergeQueue":{"entries":{"totalCount":0}}}}}}`
	if e, err := parseQueueEntry([]byte(none)); err != nil || e != nil {
		t.Errorf("a PR with no entry parsed as %+v, %v; want nil, nil", e, err)
	}
	if _, err := parseQueueEntry([]byte(`{"errors":[{"message":"boom"}]}`)); err == nil {
		t.Error("an errors payload parsed as an answer")
	}
}

func TestRulesHaveMergeQueue_ReadsEveryPageOfASlurpedRead(t *testing.T) {
	pages := `[[{"type":"deletion"}],[{"type":"merge_queue"}]]`
	got, err := rulesHaveMergeQueue([]byte(pages))
	if err != nil || !got {
		t.Errorf("a queue rule on page 2 was missed: %v, %v", got, err)
	}
}

// No rulesets on the host (404) is no queue; auth or server trouble is not an
// answer and refuses with the fix.
func TestRulesReadResult_A404IsNoQueueAndEverythingElseIsAnError(t *testing.T) {
	out, err := rulesReadResult("main", []byte("gh: Not Found (HTTP 404)"), errors.New("exit status 1"))
	if err != nil || string(out) != "[]" {
		t.Errorf("404 = %q, %v; want an empty rule list", out, err)
	}
	for _, text := range []string{"gh: Bad credentials (HTTP 401)", "gh: Bad Gateway (HTTP 502)", "dial tcp: no such host"} {
		_, err := rulesReadResult("main", []byte(text), errors.New("exit status 1"))
		if err == nil || !strings.Contains(err.Error(), "gh auth status") {
			t.Errorf("%q: error = %v, want a refusal carrying the fix hint", text, err)
		}
	}
}

// A fork PR lives in its base repository: the rules are read there, not in origin.
func TestHasMergeQueue_ReadsTheBaseRepositoryNotOrigin(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "fork-owner", "widgets")
	var read string
	prev := ghBranchRules
	ghBranchRules = func(_, owner, name, _ string) ([]byte, error) {
		read = owner + "/" + name
		return []byte(rulesWithQueue), nil
	}
	queueRulesCache.reset()
	t.Cleanup(func() { ghBranchRules = prev; queueRulesCache.reset() })

	if got, err := ghHasMergeQueueReal(repo, "upstream/widgets", "main"); err != nil || !got {
		t.Fatalf("queue = %v, %v", got, err)
	}
	if read != "upstream/widgets" {
		t.Errorf("rules read from %q, want the PR's base repository upstream/widgets", read)
	}
}

func TestParseQueueRemoval_OnlyTheNewestQueueEventCounts(t *testing.T) {
	cases := map[string]struct {
		nodes string
		want  QueueRemoval
	}{
		"removed for failed checks": {`[{"__typename":"AddedToMergeQueueEvent"},{"__typename":"RemovedFromMergeQueueEvent","reason":"failed_checks"}]`,
			QueueRemoval{Removed: true, Reason: "failed_checks"}},
		"removed then enqueued again": {`[{"__typename":"RemovedFromMergeQueueEvent","reason":"failed_checks"},{"__typename":"AddedToMergeQueueEvent"}]`, QueueRemoval{}},
		"the queue's own merge":       {`[{"__typename":"AddedToMergeQueueEvent"},{"__typename":"RemovedFromMergeQueueEvent","reason":"merged"}]`, QueueRemoval{Reason: "merged"}},
		"auto-merge switched off":     {`[{"__typename":"AutoMergeDisabledEvent"}]`, QueueRemoval{Removed: true, Reason: "auto-merge disabled"}},
		"nothing yet":                 {`[]`, QueueRemoval{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := `{"data":{"repository":{"pullRequest":{"autoMergeRequest":null,"timelineItems":{"nodes":` + tc.nodes + `}}}}}`
			got, err := parseQueueRemoval([]byte(in))
			if err != nil || got != tc.want {
				t.Errorf("got %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
	on, _ := parseQueueRemoval([]byte(`{"data":{"repository":{"pullRequest":{"autoMergeRequest":{},"timelineItems":{"nodes":[]}}}}}`))
	if !on.AutoMerge {
		t.Error("an autoMergeRequest was not read as auto-merge on")
	}
}
