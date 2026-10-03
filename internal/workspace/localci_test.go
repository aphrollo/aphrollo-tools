package workspace

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// The CI choice for a merge: ci = auto | local | github, a repo setting a
// per-merge --ci flag beats. auto reads GitHub's checks and falls back to local
// CI only when they never started (#1064, #1081). Every merge prints which CI
// judged it and why.

// ciWorld stubs everything a merge reaches: the PR, the GitHub CI read, the
// pre-merge gate, the local CI run and the repo's declared mode, and counts
// each.
type ciWorld struct {
	ghReads, localRuns, gateRuns, merges int
	localHead, gateHead, mergeHead       string // the head each step was given
	localErr                             error
}

func newCIWorld(t *testing.T, declared string, gh CIStatus) *ciWorld {
	t.Helper()
	gateState(t)
	w := &ciWorld{}
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 5, URL: "u"}, nil },
		func(wt, branch, method, sha string) error { w.merges++; w.mergeHead = sha; return nil },
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubSync(t, func(string, bool, io.Writer, io.Writer) error { return nil })
	stubCI(t, func(wt, branch string) (CIStatus, error) { w.ghReads++; return gh, nil })
	oGate, oLocal, oRead := premergeGate, localCI, readCIMode
	premergeGate = func(_ *Target, head string, _ *tdd.CIVerdict, _ io.Writer) error {
		w.gateRuns++
		w.gateHead = head
		return nil
	}
	localCI = func(_ *Target, head string, _ io.Writer) (tdd.LocalCIVerdict, error) {
		w.localRuns++
		w.localHead = head
		return tdd.LocalCIVerdict{Tree: "t"}, w.localErr
	}
	readCIMode = func(string) (string, error) { return declared, nil }
	t.Cleanup(func() { premergeGate, localCI, readCIMode = oGate, oLocal, oRead })
	return w
}

// pinCIMode declares the repo's ci setting for a test that is about one mode.
func pinCIMode(t *testing.T, mode string) {
	t.Helper()
	prev := readCIMode
	readCIMode = func(string) (string, error) { return mode, nil }
	t.Cleanup(func() { readCIMode = prev })
}

func applyMerge(t *testing.T, flag string) (out string, err error) {
	t.Helper()
	m, perr := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	if perr != nil {
		t.Fatal(perr)
	}
	m.CI = flag
	var o, e bytes.Buffer
	err = m.Apply(&o, &e)
	return o.String(), err
}

func TestMergeCI_LocalNeverReadsGitHubAndNamesItsReason(t *testing.T) {
	w := newCIWorld(t, tdd.CIAuto, CIStatus{State: "red", Failing: 4})
	out, err := applyMerge(t, "local")
	if err != nil {
		t.Fatalf("local CI green must merge: %v", err)
	}
	if w.ghReads != 0 {
		t.Errorf("--ci local read GitHub's checks %d time(s)", w.ghReads)
	}
	if w.localRuns != 1 || w.merges != 1 {
		t.Errorf("local runs = %d, merges = %d, want 1 and 1", w.localRuns, w.merges)
	}
	if w.gateRuns != 1 {
		t.Errorf("the pre-merge gate ran %d time(s) beside local CI, want 1 as before: local CI replaces GitHub's checks, not the gate", w.gateRuns)
	}
	if !strings.Contains(out, "ci: local") || !strings.Contains(out, "--ci local") {
		t.Errorf("merge did not say which CI judged it and why:\n%s", out)
	}
}

func TestMergeCI_AutoFallsBackToLocalWhenGitHubJobsNeverStarted(t *testing.T) {
	w := newCIWorld(t, tdd.CIAuto, CIStatus{State: "unavailable", NotStarted: 4, SHA: "021f725aaaa"})
	out, err := applyMerge(t, "")
	if err != nil {
		t.Fatalf("auto must fall back to local CI on an outage: %v", err)
	}
	if w.localRuns != 1 || w.merges != 1 {
		t.Errorf("local runs = %d, merges = %d, want 1 and 1", w.localRuns, w.merges)
	}
	if !strings.Contains(out, "ci: local") || !strings.Contains(out, "not started") {
		t.Errorf("fallback not explained:\n%s", out)
	}
}

func TestMergeCI_AutoKeepsGitHubWhenItsJobsStarted(t *testing.T) {
	w := newCIWorld(t, tdd.CIAuto, CIStatus{State: "green", SHA: "021f725aaaa"})
	out, err := applyMerge(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if w.localRuns != 0 {
		t.Errorf("auto ran local CI %d time(s) although GitHub judged the head", w.localRuns)
	}
	if w.gateRuns != 1 {
		t.Errorf("the pre-merge gate ran %d time(s), want 1 as before", w.gateRuns)
	}
	if !strings.Contains(out, "ci: github") {
		t.Errorf("merge did not say GitHub judged it:\n%s", out)
	}
}

func TestMergeCI_AutoStillRefusesARealRedWithoutFallingBack(t *testing.T) {
	w := newCIWorld(t, tdd.CIAuto, CIStatus{State: "red", Failing: 2, SHA: "021f725aaaa"})
	_, err := applyMerge(t, "")
	if err == nil || !strings.Contains(err.Error(), "not green") {
		t.Fatalf("a red GitHub check must refuse, got: %v", err)
	}
	if w.localRuns != 0 || w.merges != 0 {
		t.Errorf("local runs = %d, merges = %d after a real red; a failing job is never talked around", w.localRuns, w.merges)
	}
}

func TestMergeCI_GithubModeRefusesAnOutageAndNeverRunsLocal(t *testing.T) {
	w := newCIWorld(t, tdd.CIAuto, CIStatus{State: "unavailable", NotStarted: 4, SHA: "021f725aaaa"})
	_, err := applyMerge(t, "github")
	if err == nil || !strings.Contains(err.Error(), "ci unavailable") {
		t.Fatalf("--ci github must keep today's refusal, got: %v", err)
	}
	if w.localRuns != 0 {
		t.Error("--ci github ran local CI")
	}
}

func TestMergeCI_ALocalRedRefusesTheMerge(t *testing.T) {
	w := newCIWorld(t, tdd.CILocal, CIStatus{})
	w.localErr = errors.New("gate premerge: suite red")
	_, err := applyMerge(t, "")
	if err == nil || !strings.Contains(err.Error(), "suite red") {
		t.Fatalf("a red local CI must refuse carrying its words, got: %v", err)
	}
	if w.merges != 0 {
		t.Error("merged on a red local CI")
	}
}

func TestMergeCI_TheRepoSettingAppliesAndTheFlagBeatsIt(t *testing.T) {
	w := newCIWorld(t, tdd.CILocal, CIStatus{State: "green", SHA: "021f725aaaa"})
	out, err := applyMerge(t, "")
	if err != nil || w.localRuns != 1 || !strings.Contains(out, "aphrollo.toml") {
		t.Fatalf("ci = local in the repo must run local CI and say where it came from: err=%v runs=%d\n%s", err, w.localRuns, out)
	}
	w = newCIWorld(t, tdd.CILocal, CIStatus{State: "green", SHA: "021f725aaaa"})
	if _, err := applyMerge(t, "github"); err != nil || w.localRuns != 0 || w.ghReads != 1 {
		t.Fatalf("--ci github must beat ci = local: err=%v local runs=%d gh reads=%d", err, w.localRuns, w.ghReads)
	}
}

func TestMergeCI_AnUnknownModeIsRefusedNotDefaulted(t *testing.T) {
	newCIWorld(t, tdd.CIAuto, CIStatus{State: "green"})
	if _, err := applyMerge(t, "selfhosted"); err == nil || !strings.Contains(err.Error(), "auto | local | github") {
		t.Fatalf("an unknown --ci value must be refused naming the modes, got: %v", err)
	}
}

func TestMergeWait_LocalModeDoesNotWaitOnGitHub(t *testing.T) {
	pr := &fakePR{number: 193, branch: "feat/tg", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{run("backend", newSHA, "in_progress", "")}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/tg": newSHA}}
	install(t, f)
	w := newCIWorld(t, tdd.CIAuto, CIStatus{State: "green"})
	var out, errb bytes.Buffer
	o := testWait
	o.CI = "local"
	if err := MergeWait(&Target{Worktree: "/w/tg", Branch: "feat/tg", MainRepo: "/r", RepoName: "r"}, "squash", true, o, &out, &errb); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if f.slept != 0 || strings.Contains(out.String(), "[wait]") {
		t.Errorf("--ci local waited on GitHub (slept %v):\n%s", f.slept, out.String())
	}
	if w.localRuns != 1 || w.merges != 1 {
		t.Errorf("local runs = %d, merges = %d, want 1 and 1", w.localRuns, w.merges)
	}
}

func TestMergeWait_AutoFallsBackToLocalWhenJobsNeverStarted(t *testing.T) {
	pr := &fakePR{number: 193, branch: "feat/tg", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{notStartedRun("backend", newSHA), notStartedRun("frontend", newSHA)}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/tg": newSHA}}
	install(t, f)
	// Apply re-reads the outage the wait just saw.
	w := newCIWorld(t, tdd.CIAuto, CIStatus{State: "unavailable", NotStarted: 2, SHA: newSHA})
	var out, errb bytes.Buffer
	if err := MergeWait(&Target{Worktree: "/w/tg", Branch: "feat/tg", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb); err != nil {
		t.Fatalf("an outage under auto must fall back, not refuse: %v\n%s", err, out.String())
	}
	if w.localRuns != 1 || w.merges != 1 {
		t.Errorf("local runs = %d, merges = %d, want 1 and 1", w.localRuns, w.merges)
	}
}

func TestMergeWait_GithubModeStillRefusesAnOutage(t *testing.T) {
	pr := &fakePR{number: 193, branch: "feat/tg", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{notStartedRun("backend", newSHA)}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/tg": newSHA}}
	install(t, f)
	w := newCIWorld(t, tdd.CIGithub, CIStatus{})
	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/tg", Branch: "feat/tg", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)
	if err == nil || !strings.Contains(err.Error(), "ci unavailable: jobs not started") {
		t.Fatalf("ci = github must keep refusing an outage, got: %v", err)
	}
	if w.localRuns != 0 {
		t.Error("github mode ran local CI")
	}
}

func TestMergeCI_TheRefusalCountsTheFailingChecksOnlyWhenThereAreSome(t *testing.T) {
	for _, c := range []struct {
		failing int
		want    string
	}{
		{2, "(red (2 failing))"},
		{1, "(red (1 failing))"},
		{0, "(red)"},
	} {
		newCIWorld(t, tdd.CIGithub, CIStatus{State: "red", Failing: c.failing, SHA: "021f725aaaa"})
		_, err := applyMerge(t, "")
		if err == nil || !strings.HasSuffix(err.Error(), c.want) {
			t.Errorf("failing=%d: refusal = %v, want it to end %q", c.failing, err, c.want)
		}
	}
}
