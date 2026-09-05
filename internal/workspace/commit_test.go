package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// targetFor builds a Target for a repo's main tree without going through cwd
// resolution, so commit/push tests stay independent of the process working dir.
func targetFor(repo, branch string) *Target {
	return &Target{Worktree: repo, Branch: branch, MainRepo: repo, RepoName: filepath.Base(repo)}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitHEADSubject(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "log", "-1", "--pretty=%s").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestCommit_StageAllAndApply(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "new.txt", "hello\n")

	c, err := CommitPlan(targetFor(repo, "main"), "add new.txt", true, false, "")
	if err != nil {
		t.Fatalf("CommitPlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if got := gitHEADSubject(t, repo); got != "add new.txt" {
		t.Errorf("HEAD subject = %q, want %q", got, "add new.txt")
	}
	if !strings.Contains(out.String(), "committed ") || !strings.Contains(out.String(), "add new.txt") {
		t.Errorf("Apply output missing precise feedback:\n%s", out.String())
	}
}

// The commit receipt is stateful: quoted subject, the branch + ahead-count
// relative to the default branch, the file/line delta, and the gate verdict, so
// the caller needs no follow-up git show/status.
func TestCommit_StatefulReceipt(t *testing.T) {
	repo := repoWithRemote(t) // main on origin so ahead-count resolves
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("checkout", "-q", "-b", "feat/z")
	writeFile(t, repo, "new.txt", "hello\n")

	c, err := CommitPlan(targetFor(repo, "feat/z"), "feat: add new", true, false, "")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, `committed `) || !strings.Contains(s, `"feat: add new"`) {
		t.Errorf("receipt missing quoted-subject committed line:\n%s", s)
	}
	if !strings.Contains(s, "branch feat/z") || !strings.Contains(s, "ahead of origin/main") {
		t.Errorf("receipt missing branch + ahead line:\n%s", s)
	}
	if !strings.Contains(s, "delta") {
		t.Errorf("receipt missing delta line:\n%s", s)
	}
	if !strings.Contains(s, "gate") {
		t.Errorf("receipt missing gate line:\n%s", s)
	}
}

// stubPrecommitRanSince swaps the precommitRanSince seam for the duration of
// a test and restores it after — Commit.Apply's OWN choice of receipt text is
// the behavior under test, not the real gate.log / pre-commit hook plumbing
// (covered separately in the tdd package).
func stubPrecommitRanSince(t *testing.T, ran bool) {
	t.Helper()
	orig := precommitRanSince
	precommitRanSince = func(root string, at time.Time) bool { return ran }
	t.Cleanup(func() { precommitRanSince = orig })
}

// A `git commit` that succeeds with no pre-commit hook wired (no
// core.hooksPath — a fresh clone, a broken profile, or this very test
// harness's own git isolation) must never claim "gate TDD pass": that is a
// claim of verification that never happened.
func TestCommit_ReportsGateNotRunWhenNoPrecommitHookFired(t *testing.T) {
	stubPrecommitRanSince(t, false)
	repo := initRepo(t)
	writeFile(t, repo, "new.txt", "hello\n")
	c, err := CommitPlan(targetFor(repo, "main"), "add new.txt", true, false, "")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	s := out.String()
	if strings.Contains(s, "gate TDD pass") {
		t.Errorf("no hook fired — must never claim TDD pass:\n%s", s)
	}
	if !strings.Contains(s, "gate not run") {
		t.Errorf("receipt must say the gate did not run:\n%s", s)
	}
}

// When the pre-commit hook DID fire (the marker is there), the receipt
// reports the verified pass.
func TestCommit_ReportsGateTDDPassWhenThePrecommitHookFired(t *testing.T) {
	stubPrecommitRanSince(t, true)
	repo := initRepo(t)
	writeFile(t, repo, "new.txt", "hello\n")
	c, err := CommitPlan(targetFor(repo, "main"), "add new.txt", true, false, "")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, "gate TDD pass") {
		t.Errorf("a confirmed hook run must report TDD pass:\n%s", s)
	}
}

// --no-verify must still say so, regardless of the seam — a deliberately
// skipped gate is not "not run", it is a stated bypass.
func TestCommit_NoVerifyStillReportsSkippedRegardlessOfTheSeam(t *testing.T) {
	stubPrecommitRanSince(t, true)
	repo := initRepo(t)
	writeFile(t, repo, "new.txt", "hello\n")
	c, err := CommitPlan(targetFor(repo, "main"), "add new.txt", true, true, "test bypass")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, "skipped (--no-verify)") {
		t.Errorf("--no-verify must report itself as skipped, not TDD pass:\n%s", s)
	}
}

// gate.log timestamps truncate to whole seconds (tdd's appendGateLog calls
// time.Now().UTC().Format(time.RFC3339)), so a REAL hook that finishes a
// fraction of a second after `started` was captured still logs a marker
// whose floored second equals `started`'s own integer second. Comparing the
// marker against the un-floored `started` reads that marker as "before
// started" and reports "gate not run" on a commit the gate actually
// verified — this test exercises the REAL precommitRanSince (unstubbed)
// against a REAL marker to prove the floor, not a mocked boolean.
func TestCommit_ReportsGateTDDPassWhenTheRealMarkerLandsInTheSameWallClockSecondAsStarted(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "new.txt", "hello\n")
	c, err := CommitPlan(targetFor(repo, "main"), "add new.txt", true, false, "")
	if err != nil {
		t.Fatal(err)
	}

	// Write the REAL marker now (real time.Now() inside AppendGateLog), then
	// pin Commit.Apply's `started` to a LATER fraction of THIS SAME second —
	// mirroring a hook that finished a few hundred ms after Apply captured
	// `started`, both still inside one wall-clock second.
	sec := time.Now().Truncate(time.Second)
	tdd.AppendGateLog("precommit", repo, "gate", "ran", 0)
	orig := timeNow
	timeNow = func() time.Time { return sec.Add(900 * time.Millisecond) }
	t.Cleanup(func() { timeNow = orig })

	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, "gate TDD pass") {
		t.Errorf("a marker logged within the same wall-clock second `started` was captured must still count as a verified run:\n%s", s)
	}
}

func TestCommit_CleanTreeIsNoOp(t *testing.T) {
	repo := initRepo(t)
	c, err := CommitPlan(targetFor(repo, "main"), "nothing", true, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Render(false), "nothing to commit") {
		t.Errorf("clean dry-run should say nothing to commit:\n%s", c.Render(false))
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply on clean tree should be a no-op, got: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to commit") {
		t.Errorf("Apply on clean tree should report no-op:\n%s", out.String())
	}
}

func TestCommit_EmptyMessageRejected(t *testing.T) {
	repo := initRepo(t)
	if _, err := CommitPlan(targetFor(repo, "main"), "   ", true, false, ""); err == nil {
		t.Fatal("expected an error for an empty commit message")
	}
}

func TestCommit_StagedOnly_SkipsUnstaged(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "staged.txt", "a\n")
	writeFile(t, repo, "loose.txt", "b\n")
	if out, err := exec.Command("git", "-C", repo, "add", "staged.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	c, err := CommitPlan(targetFor(repo, "main"), "only staged", false /*stageAll*/, false, "")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	// loose.txt must remain untracked after a --staged-only commit.
	st, _ := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if !strings.Contains(string(st), "?? loose.txt") {
		t.Errorf("loose.txt should still be untracked after --staged-only:\n%s", st)
	}
}

func TestCommit_StagedOnly_NothingStaged(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "loose.txt", "b\n") // present but never staged
	c, err := CommitPlan(targetFor(repo, "main"), "noop", false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(out.String(), "nothing staged") {
		t.Errorf("--staged-only with empty index should report nothing staged:\n%s", out.String())
	}
}

// --no-verify with no stated reason is an unexplained bypass gate stats has
// nothing to attribute it to (issue #314) -- CommitPlan refuses to build the
// plan at all, the same way an empty message never reaches Apply.
func TestCommitPlan_NoVerifyWithoutReasonRejected(t *testing.T) {
	repo := initRepo(t)
	if _, err := CommitPlan(targetFor(repo, "main"), "add new.txt", true, true, ""); err == nil {
		t.Fatal("expected an error for --no-verify with no --reason")
	}
	if _, err := CommitPlan(targetFor(repo, "main"), "add new.txt", true, true, "   "); err == nil {
		t.Fatal("expected an error for --no-verify with a blank --reason")
	}
}

// A --no-verify commit logs "override-no-verify" to gate.log carrying the
// stated reason, the same verdict token the git shim's own hooks-bypass door
// writes (issue #314) -- so a hatch that used to leave no trace anywhere now
// shows up in `gate stats` regardless of which of the two doors was used.
func TestCommit_NoVerifyLogsOverrideTokenWithReason(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := initRepo(t)
	writeFile(t, repo, "new.txt", "hello\n")
	c, err := CommitPlan(targetFor(repo, "main"), "add new.txt", true, true, "verifying a false-positive gate rejection")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := c.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	data, err := os.ReadFile(filepath.Join(tdd.StateDir(), "gate.log"))
	if err != nil {
		t.Fatalf("reading gate.log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "override-no-verify") {
		t.Fatalf("gate.log missing the override-no-verify token:\n%s", log)
	}
	if !strings.Contains(log, "verifying_a_false-positive_gate_rejection") {
		t.Fatalf("gate.log missing the stated reason:\n%s", log)
	}
}

// installFailingPreCommitHook plants a REAL pre-commit hook that refuses
// every commit with a distinctive message, so Apply's rejection path is
// exercised against git's own hook plumbing rather than a stubbed error.
func installFailingPreCommitHook(t *testing.T, repo string) {
	t.Helper()
	hooksDir := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho 'REJECTED by fake pre-commit hook' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A gate rejection must surface the failing stage's own stderr and recommend
// nothing further -- the old message pointed straight at --no-verify, so the
// documented remedy for a red gate was the bypass itself (issue #314).
func TestCommit_RejectionMessageDropsTheNoVerifyBypassHint(t *testing.T) {
	repo := initRepo(t)
	installFailingPreCommitHook(t, repo)
	writeFile(t, repo, "new.txt", "hello\n")
	c, err := CommitPlan(targetFor(repo, "main"), "add new.txt", true, false, "")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	err = c.Apply(&out, &errb)
	if err == nil {
		t.Fatal("expected the hook rejection to surface as an error")
	}
	if strings.Contains(err.Error(), "--no-verify") {
		t.Fatalf("rejection error must not recommend the bypass, got %q", err.Error())
	}
	if !strings.Contains(errb.String(), "REJECTED by fake pre-commit hook") {
		t.Fatalf("rejection must surface the hook's own stderr, got %q", errb.String())
	}
}
