package tdd

import (
	"slices"
	"testing"
)

// The fail-first stage runs cargo in a worktree holding HEAD's SOURCE plus the
// staged TESTS, and it deliberately points that run at the repo's own shared
// CARGO_TARGET_DIR (one target dir per repo, so a commit does not cold-build
// what is already built next door). The consequence was not deliberate: the
// artifacts it writes are built from HEAD's implementation, they land on top
// of the ones the mechanical stage is about to use, and their mtime is newer
// than the staged source. cargo then considers them fresh and does not
// rebuild, so the mechanical stage can run the NEW tests against the OLD
// implementation.
//
// Reported from a borld lane with a kernel fix plus two new tests: fail-first
// red-proven, then the mechanical run reported the new test failing, and
// re-running the identical command by hand failed again in 0.008s with no
// compilation, until the source was touched. The direction that matters is
// the opposite one: a test that SHOULD fail against the new code passes
// against the stale binary and the gate reports green.
func TestPrecommit_FailFirst_InvalidatesTheArtifactsItBuiltFromHEAD(t *testing.T) {
	withLinter(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)

	// Staged: a source change plus a genuinely new test, which is what makes
	// fail-first fire at all.
	write(t, root, "src/lib.rs", "pub fn base() -> i32 { 1 }\n")
	write(t, root, "tests/floor.rs", "#[test]\nfn floor_is_one() {\n    assert_eq!(m::base(), 1);\n}\n")
	gitDo(t, root, "add", ".")

	var runs []loggedRun
	// The worktree run FAILS (the new test does not pass against HEAD's
	// source), which is the clean RED fail-first wants; the mechanical run in
	// root passes.
	Precommit(root, recordAllRuns(&runs, func(dir string) bool { return dir == root }))

	failFirstAt, cleanAt := -1, -1
	for i, r := range runs {
		if r.dir != root && failFirstAt < 0 {
			failFirstAt = i
		}
		if r.runner.Cmd == "cargo" && len(r.runner.Args) > 0 && r.runner.Args[0] == "clean" {
			cleanAt = i
		}
	}
	if failFirstAt < 0 {
		t.Fatalf("fail-first never ran, so this test proves nothing: %+v", runs)
	}
	if cleanAt < 0 {
		t.Fatalf("nothing invalidated the artifacts fail-first built from HEAD; runs were %+v", runs)
	}
	if cleanAt < failFirstAt {
		t.Errorf("the invalidation ran at %d, before fail-first at %d — it must drop what fail-first wrote, not what preceded it", cleanAt, failFirstAt)
	}
	if want := []string{"clean", "-p", "m"}; !slices.Equal(runs[cleanAt].runner.Args, want) {
		t.Errorf("invalidation args = %q, want %q — only the packages fail-first rebuilt, never the whole target dir", runs[cleanAt].runner.Args, want)
	}
	if runs[cleanAt].runner.Dir != root {
		t.Errorf("invalidation ran in %q, want the repo root %q — the poisoned artifacts are in the SHARED target dir, not the worktree's", runs[cleanAt].runner.Dir, root)
	}
}

// A Go repo has no cargo target dir to poison, so it must not pay for an
// invalidation: the guard against fixing this everywhere instead of where it
// breaks.
func TestPrecommit_FailFirst_DoesNotInvalidateForANonCargoRepo(t *testing.T) {
	withLinter(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")

	var runs []loggedRun
	Precommit(root, recordAllRuns(&runs, func(dir string) bool { return dir == root }))

	for _, r := range runs {
		if r.runner.Cmd == "cargo" {
			t.Errorf("a Go repo ran %q %q — there is no cargo target dir to invalidate", r.runner.Cmd, r.runner.Args)
		}
	}
}
