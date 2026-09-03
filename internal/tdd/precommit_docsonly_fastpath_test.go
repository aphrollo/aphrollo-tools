package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A commit staging no code has nothing to build and nothing to run, and every
// stage past the law scan is about code. Measured on the real tree, a
// Markdown-only commit's ratchet stage took 132.7s and a workflow-only one
// 418.9s while the same scan by hand takes about 2s over 1921 files: the cost
// was queueing for a build slot the scan never needed. Prose must never wait
// behind somebody else's compile.

// heldBuildSlot takes the machine-wide build slot for the whole test, so a
// stage that queues for it cannot finish.
func heldBuildSlot(t *testing.T) {
	t.Helper()
	withIsolatedBuildLock(t)
	t.Setenv(buildSlotsEnv, "1")
	target := filepath.Join(t.TempDir(), "target")
	_, release, ok := TryAcquireBuildSlot(target, "held by the test", target)
	if !ok {
		t.Fatal("setup: could not take the build slot")
	}
	t.Cleanup(release)
	// Everything that would queue must queue for the SAME slot, whatever
	// target dir it resolves.
	t.Setenv("CARGO_TARGET_DIR", target)
}

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

func TestPrecommit_DocsOnlyCommitCompletesWhileAnotherProcessHoldsTheBuildLock(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	heldBuildSlot(t)
	root := docsRepo(t, map[string]string{"README.md": "# notes\n", "docs/guide.md": "prose\n"})

	done := make(chan GateResult, 1)
	go func() { done <- Precommit(root, refuseToRun(t)) }()
	select {
	case res := <-done:
		if res.Blocked {
			t.Fatalf("a Markdown-only commit must not be blocked: %s", res.Message)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a docs-only commit queued for a build lock it does not need")
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
	heldBuildSlot(t)
	root := docsRepo(t, map[string]string{"CHANGELOG.md": "notes\n"})

	done := make(chan GateResult, 1)
	go func() { done <- Mechanical(root, refuseToRun(t)) }()
	select {
	case res := <-done:
		if res.Blocked {
			t.Fatalf("a docs-only merge must not be blocked: %s", res.Message)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a docs-only merge queued for a build lock it does not need")
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
	if res := Precommit(root, recordAllRuns(&ran, func(string) bool { return true })); res.Blocked {
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
