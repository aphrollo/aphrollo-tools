package cli

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These use the REAL git binary against REAL temporary repositories, the
// same discipline git_shim_upstream_test.go uses for the merge-only wall:
// the check reads actual git plumbing (diff --diff-filter=D, merge-base),
// which a synthetic argv-echoing stub cannot produce truthfully.

// staleBranchRun is the shared runGitShim invocation these tests make: push
// a lane branch to origin from within dir, with the real git and a short
// wait budget (there is no lock contention to wait for in these tests).
func staleBranchRun(t *testing.T, realGit, dir string, args []string) (code int, stderr string) {
	t.Helper()
	t.Chdir(dir)
	var out, errb bytes.Buffer
	cfg := gitShimConfig{waitBudget: time.Second, pollInterval: 20 * time.Millisecond, realGit: realGit}
	code = runGitShim(args, strings.NewReader(""), &out, &errb, cfg)
	return code, errb.String()
}

// staleBranchOrigin builds a bare "origin" and a "seed" clone on main
// carrying one file, pushed. Callers grow trunk and/or the lane from here.
func staleBranchOrigin(t *testing.T) (realGit, origin, seed string) {
	t.Helper()
	isolateGitConfigCLI(t)
	withDirectGitShim(t)
	realGit = realGitForTest(t)
	run := func(dir string, args ...string) {
		cmd := exec.Command(realGit, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	origin = filepath.Join(t.TempDir(), "origin")
	run(t.TempDir(), "init", "-q", "--bare", "-b", "main", origin)

	seed = filepath.Join(t.TempDir(), "seed")
	run(t.TempDir(), "clone", "-q", origin, seed)
	run(seed, "config", "user.email", "t@example.com")
	run(seed, "config", "user.name", "t")
	mustWriteFile(t, filepath.Join(seed, "base.go"), "package base\n")
	run(seed, "add", "-A")
	run(seed, "commit", "-q", "-m", "init")
	run(seed, "push", "-q", "origin", "main")
	return realGit, origin, seed
}

// staleBranchLane clones origin into a lane checkout on lane/x, branched at
// whatever origin/main currently is.
func staleBranchLane(t *testing.T, realGit, origin string) string {
	t.Helper()
	lane := filepath.Join(t.TempDir(), "lane")
	run := func(dir string, args ...string) {
		cmd := exec.Command(realGit, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(t.TempDir(), "clone", "-q", origin, lane)
	run(lane, "config", "user.email", "t@example.com")
	run(lane, "config", "user.name", "t")
	run(lane, "checkout", "-q", "-b", "lane/x")
	return lane
}

func remoteHasBranch(t *testing.T, realGit, origin, branch string) bool {
	t.Helper()
	cmd := exec.Command(realGit, "-C", origin, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

// TestStaleBranchRefusalLine_CatchesALaneThatNeverSawTrunksLatestFile is
// PR #264's own shape: the lane branched before trunk gained a file, never
// merged trunk back in, and its diff against trunk's current tip reads as
// deleting that file -- evidence the lane is stale, not that it means to
// remove anything.
func TestStaleBranchRefusalLine_CatchesALaneThatNeverSawTrunksLatestFile(t *testing.T) {
	realGit, origin, seed := staleBranchOrigin(t)
	lane := staleBranchLane(t, realGit, origin)
	run := func(dir string, args ...string) {
		cmd := exec.Command(realGit, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// The lane makes its own, unrelated commit.
	mustWriteFile(t, filepath.Join(lane, "lane_feature.go"), "package base\n\nfunc Feature() {}\n")
	run(lane, "add", "-A")
	run(lane, "commit", "-q", "-m", "lane feature")

	// Trunk gains a file the lane branched before -- the box-wide lock,
	// standing in for #253.
	mustWriteFile(t, filepath.Join(seed, "locked.go"), "package base\n\nfunc Locked() {}\n")
	run(seed, "add", "-A")
	run(seed, "commit", "-q", "-m", "add the mutation-run lock")
	run(seed, "push", "-q", "origin", "main")

	// The lane learns about it (a fetch that happened for any reason, or a
	// CI runner's own checkout) without merging it in.
	run(lane, "fetch", "-q", "origin")

	code, errb := staleBranchRun(t, realGit, lane, []string{"push", "origin", "lane/x"})

	if code == 0 {
		t.Fatalf("push should have been refused, got exit 0\n%s", errb)
	}
	if !strings.Contains(errb, "locked.go") {
		t.Fatalf("refusal must name the stale path, got %q", errb)
	}
	if !strings.Contains(errb, "git fetch origin && git merge origin/main") {
		t.Fatalf("refusal must carry the remedy, got %q", errb)
	}
	if remoteHasBranch(t, realGit, origin, "lane/x") {
		t.Fatalf("lane/x reached origin -- the refusal must happen before git runs")
	}
}

// TestStaleBranchRefusalLine_AllowsALaneThatDeletesAFileItTouched proves the
// check never fights a genuine deletion: the lane's OWN commit removed the
// file, so it is in the lane's touched set and never flagged.
func TestStaleBranchRefusalLine_AllowsALaneThatDeletesAFileItTouched(t *testing.T) {
	realGit, origin, seed := staleBranchOrigin(t)
	run := func(dir string, args ...string) {
		cmd := exec.Command(realGit, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustWriteFile(t, filepath.Join(seed, "obsolete.go"), "package base\n")
	run(seed, "add", "-A")
	run(seed, "commit", "-q", "-m", "add obsolete.go")
	run(seed, "push", "-q", "origin", "main")

	lane := staleBranchLane(t, realGit, origin)
	run(lane, "rm", "-q", "obsolete.go")
	run(lane, "commit", "-q", "-m", "remove obsolete.go")

	code, errb := staleBranchRun(t, realGit, lane, []string{"push", "origin", "lane/x"})

	if code != 0 {
		t.Fatalf("a lane deleting a file it touched should not be refused, exit %d\n%s", code, errb)
	}
	if strings.Contains(errb, "deletes paths") {
		t.Fatalf("a genuine deletion must not be flagged as stale, got %q", errb)
	}
	if !remoteHasBranch(t, realGit, origin, "lane/x") {
		t.Fatalf("the push should have reached origin")
	}
}

// TestStaleBranchRefusalLine_PassesSilentlyWhenNothingIsStale covers the
// ordinary case: the lane only adds, trunk has not moved -- no deletions in
// either diff, nothing to say.
func TestStaleBranchRefusalLine_PassesSilentlyWhenNothingIsStale(t *testing.T) {
	realGit, origin, _ := staleBranchOrigin(t)
	lane := staleBranchLane(t, realGit, origin)
	run := func(dir string, args ...string) {
		cmd := exec.Command(realGit, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustWriteFile(t, filepath.Join(lane, "lane_feature.go"), "package base\n")
	run(lane, "add", "-A")
	run(lane, "commit", "-q", "-m", "lane feature")

	code, errb := staleBranchRun(t, realGit, lane, []string{"push", "origin", "lane/x"})

	if code != 0 {
		t.Fatalf("a clean lane push should not be refused, exit %d\n%s", code, errb)
	}
	if strings.Contains(errb, "gate: this push's diff against") {
		t.Fatalf("nothing is stale here, but the refusal fired: %q", errb)
	}
	if !remoteHasBranch(t, realGit, origin, "lane/x") {
		t.Fatalf("the push should have reached origin")
	}
}

// TestStaleBranchRefusalLine_FailsOpenWhenTrunkIsUnresolvable is the
// environmental case: no "origin" remote-HEAD and no branch named main or
// master, so trunk cannot be named -- the push must go through untouched.
func TestStaleBranchRefusalLine_FailsOpenWhenTrunkIsUnresolvable(t *testing.T) {
	isolateGitConfigCLI(t)
	withDirectGitShim(t)
	realGit := realGitForTest(t)
	run := func(dir string, args ...string) {
		cmd := exec.Command(realGit, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	dest := filepath.Join(t.TempDir(), "dest")
	run(t.TempDir(), "init", "-q", "--bare", "-b", "trunkless", dest)

	work := filepath.Join(t.TempDir(), "work")
	run(t.TempDir(), "init", "-q", "-b", "trunkless", work)
	run(work, "config", "user.email", "t@example.com")
	run(work, "config", "user.name", "t")
	mustWriteFile(t, filepath.Join(work, "a.go"), "package a\n")
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "init")
	run(work, "remote", "add", "dest", dest)

	code, errb := staleBranchRun(t, realGit, work, []string{"push", "dest", "trunkless"})

	if code != 0 {
		t.Fatalf("an unresolvable trunk must fail open, exit %d\n%s", code, errb)
	}
	if strings.Contains(errb, "gate: this push's diff against") {
		t.Fatalf("no trunk could be named, but the refusal fired anyway: %q", errb)
	}
}
