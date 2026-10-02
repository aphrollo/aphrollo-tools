package workspace

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// emitted is every record of the test's own events.jsonl, in file order.
func emitted(t *testing.T) []tdd.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tdd.StateDir(), "events.jsonl"))
	if err != nil {
		return nil
	}
	var out []tdd.Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e tdd.Event
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

func ofKind(evs []tdd.Event, kind string) []tdd.Event {
	var out []tdd.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func pushedLane(t *testing.T) (repo string) {
	t.Helper()
	repo = repoWithRemote(t)
	for _, args := range [][]string{{"checkout", "-q", "-b", "feat/y"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeFile(t, repo, "f.txt", "x\n")
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "work"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

// A push reads CI right after publishing, when the answer is nearly always
// pending. Only a settled state is an event, it names the commit and PR, and a
// re-push of the unchanged lane does not record it again.
func TestPushApply_RecordsASettledCIEventOncePerCommit(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := pushedLane(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 9, URL: "https://github.com/o/r/pull/9", State: "OPEN"}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubCI(t, func(wt, sha string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		p, _ := PushPlan(targetFor(repo, "feat/y"), false)
		if err := p.Apply(&bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}

	cis := ofKind(emitted(t), "ci")
	if len(cis) != 1 {
		t.Fatalf("ci events = %+v, want exactly one for two pushes of one commit", cis)
	}
	if cis[0].Verdict != "green" || cis[0].Detail["sha"] != strings.TrimSpace(string(head)) || cis[0].Detail["pr"] != "9" {
		t.Fatalf("ci event = %+v, want green, sha %s, pr 9", cis[0], head)
	}
}

func TestPushApply_PendingCIIsNotAnEvent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := pushedLane(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubCI(t, func(wt, sha string) (CIStatus, error) { return CIStatus{State: "pending", NoRun: true}, nil })

	p, _ := PushPlan(targetFor(repo, "feat/y"), false)
	if err := p.Apply(&bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if cis := ofKind(emitted(t), "ci"); len(cis) != 0 {
		t.Fatalf("ci events = %+v, want none for a pending read", cis)
	}
}

func TestMergeApply_RecordsTheMergeAndTheCIItRead(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 18, URL: "u", State: "OPEN", HeadSHA: "abc123"}, nil
		},
		func(wt, branch, method string) error { return nil },
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, sha string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
	stubSync(t, func(repoArg string, dry bool, stdout, stderr io.Writer) error { return nil })
	stubPremergeGate(t, func(tgt *Target, log io.Writer) error { return nil })

	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	if err := m.Apply(&bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	evs := emitted(t)
	merges := ofKind(evs, "merge")
	if len(merges) != 1 || merges[0].Detail["pr"] != "18" || merges[0].Detail["method"] != "squash" {
		t.Fatalf("merge events = %+v, want one for PR 18 by squash", merges)
	}
	cis := ofKind(evs, "ci")
	if len(cis) != 1 || cis[0].Detail["sha"] != "abc123" || cis[0].Detail["pr"] != "18" {
		t.Fatalf("ci events = %+v, want one for abc123 on PR 18", cis)
	}
}

func TestPRApply_RecordsAPROpenedEvent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := repoWithRemote(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "u", State: "OPEN", IsDraft: true}, nil
		},
	)
	pr, err := PRPlan(targetFor(repo, "main"), "", "My title", "body", true)
	if err != nil {
		t.Fatal(err)
	}

	if err := pr.Apply(&bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	opened := ofKind(emitted(t), "pr_opened")
	if len(opened) != 1 || opened[0].Detail["pr"] != "42" || opened[0].Detail["draft"] != "true" {
		t.Fatalf("pr_opened events = %+v, want one for PR 42, draft", opened)
	}
}

// merge --wait reads CI to its end on the PR's head: that settled green is the
// ci event, recorded once even though Merge.Apply reads the same commit again.
func TestMergeWait_RecordsTheSettledGreenOnceForTheHead(t *testing.T) {
	pr := &fakePR{number: 11, branch: "lane/a", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{run("go test", newSHA, "in_progress", "")}},
		{head: newSHA, checks: []CheckRun{run("go test", newSHA, "completed", "success")}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/a": newSHA}}
	install(t, f)

	err := MergeWait(&Target{Worktree: "/w/a", Branch: "lane/a", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("MergeWait: %v", err)
	}

	cis := ofKind(emitted(t), "ci")
	if len(cis) != 1 || cis[0].Verdict != "green" || cis[0].Detail["sha"] != newSHA || cis[0].Detail["pr"] != "11" {
		t.Fatalf("ci events = %+v, want one green for %s on PR 11", cis, newSHA)
	}
}

func TestMergeWait_RecordsARedCIEventWhenAFirstRunFails(t *testing.T) {
	pr := &fakePR{number: 13, branch: "lane/c", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{run("go test", newSHA, "completed", "failure")}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/c": newSHA}}
	install(t, f)

	err := MergeWait(&Target{Worktree: "/w/c", Branch: "lane/c", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("a failed check must refuse the merge")
	}

	cis := ofKind(emitted(t), "ci")
	if len(cis) != 1 || cis[0].Verdict != "red" || cis[0].Detail["sha"] != newSHA {
		t.Fatalf("ci events = %+v, want one red for %s", cis, newSHA)
	}
}

// A push before any PR exists still records the settled result, with no pr.
func TestPushApply_CIEventHasNoPRWhenNoneExists(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := pushedLane(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubCI(t, func(wt, sha string) (CIStatus, error) { return CIStatus{State: "red", Failing: 1}, nil })

	p, _ := PushPlan(targetFor(repo, "feat/y"), false)
	if err := p.Apply(&bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	cis := ofKind(emitted(t), "ci")
	if len(cis) != 1 || cis[0].Verdict != "red" {
		t.Fatalf("ci events = %+v, want one red", cis)
	}
	if got, ok := cis[0].Detail["pr"]; ok {
		t.Fatalf("pr = %q with no PR open", got)
	}
}
