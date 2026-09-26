package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGhAPIByPath writes a `gh` script in t.TempDir() that answers `api
// <path>` calls from an ordered list of (prefix, stdout, exit) rules, first
// match wins — an exact prefix, not the exact-string match fakeGhAPIScript
// uses, since these tests need to distinguish paths that share a prefix
// (e.g. "pulls" vs "pulls/7" vs "pulls/7/merge"). Anything unmatched exits 1
// with no output, so an unexpected call fails loudly.
type ghAPIRule struct {
	prefix string
	stdout string
	exit   int
}

func fakeGhAPIByPath(t *testing.T, rules []ghAPIRule) {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("all=\"$*\"\n")
	for _, r := range rules {
		fmt.Fprintf(&b, "case \"$all\" in *'%s'*)", r.prefix)
		if r.exit == 0 {
			fmt.Fprintf(&b, " printf '%%s' '%s'; exit 0 ;; esac\n", r.stdout)
		} else {
			fmt.Fprintf(&b, " printf '%%s' '%s' 1>&2; exit %d ;; esac\n", r.stdout, r.exit)
		}
	}
	b.WriteString("exit 1\n")
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestGhCreatePR_RealClosureCreatesViaREST calls ghCreatePR's real closure
// directly (every OTHER test in this package overrides the var, so nothing
// else in this package's own suite ever runs this body).
func TestGhCreatePR_RealClosureCreatesViaREST(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhAPIByPath(t, []ghAPIRule{
		{"repos/acme/widgets/pulls", `{"number":9,"html_url":"https://github.com/acme/widgets/pull/9","state":"open","draft":false,"merged":false,"mergeable":null,"mergeable_state":"unknown","head":{"ref":"feat/x","sha":"abc"}}`, 0},
	})
	info, err := ghCreatePR(repo, PRCreate{Base: "main", Branch: "feat/x", Title: "t", Body: "b"})
	if err != nil {
		t.Fatalf("ghCreatePR: %v", err)
	}
	if info.Number != 9 || info.State != "OPEN" {
		t.Fatalf("ghCreatePR = %+v, want number 9, state OPEN", info)
	}
}

// TestGhCreatePR_RealClosureFillsTitleFromCommitsWhenBlank proves the
// title=="" branch (no --title given) actually runs: the real closure must
// derive the title from fillTitleBody and send THAT to REST, not an empty
// title — checking the response alone can't tell the two apart (the fake
// answers the same JSON regardless of what was sent), so the fake here logs
// the exact argv it received and the test asserts the derived title is
// in it.
func TestGhCreatePR_RealClosureFillsTitleFromCommitsWhenBlank(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("checkout", "-q", "-b", "feat/x")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f.txt")
	run("commit", "-q", "-m", "the derived title")

	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv.log")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> '" + argvLog + "'\n" +
		"printf '%s' '{\"number\":9,\"html_url\":\"https://github.com/acme/widgets/pull/9\",\"state\":\"open\",\"head\":{\"ref\":\"feat/x\",\"sha\":\"abc\"}}'\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	info, err := ghCreatePR(repo, PRCreate{Base: "main", Branch: "feat/x"})
	if err != nil {
		t.Fatalf("ghCreatePR: %v", err)
	}
	if info.Number != 9 {
		t.Fatalf("ghCreatePR = %+v, want number 9", info)
	}
	logged, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "title=the derived title") {
		t.Fatalf("gh was never sent the derived title; argv log:\n%s", logged)
	}
}

// TestGhCreatePR_RealClosurePropagatesAFailedCreate proves a failed create
// surfaces as an error.
func TestGhCreatePR_RealClosurePropagatesAFailedCreate(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhAPIByPath(t, []ghAPIRule{
		{"repos/acme/widgets/pulls", "gh: validation failed", 1},
	})
	if _, err := ghCreatePR(repo, PRCreate{Base: "main", Branch: "feat/x", Title: "t", Body: "b"}); err == nil {
		t.Fatal("ghCreatePR = nil error, want one on a failed create")
	}
}

// TestGhMergePR_RealClosureMergesViaREST calls ghMergePR's real closure
// directly for the same reason as ghCreatePR above.
func TestGhMergePR_RealClosureMergesViaREST(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhAPIByPath(t, []ghAPIRule{
		{"repos/acme/widgets/pulls/12/merge", "", 0},
		{"repos/acme/widgets/pulls", "12", 0},
	})
	if err := ghMergePR(repo, "feat/x", "squash"); err != nil {
		t.Fatalf("ghMergePR: %v", err)
	}
}

// TestGhMergePR_RealClosureRefusesWhenNoPRFound proves the !found branch.
func TestGhMergePR_RealClosureRefusesWhenNoPRFound(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhAPIByPath(t, []ghAPIRule{
		{"repos/acme/widgets/pulls", "", 0},
	})
	if err := ghMergePR(repo, "feat/x", "squash"); err == nil || !strings.Contains(err.Error(), "no PR found") {
		t.Fatalf("ghMergePR = %v, want a no-PR-found error", err)
	}
}

// TestGhMergePR_RealClosurePropagatesAFailedMerge proves a failed PUT
// surfaces as an error.
func TestGhMergePR_RealClosurePropagatesAFailedMerge(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhAPIByPath(t, []ghAPIRule{
		{"repos/acme/widgets/pulls/12/merge", "gh: not mergeable", 1},
		{"repos/acme/widgets/pulls", "12", 0},
	})
	if err := ghMergePR(repo, "feat/x", "squash"); err == nil {
		t.Fatal("ghMergePR = nil, want an error on a failed merge")
	}
}

// TestGhPRHead_RealClosureResolvesByBranch calls ghPRHead's real closure
// directly, resolving by branch name (the non-numeric ref path).
func TestGhPRHead_RealClosureResolvesByBranch(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhAPIByPath(t, []ghAPIRule{
		{"repos/acme/widgets/pulls/3", `{"number":3,"html_url":"https://github.com/acme/widgets/pull/3","state":"open","head":{"ref":"feat/y","sha":"deadbeef"}}`, 0},
		{"repos/acme/widgets/pulls", "3", 0},
	})
	head, err := ghPRHead(repo, "feat/y")
	if err != nil {
		t.Fatalf("ghPRHead: %v", err)
	}
	if head.Number != 3 || head.HeadSHA != "deadbeef" || head.HeadRef != "feat/y" {
		t.Fatalf("ghPRHead = %+v, want number 3, sha deadbeef, ref feat/y", head)
	}
}

// TestGhPRHead_RealClosureResolvesByNumber proves the numeric-ref path
// (used when polling a PR opened from the main repo, not a lane worktree).
func TestGhPRHead_RealClosureResolvesByNumber(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhAPIByPath(t, []ghAPIRule{
		{"repos/acme/widgets/pulls/3", `{"number":3,"html_url":"https://github.com/acme/widgets/pull/3","state":"open","head":{"ref":"feat/y","sha":"deadbeef"}}`, 0},
	})
	head, err := ghPRHead(repo, "3")
	if err != nil {
		t.Fatalf("ghPRHead: %v", err)
	}
	if head.Number != 3 {
		t.Fatalf("ghPRHead(\"3\") = %+v, want number 3", head)
	}
}

// TestGhPRHead_RealClosureNoSuchPullRequest proves the absence path (nil,
// nil from ghAPIViewByRef) surfaces as an error here — unlike ghViewPRReal,
// ghPRHead has no legitimate "no PR yet" caller.
func TestGhPRHead_RealClosureNoSuchPullRequest(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhAPIByPath(t, []ghAPIRule{
		{"repos/acme/widgets/pulls", "", 0},
	})
	if _, err := ghPRHead(repo, "feat/y"); err == nil {
		t.Fatal("ghPRHead = nil error, want one for a branch with no PR")
	}
}

// TestGhViewPRStatusReal_OpenPRTalliesChecks calls ghViewPRStatusReal's
// found+OPEN path, which rebuilds the pass/fail/pending tally from
// ghChecksAt — the one branch the timeout/absence tests elsewhere in this
// file never reach.
func TestGhViewPRStatusReal_OpenPRTalliesChecks(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	checkRuns := `{"name":"build","head_sha":"cafe","status":"completed","conclusion":"success"}` + "\n" +
		`{"name":"lint","head_sha":"cafe","status":"in_progress","conclusion":""}`
	fakeGhAPIByPath(t, []ghAPIRule{
		{"repos/{owner}/{repo}/commits/cafe/check-runs", checkRuns, 0},
		{"repos/{owner}/{repo}/commits/cafe/status", `{"name":"ci","head_sha":"cafe","status":"completed","conclusion":"failure"}`, 0},
		{"repos/acme/widgets/pulls/5", `{"number":5,"state":"open","draft":false,"mergeable":true,"mergeable_state":"clean","head":{"ref":"feat/z","sha":"cafe"}}`, 0},
		{"repos/acme/widgets/pulls", "5", 0},
	})
	s, err := ghViewPRStatusReal(repo, "feat/z")
	if err != nil {
		t.Fatalf("ghViewPRStatusReal: %v", err)
	}
	if s.Number != 5 || s.State != "OPEN" || s.Mergeable != "MERGEABLE" || s.MergeStateStatus != "CLEAN" {
		t.Fatalf("ghViewPRStatusReal = %+v, want number 5 OPEN MERGEABLE CLEAN", s)
	}
	if s.Pass != 1 || s.Fail != 1 || s.Pending != 1 {
		t.Fatalf("ghViewPRStatusReal tally = pass=%d fail=%d pending=%d, want 1/1/1", s.Pass, s.Fail, s.Pending)
	}
}

// TestGhPRState_RealClosureFound calls ghPRState's real closure directly on
// a found PR — every other test in this package drives it through the
// stubPRState seam instead, so the "p != nil" branch of the real closure has
// no other test of its own.
func TestGhPRState_RealClosureFound(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhAPIByPath(t, []ghAPIRule{
		{"repos/acme/widgets/pulls/8", `{"number":8,"state":"closed","merged":true,"head":{"ref":"feat/w","sha":"beef"}}`, 0},
		{"repos/acme/widgets/pulls", "8", 0},
	})
	state, err := ghPRState(repo, "feat/w")
	if err != nil {
		t.Fatalf("ghPRState: %v", err)
	}
	if state != "MERGED" {
		t.Fatalf("ghPRState = %q, want MERGED", state)
	}
}

// TestGhReadyPRSandboxFallback_PropagatesAFindError proves the sandbox
// fallback's own error path (submit.go's `if err != nil` right after
// ghAPIFindPR) when the list-pulls call itself fails, rather than proceeding
// as if no PR mattered.
func TestGhReadyPRSandboxFallback_PropagatesAFindError(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	fakeGhAPIByPath(t, []ghAPIRule{
		{"repos/acme/widgets/pulls", "gh: authentication required", 1},
	})
	if err := ghReadyPRSandboxFallback(repo, "feat/x"); err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("ghReadyPRSandboxFallback = %v, want the find error propagated", err)
	}
}
