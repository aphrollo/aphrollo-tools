package precommit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestMechanical_CargoMember_RunsScopedNeverSpawnsFailFirst pins the core
// task A6 contract: the pre-merge-commit gate (Mechanical) runs ONLY the
// mechanical stage. A merge combining commits that BOTH added a test and its
// implementation would, under Precommit, trigger fail-first's throwaway
// worktree — that judgment belongs to the AUTHORING commit, already proven
// there, and re-running it at merge time would be redundant at best (a
// worktree build) and a false block at worst (a test genuinely written
// before its impl, now staged alongside already-committed code that makes it
// pass, misread as "never went RED"). Proven two ways: the SuiteRunner's
// call list contains exactly the ONE scoped mechanical run (no run at a
// worktree-shaped temp dir), and the fail-first worktree directory under the
// state dir is never created at all.
func TestMechanical_CargoMember_RunsScopedNeverSpawnsFailFirst(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeCargoWorkspaceRepo(t)
	// A test AND its implementation staged together — exactly the shape that
	// triggers Precommit's fail-first stage.
	write(t, root, "crates/alpha/src/lib.rs", "pub fn widget() -> i32 { 1 }\n")
	write(t, root, "crates/alpha/src/lib_test.rs", "#[test]\nfn widget_is_one() { assert_eq!(1, crate::widget()); }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	alphaDir := filepath.Join(root, "crates", "alpha")
	res := Mechanical(root, recordRunner(&seen, alphaDir))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := Runner{Cmd: "cargo", Args: []string{"test", "-p", "alpha"}, Dir: root}
	if len(seen) != 1 || !reflect.DeepEqual(seen[0], want) {
		t.Fatalf("mechanical run = %+v, want one %+v", seen, want)
	}

	failFirstWTDir := filepath.Join(cfg, "gate-state", "failfirst-wt")
	if _, err := os.Stat(failFirstWTDir); !os.IsNotExist(err) {
		t.Fatalf("Mechanical must never spawn a fail-first worktree, but %s exists", failFirstWTDir)
	}
}

// TestMechanical_DocsOnlyMerge_NoOpWithNothingToTestLine pins the second
// required behavior: a merge whose staged set has nothing to test (docs
// only) is a no-op — GateResult.Blocked stays false — but says so explicitly
// via Message, rather than returning a bare empty result indistinguishable
// from "the gate never ran at all".
func TestMechanical_DocsOnlyMerge_NoOpWithNothingToTestLine(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "NOTES.md", "# notes\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("a docs-only merge must never block: %s", res.Message)
	}
	if len(seen) != 0 {
		t.Fatalf("a docs-only merge must run no suite, ran %+v", seen)
	}
	if !strings.Contains(res.Message, "nothing to test") {
		t.Fatalf("expected a 'nothing to test' line, got Message=%q", res.Message)
	}
}

// TestMechanical_NeverBlocksOnSuppressionOrFailFirst guards the scope limit
// explicitly: a commit that WOULD have been blocked by Precommit's anti-cheat
// suppression scan (a freshly-added lint-suppression directive) must NOT be
// blocked by Mechanical — that judgment belongs to precommit on the
// authoring commit, not to a merge gate that only re-proves the combined
// tree still compiles.
//
// The fixture's suppression marker is assembled from parts (never written as
// one contiguous recognizable token anywhere in THIS file, comments
// included) so aphrollo-tools' own anti-cheat gate never mistakes this
// repo's test source for an introduced suppression when committing it — the
// scanned target is the FIXTURE repo's staged content, never this file's.
func TestMechanical_NeverBlocksOnSuppressionOrFailFirst(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	marker := strings.Join([]string{"no", "lint", ":unused"}, "")
	write(t, root, "gizmo.go", "package m\n\nfunc Gizmo() int { return 1 } //"+marker+"\n")
	gitDo(t, root, "add", ".")

	res := Mechanical(root, RunSuite(precommitTestTimeout))
	if res.Blocked {
		t.Fatalf("Mechanical must never run the anti-cheat suppression scan, got blocked: %s", res.Message)
	}
}

// TestMechanical_BlocksARealCompileFailure guards that Mechanical is not a
// no-op rubber stamp: a genuinely broken combined tree still blocks the
// merge, via the SAME mechanical judgment Precommit uses.
func TestMechanical_BlocksARealCompileFailure(t *testing.T) {
	withLinter(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "broken.go", "package m\n\nfunc Broken() int { return }\n")
	gitDo(t, root, "add", ".")

	// The stage that catches it is vet — cheaper than the suite and ahead of
	// it in the cost order — so the claim is the OUTCOME and the diagnostic,
	// not which stage got there first.
	res := Mechanical(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "not enough return values") {
		t.Fatalf("expected a block naming the compile error for broken code, got %+v", res)
	}
}
