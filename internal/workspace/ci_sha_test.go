package workspace

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// stubChecksBySHA answers ghChecksAt per commit: a SHA absent from the map has
// no checks at all, exactly as GitHub answers a commit CI has not started on.
func stubChecksBySHA(t *testing.T, bySHA map[string][]CheckRun) (asked *[]string) {
	t.Helper()
	o := ghChecksAt
	var got []string
	ghChecksAt = func(dir, sha string) ([]CheckRun, error) {
		got = append(got, sha)
		return bySHA[sha], nil
	}
	t.Cleanup(func() { ghChecksAt = o })
	return &got
}

func greenRun(sha string) CheckRun {
	return CheckRun{Name: "build", SHA: sha, Status: "completed", Conclusion: "success"}
}

func TestGhCIStatus_EmptyNewSHAIsPendingNoRunWhileOldSHAIsGreen(t *testing.T) {
	old, fresh := "1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "2222222bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	asked := stubChecksBySHA(t, map[string][]CheckRun{old: {greenRun(old)}})

	got, err := ghCIStatus("/x", fresh)
	if err != nil {
		t.Fatalf("ghCIStatus: %v", err)
	}
	if got.State != "pending" || !got.NoRun || got.SHA != fresh {
		t.Fatalf("new head with no checks = %+v, want pending NoRun for %s", got, fresh)
	}
	if len(*asked) != 1 || (*asked)[0] != fresh {
		t.Fatalf("checks read for %v, want only the asked SHA %s", *asked, fresh)
	}
	if want := "pending (no run yet for 2222222)"; got.Word() != want {
		t.Errorf("Word() = %q, want %q", got.Word(), want)
	}

	if got, _ := ghCIStatus("/x", old); got.State != "green" || got.NoRun || got.Word() != "green" {
		t.Errorf("old head with a green run = %+v (%q), want green", got, got.Word())
	}
}

func TestGhCIStatus_IgnoresRunsOfAnotherCommit(t *testing.T) {
	old, fresh := "1111111aaaa", "2222222bbbb"
	// The endpoint answers a run whose head_sha is the old commit: not evidence
	// about the asked one.
	stubChecksBySHA(t, map[string][]CheckRun{fresh: {greenRun(old)}})
	got, _ := ghCIStatus("/x", fresh)
	if got.State != "pending" || !got.NoRun {
		t.Fatalf("a run of another commit counted: %+v", got)
	}
}

func TestGhCIStatus_ClassifiesRealStates(t *testing.T) {
	sha := "3333333cccc"
	cases := []struct {
		name string
		runs []CheckRun
		want CIStatus
	}{
		{"red beats pending", []CheckRun{
			{SHA: sha, Status: "completed", Conclusion: "failure"},
			{SHA: sha, Status: "completed", Conclusion: "timed_out"},
			{SHA: sha, Status: "in_progress"},
			greenRun(sha)}, CIStatus{State: "red", Failing: 2, SHA: sha, Cause: "other"}},
		{"running is pending", []CheckRun{{SHA: sha, Status: "queued"}, greenRun(sha)}, CIStatus{State: "pending", SHA: sha}},
		{"skipped and neutral pass", []CheckRun{
			{SHA: sha, Status: "completed", Conclusion: "skipped"},
			{SHA: sha, Status: "completed", Conclusion: "neutral"}}, CIStatus{State: "green", SHA: sha}},
		{"completed without conclusion is pending", []CheckRun{{SHA: sha, Status: "completed"}}, CIStatus{State: "pending", SHA: sha}},
	}
	for _, c := range cases {
		stubChecksBySHA(t, map[string][]CheckRun{sha: c.runs})
		got, err := ghCIStatus("/x", sha)
		if err != nil || got != c.want {
			t.Errorf("%s: got %+v, %v, want %+v", c.name, got, err, c.want)
		}
	}
}

func TestGhCIStatus_EmptySHAIsAnError(t *testing.T) {
	stubChecksBySHA(t, nil)
	if _, err := ghCIStatus("/x", ""); err == nil {
		t.Fatal("an unknown head must be an error, never a clear")
	}
}

// Right after a push GitHub still holds the previous head's green checks: the
// receipt must report the pushed commit's state, which has none yet.
func TestPush_ReceiptReportsPushedHeadNotThePreviousOne(t *testing.T) {
	repo := repoWithRemote(t)
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("checkout", "-q", "-b", "feat/y")
	writeFile(t, repo, "f.txt", "x\n")
	run("add", ".")
	run("commit", "-qm", "work")
	head := run("rev-parse", "HEAD")
	old := strings.Repeat("a", 40)

	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 9, URL: "u", State: "OPEN", HeadSHA: old}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubChecksBySHA(t, map[string][]CheckRun{old: {greenRun(old)}})

	p, err := PushPlan(targetFor(repo, "feat/y"), false)
	if err != nil {
		t.Fatalf("PushPlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := p.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if want := "ci pending (no run yet for " + head[:7] + ")\n"; !strings.Contains(out.String(), want) {
		t.Errorf("receipt lacks %q:\n%s", want, out.String())
	}
	if strings.Contains(out.String(), "ci green") {
		t.Errorf("receipt reports the previous head's green:\n%s", out.String())
	}
}

func TestSubmitReceiptTail_NoRunNamesTheCommit(t *testing.T) {
	var out bytes.Buffer
	s := &Submit{Target: targetFor("/x", "feat/z")}
	s.receiptTail(&out, &PRInfo{Number: 1, URL: "u"}, "0",
		CIStatus{State: "pending", NoRun: true, SHA: "4444444dddd"}, false)
	if want := "  ci pending (no run yet for 4444444) — review arms when green\n"; !strings.Contains(out.String(), want) {
		t.Errorf("receipt lacks %q:\n%s", want, out.String())
	}
}

func TestMerge_ReadsCIOfThePRHead(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 5, URL: "u", HeadSHA: "5555555eee"}, nil
		},
		func(wt, branch, method string) error {
			t.Fatal("merge must not run on pending CI")
			return nil
		},
		func(wt, branch string) (bool, error) { return false, nil },
	)
	var asked string
	stubCI(t, func(wt, sha string) (CIStatus, error) {
		asked = sha
		return CIStatus{State: "pending", NoRun: true, SHA: sha}, nil
	})
	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	var out, errb bytes.Buffer
	err := m.Apply(&out, &errb)
	if asked != "5555555eee" {
		t.Errorf("CI read for %q, want the PR head", asked)
	}
	if err == nil || !strings.Contains(err.Error(), "pending (no run yet for 5555555)") {
		t.Errorf("refusal = %v, want it to name the head without a run", err)
	}
}
