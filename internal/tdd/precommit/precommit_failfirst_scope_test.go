package precommit

import (
	"strings"
	"testing"
)

// The fail-first stage exists to prove NEW tests went RED. A commit whose
// test-file changes add no test declaration — a lint reflow, a renamed helper
// variable, gofmt across the tree — has nothing for fail-first to judge, and
// today it charges the full worktree-suite run anyway (and can even
// false-block: a reformatted EXISTING test passes at HEAD by construction and
// reads as a violation). Such commits must skip fail-first; the mechanical
// stage still gates them.
func TestPrecommit_FailFirst_SkipsWhenNoTestDeclarationAdded(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	// Committed baseline: a real test alongside its impl.
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tgot := Widget()\n\tif got != 1 {\n\t\tt.Fatal(got)\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "widget")

	// Staged: a source change plus a cosmetic test edit (rename a local) —
	// no new test declaration anywhere.
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 } // tuned\n")
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tresult := Widget()\n\tif result != 1 {\n\t\tt.Fatal(result)\n\t}\n}\n")
	gitDo(t, root, "add", ".")

	var runs []loggedRun
	res := Precommit(root, recordAllRuns(&runs, func(string) bool { return true }))
	if res.Blocked {
		t.Fatalf("a declaration-free test edit must not block: %s", res.Message)
	}
	for _, r := range runs {
		if r.dir != root {
			t.Fatalf("fail-first (worktree run in %s) must not fire when the staged test changes add no test declaration", r.dir)
		}
	}
	if len(runs) != 0 {
		t.Fatalf("the commit gate runs no suite, so a declaration-free test edit must run nothing; got %d runs: %+v", len(runs), runs)
	}
}

// Adding a genuinely NEW test keeps the stage: a fresh `func Test…` that
// passes against HEAD is exactly the violation fail-first exists to catch.
func TestPrecommit_FailFirst_StillFiresOnANewTestDeclaration(t *testing.T) {
	withLinter(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) { _ = 1 }\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "fail-first") {
		t.Fatalf("a new test that passes at HEAD must still fail-first-block, got %+v", res)
	}
}
