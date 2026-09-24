package precommit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A commit staging no code has nothing to build and nothing to run, and every
// stage past the law scan is about code: vet, lint, the compile-coverage
// check, the fail-first worktree and the suites all compile, and all of them
// queue for the machine-wide build slot. A docs-only commit must reach none of
// them.

// docsRepo commits a Go module and stages the given files, none of them code.
func docsRepo(t *testing.T, staged map[string]string) string {
	t.Helper()
	root := makeGoRepo(t)
	for rel, body := range staged {
		write(t, root, rel, body)
	}
	gitDo(t, root, "add", "-A")
	return root
}

func TestPrecommit_DocsOnlyCommitRunsNoStageThatCouldQueue(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := docsRepo(t, map[string]string{"README.md": "# notes\n", "docs/guide.md": "prose\n"})

	// Every stage that can take a build slot runs its command through the
	// runner, so "no runner started" IS the claim: nothing here can queue.
	if res := Precommit(root, refuseToRun(t)); res.Blocked {
		t.Fatalf("a Markdown-only commit must not be blocked: %s", res.Message)
	}

	log, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("no gate.log written: %v", err)
	}
	if !strings.Contains(string(log), "docs-only-fastpath") {
		t.Fatalf("the fast path must name itself in the log, got:\n%s", log)
	}
}

func TestMechanical_DocsOnlyMergeTakesTheFastPath(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := docsRepo(t, map[string]string{"CHANGELOG.md": "notes\n"})

	if res := Mechanical(root, refuseToRun(t)); res.Blocked {
		t.Fatalf("a docs-only merge must not be blocked: %s", res.Message)
	}
	log, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("no gate.log written: %v", err)
	}
	if !strings.Contains(string(log), "docs-only-fastpath") {
		t.Fatalf("the fast path must name itself in the log, got:\n%s", log)
	}
}

// The fast path is keyed on the file KINDS, so one staged source file takes
// the commit back onto the full wall however much prose rides with it.
func TestPrecommit_OneStagedSourceFileLeavesTheFastPath(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withLinter(t, false)
	root := docsRepo(t, map[string]string{
		"README.md": "# notes\n",
		"widget.go": "package m\n\nfunc Widget() int { return 1 }\n",
	})

	var ran []loggedRun
	if res := Mechanical(root, recordAllRuns(&ran, func(string) bool { return true })); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if len(ran) == 0 {
		t.Fatal("a staged source file must still reach the mechanical stage")
	}
}

func TestDocsOnly_ReadsTheStagedKindsNotTheExtensions(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := docsRepo(t, map[string]string{"README.md": "# notes\n"})
	if !docsOnly(root) {
		t.Fatal("a Markdown-only staged set is docs-only")
	}

	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	gitDo(t, root, "add", "-A")
	if docsOnly(root) {
		t.Fatal("a staged test file is code, so the commit is not docs-only")
	}
}
