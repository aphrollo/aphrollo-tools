package precommit

import (
	"path/filepath"
	"strings"
	"testing"
)

// refuseToRun is a SuiteRunner for commits that must be decided before any
// suite is worth compiling: reaching it at all is the failure.
func refuseToRun(t *testing.T) SuiteRunner {
	t.Helper()
	return func(r Runner, root string) SuiteResult {
		t.Errorf("no suite should run for this commit, but %s ran in %s", cmdString(r), root)
		return SuiteResult{Passed: true}
	}
}

// A commit that stages nothing but a hand-raised ceiling is the exact shape
// the baseline guard exists for, and it was the one shape that skipped it: no
// staged source or test meant the gate returned before the guard ran.
func TestPrecommit_RejectsARaisedBaselineInACommitWithNoCode(t *testing.T) {
	root := baselineRepo(t, ".ratchet/baselines/module_size.txt",
		"# header\ncrates/a.rs | 1048\n",
		"# header\ncrates/a.rs | 1049\n")

	res := Precommit(root, refuseToRun(t))
	if !res.Blocked || !strings.Contains(res.Message, "1048 -> 1049") {
		t.Fatalf("a raised baseline must reject even with no code staged: %+v", res)
	}
}

// Same hole one stage over: the laws judge the tree, and a docs-only commit
// walked past them entirely.
func TestPrecommit_RunsTheLawsInACommitWithNoCode(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "no-todo.toml"), `
name = "no-todo"
description = "Docs do not ship TODOs"
severity = "deny"
escape = "<!-- todo-ok:"
baseline = ".ratchet/baselines/no-todo.txt"

[scope]
include = ["docs/**/*.md"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "no-todo.txt"), "")
	mustWrite(t, filepath.Join(root, "docs", "guide.md"), "clean\n")
	gitAddAll(t, root)
	commitAll(t, root)

	mustWrite(t, filepath.Join(root, "docs", "guide.md"), "TODO: finish\n")
	gitAddAll(t, root)

	res := Precommit(root, refuseToRun(t))
	if !res.Blocked || !strings.Contains(res.Message, "no-todo") {
		t.Fatalf("a docs-only commit must still answer to the laws: %+v", res)
	}
}

// The merge gate carries the same two stages and had the same early return.
func TestMechanical_RejectsARaisedBaselineInAMergeWithNoCode(t *testing.T) {
	root := baselineRepo(t, ".ratchet/baselines/module_size.txt",
		"# header\ncrates/a.rs | 1048\n",
		"# header\ncrates/a.rs | 1049\n")

	res := Mechanical(root, refuseToRun(t))
	if !res.Blocked || !strings.Contains(res.Message, "1048 -> 1049") {
		t.Fatalf("a raised baseline must reject a docs-only merge: %+v", res)
	}
}
