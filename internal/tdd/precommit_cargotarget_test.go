package tdd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestPrecommit_MechanicalCargoRun_AlwaysBuildsInTheGateTarget pins the
// isolation contract for the mechanical stage's cargo run: it builds in the
// GATE-OWNED per-repo target dir, always — whatever the operator's own
// CARGO_TARGET_DIR says, and whether or not they set one.
//
// Two problems it solves at once. A target dir outside the repo being
// committed is the cross-checkout poisoning vector (two divergent checkouts
// sharing one warm target produce phantom compile failures). And sharing the
// DEV's target dir means the gate and the human contend for the same build
// directory on every commit — which is how ten commits in one gate.log
// waited out the full lock budget and landed untested. With the build lock
// keyed per target dir, a gate-owned target is a slot the gate almost always
// gets.
func TestPrecommit_MechanicalCargoRun_AlwaysBuildsInTheGateTarget(t *testing.T) {
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

	t.Run("a foreign target dir is replaced with the gate-owned one", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeCargoRepo(t)
		stageSourceChange(t, root)
		foreign := filepath.Join(t.TempDir(), "someone-elses-checkout", "target")
		t.Setenv("CARGO_TARGET_DIR", foreign)

		var seen string
		Precommit(root, record(&seen))
		if want := cargoFailFirstTarget(root); seen != want {
			t.Fatalf("mechanical run built in %q, want the gate-owned target %q", seen, want)
		}
		if os.Getenv("CARGO_TARGET_DIR") != foreign {
			t.Fatal("the operator's CARGO_TARGET_DIR must be restored after the run")
		}
	})

	t.Run("the operator's repo-local target is not shared either", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeCargoRepo(t)
		stageSourceChange(t, root)
		local := filepath.Join(root, "target")
		t.Setenv("CARGO_TARGET_DIR", local)

		var seen string
		Precommit(root, record(&seen))
		if seen == local {
			t.Fatal("the gate must not build into the dev's own target dir — that is what makes every commit contend with the human")
		}
		if want := cargoFailFirstTarget(root); seen != want {
			t.Fatalf("mechanical run built in %q, want the gate-owned target %q", seen, want)
		}
	})

	t.Run("with no target dir set the gate still uses its own", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeCargoRepo(t)
		stageSourceChange(t, root)
		os.Unsetenv("CARGO_TARGET_DIR")

		var seen string
		Precommit(root, record(&seen))
		if want := cargoFailFirstTarget(root); seen != want {
			t.Fatalf("mechanical run built in %q, want the gate-owned target %q", seen, want)
		}
		if _, set := os.LookupEnv("CARGO_TARGET_DIR"); set {
			t.Fatal("CARGO_TARGET_DIR must be unset again after the run — the operator never set one")
		}
	})
}

// TestCargoFailFirstTarget_SharedAcrossLinkedWorktrees pins the fix for the
// worktree-thrash waste: every throwaway `.claude/worktrees/agent-*` linked
// worktree of ONE repo shares a single warm gate-owned cargo target dir,
// because the key is now the repo's git COMMON dir (same for the main clone
// and every worktree linked to it) rather than repoRoot (unique per worktree
// path, so each one cold-built its own target before this fix).
func TestCargoFailFirstTarget_SharedAcrossLinkedWorktrees(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t) // any committed git repo; the target key doesn't care about language
	wtDir := filepath.Join(t.TempDir(), "linked-wt")
	gitDo(t, root, "worktree", "add", "-b", "feature-x", wtDir, "HEAD")

	mainTarget := cargoFailFirstTarget(root)
	wtTarget := cargoFailFirstTarget(wtDir)
	if mainTarget == "" || wtTarget == "" {
		t.Fatalf("expected non-empty target dirs, got main=%q wt=%q", mainTarget, wtTarget)
	}
	if mainTarget != wtTarget {
		t.Fatalf("a linked worktree must share its main clone's cargo target dir, got main=%q wt=%q", mainTarget, wtTarget)
	}
}

// TestCargoFailFirstTarget_FallsBackToRepoRootHashWhenGitFails pins the
// fallback: when git can't resolve a common dir for the given root (no .git
// at all here), cargoFailFirstTarget falls back to hashing repoRoot itself,
// exactly as it did before this change.
func TestCargoFailFirstTarget_FallsBackToRepoRootHashWhenGitFails(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	notARepo := t.TempDir() // no .git — `git rev-parse --git-common-dir` errors

	got := cargoFailFirstTarget(notARepo)
	if got == "" {
		t.Fatal("expected a fallback target dir even when git fails")
	}
	sum := sha256.Sum256([]byte(notARepo))
	want := filepath.Join(cfg, "tdd-state", "cargo-target", hex.EncodeToString(sum[:8]))
	if got != want {
		t.Fatalf("fallback target = %q, want the repoRoot-hashed path %q", got, want)
	}
}
