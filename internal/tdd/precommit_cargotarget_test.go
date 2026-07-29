package tdd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPrecommit_MechanicalCargoRun_TargetDirPolicy pins the isolation contract
// for the mechanical stage's cargo run: a CARGO_TARGET_DIR pointing OUTSIDE the
// repo being committed is the cross-checkout artifact-poisoning vector (two
// divergent checkouts sharing one warm target produce phantom compile/test
// failures), so the gate must swap it for a gate-owned per-repo target. A
// repo-local target — or none at all — is honest and must pass through.
func TestPrecommit_MechanicalCargoRun_TargetDirPolicy(t *testing.T) {
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

	t.Run("a foreign target dir is replaced with a gate-owned one", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeCargoRepo(t)
		stageSourceChange(t, root)
		foreign := filepath.Join(t.TempDir(), "someone-elses-checkout", "target")
		t.Setenv("CARGO_TARGET_DIR", foreign)

		var seen string
		Precommit(root, record(&seen))
		if seen == foreign {
			t.Fatal("the mechanical cargo run must not build into a target dir outside the repo being committed")
		}
		if seen == "" {
			t.Fatal("with a state dir available the run should get the gate-owned per-repo target, not an unset env")
		}
		if os.Getenv("CARGO_TARGET_DIR") != foreign {
			t.Fatal("the operator's CARGO_TARGET_DIR must be restored after the run")
		}
	})

	t.Run("a repo-local target dir passes through", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeCargoRepo(t)
		stageSourceChange(t, root)
		local := filepath.Join(root, "target")
		t.Setenv("CARGO_TARGET_DIR", local)

		var seen string
		Precommit(root, record(&seen))
		if seen != local {
			t.Fatalf("a target dir inside the repo is not a poisoning vector and must be inherited, got %q", seen)
		}
	})

	t.Run("no target dir stays unset", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeCargoRepo(t)
		stageSourceChange(t, root)
		os.Unsetenv("CARGO_TARGET_DIR")

		var seen string
		Precommit(root, record(&seen))
		if seen != "" {
			t.Fatalf("with no operator CARGO_TARGET_DIR the run must use cargo's default repo-local target, got %q", seen)
		}
	})
}
