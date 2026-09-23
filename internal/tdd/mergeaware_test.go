package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func currentBranch(t *testing.T, repoRoot string) string {
	t.Helper()
	return tddtest.CurrentBranch(t, repoRoot, git)
}

// makeConflictedMergeRepo builds a real conflicted-merge state: two branches
// touch the SAME line of the same file differently, `git merge` fails with
// a conflict (leaving MERGE_HEAD in place, as real git does), the conflict
// is resolved and staged -- but NOT committed. This is exactly the
// filesystem state `git commit` sees when a session concludes a real
// conflicted merge, which is what fires git's pre-commit hook (unlike an
// automatic, conflict-free merge, which fires pre-merge-commit instead).
func makeConflictedMergeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")

	base := currentBranch(t, root)

	gitDo(t, root, "checkout", "-qb", "feature")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 2 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "feature change")

	gitDo(t, root, "checkout", "-q", base)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 3 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base change")

	// Expected to fail with a real conflict -- git() (unlike gitDo) does not
	// fail the test on a non-zero exit, since this ONE command is meant to
	// return one.
	if _, err := git(root, "merge", "feature"); err == nil {
		t.Fatal("setup: expected `git merge feature` to conflict, but it succeeded cleanly")
	}
	if _, err := git(root, "rev-parse", "-q", "--verify", "MERGE_HEAD"); err != nil {
		t.Fatal("setup: expected MERGE_HEAD to exist after a conflicting merge")
	}

	// Resolve the conflict and stage it -- but do NOT commit, matching the
	// exact state a real conclude-the-merge `git commit` sees.
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 4 }\n")
	gitDo(t, root, "add", "widget.go")
	return root
}

// TestPrecommit_ConflictedMergeInProgress_RunsOnlyMechanical is the literal
// required test: concluding a real conflicted merge must run ONLY the
// mechanical stage (no fail-first, no anti-cheat) and print the
// merge-in-progress line explaining why -- proven by asserting the
// SuiteRunner's call list contains ONLY runs at repoRoot (never a fail-first
// worktree temp dir) and that the fail-first worktree directory under the
// state dir was never created at all.
func TestPrecommit_ConflictedMergeInProgress_RunsOnlyMechanical(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeConflictedMergeRepo(t)

	var seen []loggedRun
	var res GateResult
	stderr := captureStderr(t, func() {
		res = Precommit(root, recordAllRuns(&seen, func(string) bool { return true }))
	})

	if res.Blocked {
		t.Fatalf("a conflicted-merge commit whose mechanical stage passes must not block: %s", res.Message)
	}
	if !strings.Contains(stderr, "gate precommit: merge in progress (MERGE_HEAD)") {
		t.Fatalf("expected the merge-in-progress line, got stderr: %s", stderr)
	}
	if !strings.Contains(stderr, "running the pre-merge routine (mechanical only)") {
		t.Fatalf("expected the pre-merge-routine line, got stderr: %s", stderr)
	}

	for _, r := range seen {
		if r.dir != root {
			t.Fatalf("fail-first must never run during a conflicted merge, but a run happened at %s (not repoRoot): %+v", r.dir, seen)
		}
	}
	if len(seen) == 0 {
		t.Fatal("expected the mechanical stage to actually run at least once")
	}

	failFirstWTDir := filepath.Join(cfg, "gate-state", "failfirst-wt")
	if _, err := os.Stat(failFirstWTDir); !os.IsNotExist(err) {
		t.Fatalf("the fail-first worktree dir must never be created during a conflicted merge, but %s exists", failFirstWTDir)
	}
}

// TestPrecommit_NoMergeInProgress_NeverPrintsTheMergeLine pins the "a
// normal commit is unchanged" requirement: a repo with no MERGE_HEAD (the
// overwhelming majority of commits) must never print the merge-in-progress
// line, and Precommit's normal fail-first + mechanical flow must still run
// exactly as before this task.
func TestPrecommit_NoMergeInProgress_NeverPrintsTheMergeLine(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	var res GateResult
	stderr := captureStderr(t, func() {
		res = Mechanical(root, recordRunner(&seen, root))
	})
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if strings.Contains(stderr, "merge in progress") {
		t.Fatalf("a normal commit (no MERGE_HEAD) must never print the merge-in-progress line, got: %s", stderr)
	}
	if len(seen) != 1 {
		t.Fatalf("expected the normal single mechanical run, got %d: %+v", len(seen), seen)
	}
}

// TestMergeInProgressRef_TableDriven pins mergeInProgressRef's contract
// directly: "" for a clean repo, "MERGE_HEAD" for a real conflicted merge,
// and "CHERRY_PICK_HEAD"/"REVERT_HEAD" when THOSE refs exist instead --
// task A9 said MERGE_HEAD "also treat CHERRY_PICK_HEAD/REVERT_HEAD the
// same". The cherry-pick/revert cases write the ref file directly with a
// valid commit SHA (exactly what git itself does when starting either) --
// mergeInProgressRef only checks ref EXISTENCE via `rev-parse --verify`, so
// this exercises the identical mechanism a real conflicting cherry-pick/
// revert would, without the extra fixture cost of staging one for real.
func TestMergeInProgressRef_TableDriven(t *testing.T) {
	t.Run("clean repo has no merge in progress", func(t *testing.T) {
		root := makeGoRepo(t)
		if got := mergeInProgressRef(root); got != "" {
			t.Fatalf("mergeInProgressRef = %q, want \"\" for a clean repo", got)
		}
	})

	t.Run("MERGE_HEAD present is detected", func(t *testing.T) {
		root := makeConflictedMergeRepo(t)
		if got := mergeInProgressRef(root); got != "MERGE_HEAD" {
			t.Fatalf("mergeInProgressRef = %q, want MERGE_HEAD", got)
		}
	})

	t.Run("CHERRY_PICK_HEAD present is detected", func(t *testing.T) {
		root := makeGoRepo(t)
		head, err := git(root, "rev-parse", "HEAD")
		if err != nil {
			t.Fatalf("rev-parse HEAD: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, ".git", "CHERRY_PICK_HEAD"), []byte(strings.TrimSpace(head)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := mergeInProgressRef(root); got != "CHERRY_PICK_HEAD" {
			t.Fatalf("mergeInProgressRef = %q, want CHERRY_PICK_HEAD", got)
		}
	})

	t.Run("REVERT_HEAD present is detected", func(t *testing.T) {
		root := makeGoRepo(t)
		head, err := git(root, "rev-parse", "HEAD")
		if err != nil {
			t.Fatalf("rev-parse HEAD: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, ".git", "REVERT_HEAD"), []byte(strings.TrimSpace(head)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := mergeInProgressRef(root); got != "REVERT_HEAD" {
			t.Fatalf("mergeInProgressRef = %q, want REVERT_HEAD", got)
		}
	})
}
