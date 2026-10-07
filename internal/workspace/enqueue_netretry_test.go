package workspace

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A wait that follows a PR for an hour reads GitHub hundreds of times, and one
// read that fails on the network says nothing about the PR. The wait rides out a
// run of such failures, and when it gives up it says the PR's state is unknown,
// never that the PR did not merge.

const dialFailure = "gh api pr view 5: gh api repos/o/r/pulls/5: dial tcp 140.82.121.6:443: connectex: A connection attempt failed because the connected party did not properly respond after a period of time"

// failNumberedReads makes the next n reads of a PR by number fail with msg, and
// every later one answer as the scripted world does.
func failNumberedReads(t *testing.T, n int, msg string) *int {
	t.Helper()
	prev := ghPRHead
	t.Cleanup(func() { ghPRHead = prev })
	failed := 0
	ghPRHead = func(dir, ref string) (*PRHead, error) {
		if _, err := strconv.Atoi(ref); err == nil && failed < n {
			failed++
			return nil, errors.New(msg)
		}
		return prev(dir, ref)
	}
	return &failed
}

func TestMergeWait_QueueWaitRidesOutANetworkErrorOnAStatusRead(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, {Position: 1, Total: 1, State: "AWAITING_CHECKS"}}
	q.polls = []qPoll{{"OPEN", &QueueEntry{Position: 1, Total: 1, State: "AWAITING_CHECKS"}}, {"MERGED", nil}}
	failed := failNumberedReads(t, 2, dialFailure)
	stubSync(t, func(string, bool, io.Writer, io.Writer) error { return nil })

	var out, errb bytes.Buffer
	err := MergeWait(queueTarget(), "squash", true, queueWait, &out, &errb)

	if err != nil {
		t.Fatalf("a network error on a poll ended the wait: %v\n%s", err, out.String())
	}
	if *failed != 2 {
		t.Errorf("%d reads failed, want the 2 scripted", *failed)
	}
	if !strings.Contains(out.String(), "GitHub could not be reached") {
		t.Errorf("the retry is not said:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "merged PR #5 (merge queue)") {
		t.Errorf("the merge after the retries is not reported:\n%s", out.String())
	}
}

func TestMergeWait_QueueWaitGivesUpAfterFiveNetworkErrorsInARowAndSaysTheStateIsUnknown(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, {Position: 1, Total: 1, State: "QUEUED"}}
	q.polls = []qPoll{{"OPEN", &QueueEntry{Position: 1, Total: 1, State: "QUEUED"}}}
	failed := failNumberedReads(t, 1000, dialFailure)
	var slept []time.Duration
	prev := waitSleep
	t.Cleanup(func() { waitSleep = prev })
	waitSleep = func(d time.Duration) { slept = append(slept, d); prev(d) }

	var out, errb bytes.Buffer
	err := MergeWait(queueTarget(), "squash", true, queueWait, &out, &errb)

	if err == nil {
		t.Fatal("a PR GitHub cannot be asked about was reported merged")
	}
	if *failed != 5 {
		t.Errorf("%d reads before giving up, want 5", *failed)
	}
	for _, want := range []string{"is unknown", "5 times in a row", "dial tcp"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "did not merge") || strings.Contains(err.Error(), "removed from the merge queue") {
		t.Errorf("an unreadable PR is reported as not merged:\n%v", err)
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second}
	if len(slept) != len(want) {
		t.Fatalf("slept %v between the retries, want %v", slept, want)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Errorf("sleep %d = %v, want %v: the backoff doubles up to the poll interval", i, slept[i], want[i])
		}
	}
}

func TestMergeWait_QueueWaitCountsOnlyNetworkErrorsInARow(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, {Position: 1, Total: 1, State: "QUEUED"}}
	q.polls = []qPoll{{"OPEN", &QueueEntry{Position: 1, Total: 1, State: "QUEUED"}}, {"OPEN", &QueueEntry{Position: 1, Total: 1, State: "QUEUED"}}, {"MERGED", nil}}
	prev := ghPRHead
	t.Cleanup(func() { ghPRHead = prev })
	calls := 0
	ghPRHead = func(dir, ref string) (*PRHead, error) {
		if _, err := strconv.Atoi(ref); err == nil {
			calls++
			if calls%2 == 1 && calls < 10 { // every other read fails, so never 5 in a row
				return nil, errors.New(dialFailure)
			}
		}
		return prev(dir, ref)
	}
	stubSync(t, func(string, bool, io.Writer, io.Writer) error { return nil })

	var out, errb bytes.Buffer
	if err := MergeWait(queueTarget(), "squash", true, queueWait, &out, &errb); err != nil {
		t.Fatalf("scattered network errors ended the wait: %v\n%s", err, out.String())
	}
}

func TestMergeWait_QueueWaitDoesNotRetryAnAnswerGitHubGaveAndRefused(t *testing.T) {
	_, q := newQueueWorld(t, CIStatus{State: "green", SHA: "abc"})
	q.pre = []*QueueEntry{nil, {Position: 1, Total: 1, State: "QUEUED"}}
	q.polls = []qPoll{{"OPEN", nil}}
	failed := failNumberedReads(t, 1000, "gh api pr view 5: HTTP 404: Not Found (https://api.github.com/repos/o/r/pulls/5)")

	var out, errb bytes.Buffer
	err := MergeWait(queueTarget(), "squash", true, queueWait, &out, &errb)

	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v, want the 404 as it came", err)
	}
	if *failed != 1 {
		t.Errorf("%d reads, want 1: a refusal is not a network error", *failed)
	}
}

func TestIsTransientNetError_NamesTheNetworkFailuresAndNotTheAnswers(t *testing.T) {
	for msg, want := range map[string]bool{
		dialFailure: true,
		"read tcp 10.0.0.2:5555->140.82.121.6:443: wsarecv: An existing connection was forcibly closed by the remote host. connection reset": true,
		"Get \"https://api.github.com/x\": net/http: TLS handshake timeout":                                                                  true,
		"dial tcp: lookup api.github.com: no such host":                                                                                      true,
		"gh api pulls/5: i/o timeout": true,
		"gh api pulls/5: timed out after 1m0s — check network connectivity/credentials and retry":                             true,
		"gh api pulls/5: HTTP 502: Bad Gateway":                                                                               true,
		"gh api pulls/5: HTTP 500: Internal Server Error":                                                                     true,
		"gh api --paginate: exit status 1: unexpected end of JSON input":                                                      true,
		"gh api pulls/5: HTTP 429: Too Many Requests":                                                                         true,
		"gh api pulls/5: HTTP 403: You have exceeded a secondary rate limit. Please wait a few minutes before you try again.": true,
		"gh api pulls/5: HTTP 403: API rate limit exceeded for user ID 1.":                                                    true,
		"gh api pulls/5: HTTP 404: Not Found":                                                                                 false,
		"gh api pulls/5: HTTP 401: Bad credentials":                                                                           false,
		"gh api pulls/5: HTTP 403: Resource not accessible":                                                                   false,
		"no such pull request": false,
	} {
		if got := isTransientNetError(errors.New(msg)); got != want {
			t.Errorf("isTransientNetError(%q) = %v, want %v", msg, got, want)
		}
	}
	if isTransientNetError(nil) {
		t.Error("a nil error is not a network error")
	}
}

// The wait for a PR's checks reads GitHub just as often, and rides out the same
// failures.
func TestMergeWait_CheckWaitRidesOutANetworkErrorOnTheChecksRead(t *testing.T) {
	pr := &fakePR{number: 31, branch: "lane/net", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{run("go test", newSHA, "in_progress", "")}},
		{head: newSHA, checks: []CheckRun{run("go test", newSHA, "completed", "success")}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/net": newSHA}}
	install(t, f)
	prev := ghChecksAt
	t.Cleanup(func() { ghChecksAt = prev })
	failures := 0
	ghChecksAt = func(dir, sha string) ([]CheckRun, error) {
		if failures < 2 {
			failures++
			return nil, errors.New("gh api check-runs: " + dialFailure)
		}
		return prev(dir, sha)
	}

	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/net", Branch: "lane/net", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)

	if err != nil {
		t.Fatalf("a network error on the checks read ended the wait: %v\n%s", err, out.String())
	}
	if len(f.merged) != 1 {
		t.Errorf("merged %v, want lane/net once", f.merged)
	}
	if !strings.Contains(out.String(), "GitHub could not be reached (2 of 5 tries)") {
		t.Errorf("the second retry is not said:\n%s", out.String())
	}
}

// A rate limit says how long to wait when it can. The wait honours that, bounded
// so a hostile or mistaken header cannot park the verb, and counts it like any
// other failed read.
func TestNetRetry_HonoursARetryAfterWithinABound(t *testing.T) {
	for _, tc := range []struct {
		msg  string
		want time.Duration
	}{
		{"gh api pulls/5: HTTP 429: Too Many Requests (Retry-After: 45)", 45 * time.Second},
		{"gh api pulls/5: HTTP 403: secondary rate limit; retry after 90 seconds", 90 * time.Second},
		{"gh api pulls/5: HTTP 429: Too Many Requests (Retry-After: 99999)", 2 * time.Minute},
		{"gh api pulls/5: HTTP 429: Too Many Requests", 5 * time.Second},
		{"gh api pulls/5: HTTP 429: Retry-After: 1", 5 * time.Second},
	} {
		var r netRetry
		got, err := r.failed(errors.New(tc.msg), "PR #5", "5", 30*time.Second, io.Discard)
		if err != nil || got != tc.want {
			t.Errorf("%q: wait = %v, %v; want %v", tc.msg, got, err, tc.want)
		}
	}
}
