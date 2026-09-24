package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #813, from borld's forge_solver lane: the lane ran `git merge main`
// twice, the second merge brought four new integration test files into the
// crate, and the merge gate's [mechanical] stage answered cache-hit. The key
// is right about the merged tree — it names the four files — so a green was
// recorded under it by something that never compiled them: the edit hook
// hashed the worktree AFTER its own run, and a merge that landed while that
// run was going was the state it hashed. A green is a fact about the tree the
// run saw, and a tree that moved under the run was not seen.

// midMergeLane is a cargo workspace with one crate, forge_solver, whose
// integration target is tests/integration/main.rs with a rung/ module tree.
// The lane has one commit of its own and has already merged main once;
// main has since gained four new rung test files. mergeAgain lands the
// second merge the way `git merge main` does up to its pre-merge-commit hook:
// merged index and worktree, MERGE_HEAD set, HEAD still the lane's tip.
func midMergeLane(t *testing.T) (crate string, mergeAgain func()) {
	t.Helper()
	repo := t.TempDir()
	gitInit(t, repo)
	write(t, repo, "Cargo.toml", "[workspace]\nmembers = [\"crates/forge_solver\"]\nresolver = \"2\"\n")
	write(t, repo, "crates/forge_solver/Cargo.toml", "[package]\nname = \"forge_solver\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, repo, "crates/forge_solver/src/lib.rs", "pub fn force() -> f64 { 1.0 }\n")
	write(t, repo, "crates/forge_solver/tests/integration/main.rs", "mod rung;\n")
	write(t, repo, "crates/forge_solver/tests/integration/rung/mod.rs", "mod base;\n")
	write(t, repo, "crates/forge_solver/tests/integration/rung/base.rs", "#[test]\nfn base() { assert_eq!(forge_solver::force(), 1.0); }\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "base")
	gitDo(t, repo, "branch", "-M", "main")

	lane := filepath.Join(t.TempDir(), "lane")
	gitDo(t, repo, "worktree", "add", "-q", "-b", "lane/adaptive-init", lane)
	write(t, lane, "crates/forge_solver/src/lib.rs", "pub fn force() -> f64 { 1.0 }\npub fn init() -> f64 { 0.5 }\n")
	gitDo(t, lane, "commit", "-qam", "lane work")

	write(t, repo, "crates/forge_solver/src/particle.rs", "pub fn bdf2() {}\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "first test-only lane")
	gitDo(t, lane, "merge", "-q", "--no-ff", "-m", "merge main", "main")

	write(t, repo, "crates/forge_solver/tests/integration/rung/mod.rs", "mod anchor;\nmod base;\nmod onset;\nmod onset_measure;\nmod segment;\n")
	for _, name := range []string{"onset", "onset_measure", "anchor", "segment"} {
		write(t, repo, "crates/forge_solver/tests/integration/rung/"+name+".rs",
			"#[test]\nfn "+name+"() { assert!(forge_solver::force() > 2.0); }\n")
	}
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "second test-only lane")

	return filepath.Join(lane, "crates", "forge_solver"), func() {
		gitDo(t, lane, "merge", "--no-commit", "--no-ff", "main")
	}
}

// requireNoGreenForMergedTree fails when any command that ran is recorded
// green under the merged tree's key — the lookup the merge gate's stages make.
func requireNoGreenForMergedTree(t *testing.T, crate string, ran []Runner) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(crate, "tests", "integration", "rung", "onset.rs")); err != nil {
		t.Fatalf("setup: the second merge did not land during the run: %v", err)
	}
	merged := worktreeStateHash(crate)
	if merged == "" {
		t.Fatal("setup: no state hash for the merged tree")
	}
	if len(ran) == 0 {
		t.Fatal("setup: the edit hook ran no suite")
	}
	for _, r := range ran {
		if mechCacheHit(mechKey(crate, merged, r)) {
			t.Fatalf("%q is recorded green for the merged tree, which carries four test files that run never compiled — "+
				"the merge gate's lookup for that tree reads cache-hit", cmdString(r))
		}
	}
}

func TestPostEdit_RecordsNoGreenForAMergeThatLandedDuringItsRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate, mergeAgain := midMergeLane(t)
	target := filepath.Join(crate, "src", "lib.rs")
	write(t, crate, "src/lib.rs", "pub fn force() -> f64 { 1.0 }\npub fn init() -> f64 { 0.25 }\n")

	var ran []Runner
	run := func(r Runner, _ string) SuiteResult {
		ran = append(ran, r)
		mergeAgain()
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed\n", Duration: time.Second}
	}
	got := PostEdit(postPayload("Edit", target), run)
	if !strings.Contains(got, "gate:") {
		t.Fatalf("setup: the edit hook reported nothing: %q", got)
	}

	requireNoGreenForMergedTree(t, crate, ran)
}

// The deferred edit path records its green the same way, after a run that
// finished inside the hook's budget.
func TestPostEditDeferred_RecordsNoGreenForAMergeThatLandedDuringItsRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate, mergeAgain := midMergeLane(t)
	target := filepath.Join(crate, "src", "lib.rs")
	write(t, crate, "src/lib.rs", "pub fn force() -> f64 { 1.0 }\npub fn init() -> f64 { 0.25 }\n")

	var ran []Runner
	prev := spawnPhaseFn
	spawnPhaseFn = func(j DeferredJob) (DeferredJob, bool) {
		j.PID = 4100
		j.Started = time.Now()
		saveDeferredJob(j)
		j, _ = loadDeferredJob(j.Session, j.Project)
		if j.Phase == "run" {
			ran = append(ran, runnerFromArgv(j.Runner, j.Dir))
			mergeAgain()
		}
		if err := os.WriteFile(j.Log, []byte("test result: ok. 1 passed; 0 failed\n"), 0o600); err != nil {
			t.Error(err)
		}
		writePhaseResult(j.Result, PhaseOutcome{ExitCode: 0})
		return j, true
	}
	t.Cleanup(func() { spawnPhaseFn = prev })
	EnableDeferredPhases(true)
	t.Cleanup(func() { EnableDeferredPhases(false) })

	got := PostEdit(postPayload("Edit", target), nil)
	if !strings.Contains(got, "gate:") {
		t.Fatalf("setup: the edit hook reported nothing: %q", got)
	}

	requireNoGreenForMergedTree(t, crate, ran)
}
