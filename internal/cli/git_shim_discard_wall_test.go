package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// The discard wall is the shim's third gate, after the primary-checkout wall
// and the stale-branch check: a git verb that would throw away uncommitted
// or unmerged work is refused unless what it would destroy is zero. These
// run against a REAL repo, exactly as git_shim_discard_test.go's cost tests
// do, since the wall's decision rests on real diff/ls-files/rev-list output.

// discardWallFixture builds a one-commit repo on main with the REAL git
// binary and chdirs the test into it, so runGitShim's own os.Getwd() (there
// is no `-C` in any of these invocations) resolves the fixture as its
// workDir. withDirectGitShim clears the "someone above me already holds the
// lock" passthrough the commit gate's own git children carry, matching every
// other direct-shim test in this package.
func discardWallFixture(t *testing.T) (repo string, cfg gitShimConfig) {
	t.Helper()
	withDirectGitShim(t)
	repo, realGit := newDiscardFixture(t)
	t.Chdir(repo)
	return repo, gitShimConfig{
		waitBudget:   time.Second,
		pollInterval: 20 * time.Millisecond,
		realGit:      realGit,
	}
}

// readGateLog reads the gate.log a test's own isolated CLAUDE_CONFIG_DIR
// (gateConfigDir) collected.
func readGateLog(t *testing.T, cfgDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cfgDir, "gate-state", "gate.log"))
	if err != nil {
		return ""
	}
	return string(data)
}

func TestGitShim_RefusesResetHardWithUncommittedWork(t *testing.T) {
	cfgDir := gateConfigDir(t)
	repo, cfg := discardWallFixture(t)

	writeFixtureFile(t, repo, "a.txt", distinctLines("a-orig", 20))
	writeFixtureFile(t, repo, "b.txt", distinctLines("b-orig", 10))
	writeFixtureFile(t, repo, "c.txt", distinctLines("c-orig", 10))
	runFixtureGit(t, cfg.realGit, repo, "add", ".")
	runFixtureGit(t, cfg.realGit, repo, "commit", "-qm", "tracked base")

	writeFixtureFile(t, repo, "a.txt", distinctLines("a-new", 100))
	writeFixtureFile(t, repo, "b.txt", distinctLines("b-new", 80))
	writeFixtureFile(t, repo, "c.txt", distinctLines("c-new", 32))

	var out, errb bytes.Buffer
	code := runGitShim([]string{"reset", "--hard"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstderr: %s", code, errb.String())
	}
	const want = "gate: refused — reset --hard discards 3 file(s), +212/-40 uncommitted" +
		"; aphrollo gate allow discard arms one command, APHROLLO_DISCARD=1 for scripts\n"
	if errb.String() != want {
		t.Fatalf("stderr = %q, want %q", errb.String(), want)
	}
	body, err := os.ReadFile(filepath.Join(repo, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "a-new-0") {
		t.Fatal("reset --hard must not have run: a.txt should still carry the un-reverted edit")
	}
	if log := readGateLog(t, cfgDir); !strings.Contains(log, "git-discard-refused:reset---hard") {
		t.Fatalf("gate.log = %q, want it to record the refusal", log)
	}
}

func TestGitShim_PassesResetHardOnACleanTree(t *testing.T) {
	gateConfigDir(t)
	_, cfg := discardWallFixture(t)

	var out, errb bytes.Buffer
	code := runGitShim([]string{"reset", "--hard"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if errb.String() != "" {
		t.Fatalf("stderr = %q, want empty (nothing to discard on a clean tree)", errb.String())
	}
}

func TestGitShim_RestoreMeasuresOnlyTheNamedPath(t *testing.T) {
	gateConfigDir(t)
	repo, cfg := discardWallFixture(t)
	writeFixtureFile(t, repo, "a.txt", []string{"a", "modified"})
	writeFixtureFile(t, repo, "b.txt", distinctLines("b-modified", 1))
	// seed.txt (from newDiscardFixture) has no matching entry in the repo's
	// tracked set here, so add these two as their own tracked base first.
	runFixtureGit(t, cfg.realGit, repo, "add", ".")
	runFixtureGit(t, cfg.realGit, repo, "commit", "-qm", "tracked base")
	writeFixtureFile(t, repo, "a.txt", []string{"a", "modified", "again"})
	writeFixtureFile(t, repo, "b.txt", distinctLines("b-modified-again", 2))

	var out, errb bytes.Buffer
	code := runGitShim([]string{"restore", "b.txt"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 1 {
		t.Fatalf("restore b.txt: exit = %d, want 1\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "discards 1 file(s)") {
		t.Fatalf("restore b.txt refusal = %q, want it to name exactly 1 file", errb.String())
	}
	if strings.Contains(errb.String(), "a.txt") {
		t.Fatalf("restore b.txt refusal = %q, must not measure a.txt too", errb.String())
	}

	var out2, errb2 bytes.Buffer
	code2 := runGitShim([]string{"restore", "--staged", "b.txt"}, strings.NewReader(""), &out2, &errb2, cfg)
	if code2 != 0 {
		t.Fatalf("restore --staged b.txt: exit = %d, want 0\nstderr: %s", code2, errb2.String())
	}
	if strings.Contains(errb2.String(), "gate: refused") {
		t.Fatalf("restore --staged alone must not be refused, got %q", errb2.String())
	}
}

func TestGitShim_CleanForceRefusedDryRunPasses(t *testing.T) {
	gateConfigDir(t)
	repo, cfg := discardWallFixture(t)
	writeFixtureFile(t, repo, "u1.txt", []string{"untracked"})
	writeFixtureFile(t, repo, "u2.txt", []string{"untracked"})

	var out, errb bytes.Buffer
	code := runGitShim([]string{"clean", "-fd"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 1 {
		t.Fatalf("clean -fd: exit = %d, want 1\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "2 untracked file(s)") {
		t.Fatalf("clean -fd refusal = %q, want it to name 2 untracked files", errb.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "u1.txt")); err != nil {
		t.Fatal("clean -fd must not have run: u1.txt should still exist")
	}

	var out2, errb2 bytes.Buffer
	code2 := runGitShim([]string{"clean", "-n"}, strings.NewReader(""), &out2, &errb2, cfg)
	if code2 != 0 {
		t.Fatalf("clean -n: exit = %d, want 0\nstderr: %s", code2, errb2.String())
	}
	if strings.Contains(errb2.String(), "gate: refused") {
		t.Fatalf("clean -n (a dry run) must not be refused, got %q", errb2.String())
	}
}

func TestGitShim_BranchDeleteForceRefusedWhenUnmerged(t *testing.T) {
	gateConfigDir(t)
	repo, cfg := discardWallFixture(t)
	runFixtureGit(t, cfg.realGit, repo, "branch", "x")
	runFixtureGit(t, cfg.realGit, repo, "checkout", "-q", "x")
	writeFixtureFile(t, repo, "x1.txt", []string{"one"})
	runFixtureGit(t, cfg.realGit, repo, "add", ".")
	runFixtureGit(t, cfg.realGit, repo, "commit", "-qm", "x commit 1")
	writeFixtureFile(t, repo, "x2.txt", []string{"two"})
	runFixtureGit(t, cfg.realGit, repo, "add", ".")
	runFixtureGit(t, cfg.realGit, repo, "commit", "-qm", "x commit 2")
	runFixtureGit(t, cfg.realGit, repo, "checkout", "-q", "main")

	var out, errb bytes.Buffer
	code := runGitShim([]string{"branch", "-D", "x"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 1 {
		t.Fatalf("branch -D x while unmerged: exit = %d, want 1\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "2 commit(s) unreachable from HEAD or any remote") {
		t.Fatalf("branch -D x refusal = %q, want it to name 2 commits unreachable from HEAD or any remote", errb.String())
	}

	runFixtureGit(t, cfg.realGit, repo, "merge", "-q", "x")

	var out2, errb2 bytes.Buffer
	code2 := runGitShim([]string{"branch", "-D", "x"}, strings.NewReader(""), &out2, &errb2, cfg)
	if code2 != 0 {
		t.Fatalf("branch -D x once merged: exit = %d, want 0\nstderr: %s", code2, errb2.String())
	}
}

// TestGitShim_BranchDeleteAllowedOnceMergedEvenWithStaleLocalHead is the
// end-to-end shape of issue #499: the lane's PR has merged its commits onto
// origin/main, but local main was never fast-forwarded, and `branch -D
// lane` must be allowed to run for real, not merely measured as zero.
func TestGitShim_BranchDeleteAllowedOnceMergedEvenWithStaleLocalHead(t *testing.T) {
	gateConfigDir(t)
	repo, cfg := discardWallFixture(t)
	originDir := t.TempDir()
	runFixtureGit(t, cfg.realGit, originDir, "init", "-q", "--bare", "-b", "main")
	runFixtureGit(t, cfg.realGit, repo, "remote", "add", "origin", originDir)
	runFixtureGit(t, cfg.realGit, repo, "push", "-q", "origin", "main")

	runFixtureGit(t, cfg.realGit, repo, "branch", "lane")
	runFixtureGit(t, cfg.realGit, repo, "checkout", "-q", "lane")
	writeFixtureFile(t, repo, "lane1.txt", []string{"one"})
	runFixtureGit(t, cfg.realGit, repo, "add", ".")
	runFixtureGit(t, cfg.realGit, repo, "commit", "-qm", "lane commit 1")
	writeFixtureFile(t, repo, "lane2.txt", []string{"two"})
	runFixtureGit(t, cfg.realGit, repo, "add", ".")
	runFixtureGit(t, cfg.realGit, repo, "commit", "-qm", "lane commit 2")

	// The PR merges upstream by landing the lane's commits on origin/main
	// directly, the way a GitHub merge does -- local main never
	// fast-forwards.
	runFixtureGit(t, cfg.realGit, repo, "push", "-q", "origin", "lane:main")
	runFixtureGit(t, cfg.realGit, repo, "checkout", "-q", "main")
	runFixtureGit(t, cfg.realGit, repo, "fetch", "-q", "origin")

	var out, errb bytes.Buffer
	code := runGitShim([]string{"branch", "-D", "lane"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("branch -D lane once its commits are on origin/main, local HEAD stale: exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if strings.Contains(errb.String(), "gate: refused") {
		t.Fatalf("branch -D lane refusal = %q, want none: the commits are on origin/main", errb.String())
	}
}

// TestGitShim_BranchDeleteMissingBranchLetsGitsOwnErrorThrough pins the
// second defect from issue #499: a branch that does not exist must not print
// the discard wall's own "could not measure...retry" wrapper -- there is
// nothing to retry into existing -- and must instead run the real git
// command, which refuses with its own message.
func TestGitShim_BranchDeleteMissingBranchLetsGitsOwnErrorThrough(t *testing.T) {
	gateConfigDir(t)
	_, cfg := discardWallFixture(t)

	var out, errb bytes.Buffer
	code := runGitShim([]string{"branch", "-D", "does-not-exist"}, strings.NewReader(""), &out, &errb, cfg)
	if code == 0 {
		t.Fatalf("branch -D does-not-exist: exit = 0, want nonzero (git itself refuses this)")
	}
	if strings.Contains(errb.String(), "gate: refused") {
		t.Fatalf("branch -D does-not-exist stderr = %q, must not print the discard wall's own refusal", errb.String())
	}
	if !strings.Contains(errb.String(), "not found") {
		t.Fatalf("branch -D does-not-exist stderr = %q, want git's own \"not found\" message", errb.String())
	}
}

func TestGitShim_OneShotAllowPassesExactlyOneCommand(t *testing.T) {
	cfgDir := gateConfigDir(t)
	repo, cfg := discardWallFixture(t)
	const session = "s-discard-oneshot"
	t.Setenv("CLAUDE_SESSION_ID", session)
	if _, err := tdd.AllowWall(tdd.WallDiscard); err != nil {
		t.Fatal(err)
	}

	dirty := func() {
		writeFixtureFile(t, repo, "seed.txt", []string{"seed", "dirty"})
	}
	dirty()

	var out, errb bytes.Buffer
	code := runGitShim([]string{"reset", "--hard"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("first reset --hard under the arm: exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if log := readGateLog(t, cfgDir); !strings.Contains(log, "override-discard-used") {
		t.Fatalf("gate.log = %q, want the one-shot use recorded", log)
	}
	if ws := tdd.ListWaivers(); len(ws) != 0 {
		t.Fatalf("ListWaivers() after the one-shot fired = %v, want none", ws)
	}

	dirty()
	var out2, errb2 bytes.Buffer
	code2 := runGitShim([]string{"reset", "--hard"}, strings.NewReader(""), &out2, &errb2, cfg)
	if code2 != 1 {
		t.Fatalf("second reset --hard after the arm was spent: exit = %d, want 1\nstderr: %s", code2, errb2.String())
	}
}

func TestGitShim_OneShotExpiresAfterFiveMinutes(t *testing.T) {
	gateConfigDir(t)
	repo, cfg := discardWallFixture(t)
	const session = "s-discard-expires"
	t.Setenv("CLAUDE_SESSION_ID", session)

	t0 := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	now := t0
	restore := tdd.SetDiscardClockForTest(func() time.Time { return now })
	defer restore()

	if _, err := tdd.AllowWall(tdd.WallDiscard); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, repo, "seed.txt", []string{"seed", "dirty"})
	now = t0.Add(5*time.Minute + time.Second)

	var out, errb bytes.Buffer
	code := runGitShim([]string{"reset", "--hard"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 1 {
		t.Fatalf("reset --hard past the 5-minute arm: exit = %d, want 1\nstderr: %s", code, errb.String())
	}
	if ws := tdd.ListWaivers(); len(ws) != 0 {
		t.Fatalf("ListWaivers() after the expired arm was checked = %v, want none", ws)
	}
}

// TestGitShim_EnvOverridePassesAndIsLogged covers the case APHROLLO_DISCARD=1
// is FOR: everything the discard would throw away is already in the object
// store (staged here), so `git fsck`/`git stash` can still reach it and the
// override costs nothing that cannot be recovered. The unstaged case is the
// next test's, and is refused.
func TestGitShim_EnvOverridePassesAndIsLogged(t *testing.T) {
	cfgDir := gateConfigDir(t)
	repo, cfg := discardWallFixture(t)
	writeFixtureFile(t, repo, "seed.txt", []string{"seed", "dirty"})
	runFixtureGit(t, cfg.realGit, repo, "add", "seed.txt")
	t.Setenv("APHROLLO_DISCARD", "1")

	var out, errb bytes.Buffer
	code := runGitShim([]string{"reset", "--hard"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("reset --hard under APHROLLO_DISCARD=1: exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if log := readGateLog(t, cfgDir); !strings.Contains(log, "override-discard-env") {
		t.Fatalf("gate.log = %q, want the env override recorded", log)
	}
}

// TestGitShim_EnvOverrideRefusesWhenItWouldDestroyUnstagedWork is issue #650's
// first half, in the shape the field reported it: a file carrying work that
// exists NOWHERE but the working tree, APHROLLO_DISCARD=1 in the environment
// (the form the docs recommend for scripts), and the work silently gone. The
// marker's job is to get past the wall deliberately; it was a blanket yes to
// an unbounded loss the caller never saw a number for.
func TestGitShim_EnvOverrideRefusesWhenItWouldDestroyUnstagedWork(t *testing.T) {
	cfgDir := gateConfigDir(t)
	repo, cfg := discardWallFixture(t)
	writeFixtureFile(t, repo, "seed.txt", []string{"seed", "hand-written-work"})
	t.Setenv("APHROLLO_DISCARD", "1")

	var out, errb bytes.Buffer
	code := runGitShim([]string{"reset", "--hard"}, strings.NewReader(""), &out, &errb, cfg)
	// The destruction is asserted FIRST, before the exit code: it is the
	// defect, and a RED that prints the file with the work already gone says
	// so in one line.
	body, err := os.ReadFile(filepath.Join(repo, "seed.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "hand-written-work") {
		t.Fatalf("seed.txt = %q, want the unstaged work still there: APHROLLO_DISCARD=1 must not destroy it", string(body))
	}
	if code != 1 {
		t.Fatalf("reset --hard under APHROLLO_DISCARD=1 with unstaged work: exit = %d, want 1\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "seed.txt") {
		t.Fatalf("refusal = %q, want it to NAME the file whose unstaged work would be lost", errb.String())
	}
	if !strings.Contains(errb.String(), unstagedDiscardEnv+"=1") {
		t.Fatalf("refusal = %q, want it to name the louder marker that does cover unstaged work", errb.String())
	}
	if log := readGateLog(t, cfgDir); !strings.Contains(log, "git-discard-refused:reset---hard-unstaged") {
		t.Fatalf("gate.log = %q, want the unstaged refusal recorded", log)
	}
}

// TestGitShim_UnstagedMarkerNamesWhatItDestroysThenRuns pins the louder
// marker's whole contract: it is distinct from APHROLLO_DISCARD, it prints
// the files it is about to destroy BEFORE git runs, and it is counted.
func TestGitShim_UnstagedMarkerNamesWhatItDestroysThenRuns(t *testing.T) {
	cfgDir := gateConfigDir(t)
	repo, cfg := discardWallFixture(t)
	writeFixtureFile(t, repo, "seed.txt", []string{"seed", "hand-written-work"})
	t.Setenv(unstagedDiscardEnv, "1")

	var out, errb bytes.Buffer
	code := runGitShim([]string{"reset", "--hard"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("reset --hard under the unstaged marker: exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "seed.txt") {
		t.Fatalf("stderr = %q, want the destroyed file named before the discard ran", errb.String())
	}
	body, err := os.ReadFile(filepath.Join(repo, "seed.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "hand-written-work") {
		t.Fatal("reset --hard under the unstaged marker must actually have run")
	}
	if log := readGateLog(t, cfgDir); !strings.Contains(log, "override-discard-unstaged") {
		t.Fatalf("gate.log = %q, want the unstaged override recorded", log)
	}
}

// TestGitShim_EnvOverrideNamesOnlyTheUnstagedFilesInScope keeps the refusal's
// claim exactly as wide as what the invocation would lose: a path-scoped
// `checkout -- <paths>` names the file it names, not every dirty file in the
// tree.
func TestGitShim_EnvOverrideNamesOnlyTheUnstagedFilesInScope(t *testing.T) {
	gateConfigDir(t)
	repo, cfg := discardWallFixture(t)
	writeFixtureFile(t, repo, "a.txt", []string{"a"})
	writeFixtureFile(t, repo, "b.txt", []string{"b"})
	runFixtureGit(t, cfg.realGit, repo, "add", ".")
	runFixtureGit(t, cfg.realGit, repo, "commit", "-qm", "tracked base")
	writeFixtureFile(t, repo, "a.txt", []string{"a", "unstaged"})
	writeFixtureFile(t, repo, "b.txt", []string{"b", "unstaged"})
	t.Setenv("APHROLLO_DISCARD", "1")

	var out, errb bytes.Buffer
	code := runGitShim([]string{"checkout", "--", "b.txt"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 1 {
		t.Fatalf("checkout -- b.txt under APHROLLO_DISCARD=1: exit = %d, want 1\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "b.txt") {
		t.Fatalf("refusal = %q, want it to name b.txt", errb.String())
	}
	if strings.Contains(errb.String(), "a.txt") {
		t.Fatalf("refusal = %q, must not name a.txt: this invocation would not touch it", errb.String())
	}
}
