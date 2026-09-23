package precommit

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPrecommit_MechanicalCargoRun_BuildsInTheReposOwnTarget pins the
// target-dir contract after the 2026-09-02 decision: ONE target dir per repo.
// The gate builds where the developer builds — CARGO_TARGET_DIR when the
// environment names one, else the repo's own target/ — and the per-target
// build lock is what keeps the two off each other's toes.
//
// It used to build into a private <stateDir>/cargo-target/<hash>. That kept
// the gate out of the developer's way, and cost a second full copy of the
// workspace's artifacts (a measured 155 GB) plus a cold compile on every
// commit of anything the developer had already built next door.
func TestPrecommit_MechanicalCargoRun_BuildsInTheReposOwnTarget(t *testing.T) {
	record := func(seen *string) SuiteRunner {
		return func(r Runner, dir string) SuiteResult {
			*seen = os.Getenv("CARGO_TARGET_DIR")
			return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed"}
		}
	}
	stageSourceChange := func(t *testing.T, root string) {
		write(t, root, "src/lib.rs", "pub fn one() -> i32 { 2 }\n")
		gitDo(t, root, "add", ".")
	}

	t.Run("the environment's target dir is honoured", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeCargoRepo(t)
		stageSourceChange(t, root)
		shared := filepath.Join(t.TempDir(), "shared-target")
		t.Setenv("CARGO_TARGET_DIR", shared)

		var seen string
		Precommit(root, record(&seen))
		if seen != shared {
			t.Fatalf("mechanical run built in %q, want the operator's own %q", seen, shared)
		}
		if os.Getenv("CARGO_TARGET_DIR") != shared {
			t.Fatal("the operator's CARGO_TARGET_DIR must be restored after the run")
		}
	})

	t.Run("with no target dir set the repo's own target is used", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeCargoRepo(t)
		stageSourceChange(t, root)
		os.Unsetenv("CARGO_TARGET_DIR")

		var seen string
		Precommit(root, record(&seen))
		if want := filepath.Join(root, "target"); filepath.Clean(seen) != filepath.Clean(want) {
			t.Fatalf("mechanical run built in %q, want the repo's own %q", seen, want)
		}
		if _, set := os.LookupEnv("CARGO_TARGET_DIR"); set {
			t.Fatal("CARGO_TARGET_DIR must be unset again after the run — the operator never set one")
		}
	})
}

// TestResolvedDevTarget_IsPerCheckout pins what "the repo's own target" means
// for a linked worktree: its OWN target/, exactly like the developer's builds
// there. A shared warm target is still available — by exporting
// CARGO_TARGET_DIR, which the gate then honours.
func TestResolvedDevTarget_IsPerCheckout(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	os.Unsetenv("CARGO_TARGET_DIR")
	root := makeGoRepo(t)
	wtDir := filepath.Join(t.TempDir(), "linked-wt")
	gitDo(t, root, "worktree", "add", "-b", "feature-x", wtDir, "HEAD")

	if a, b := resolvedDevTarget(root), resolvedDevTarget(wtDir); a == b {
		t.Fatalf("main and worktree both resolved to %q — each checkout builds into its own target unless the env says otherwise", a)
	}
	shared := t.TempDir()
	t.Setenv("CARGO_TARGET_DIR", shared)
	if got := resolvedDevTarget(wtDir); filepath.Clean(got) != filepath.Clean(shared) {
		t.Fatalf("resolvedDevTarget = %q, want the exported %q", got, shared)
	}
}
