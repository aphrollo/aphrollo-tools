package precommit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

const hookHelperEnv = "HOOKHELPER_INDEX"

// TestHookHelper_Precommit is the pre-commit hook of the tests below: a
// real `git commit` runs this test binary with the environment git gives a
// hook, and it runs the gate over the working directory. It does nothing in an
// ordinary test run.
func TestHookHelper_Precommit(t *testing.T) {
	index, ok := os.LookupEnv(hookHelperEnv)
	if !ok {
		t.Skip("runs only as the pre-commit hook of the commit tests below") // skip-ok: a helper process, not a test of its own.
	}
	// The test binary's own isolation drops every GIT_* variable at start, so
	// the hook script carried the index in a variable of its own.
	t.Setenv("GIT_INDEX_FILE", index)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	res := Precommit(root, RunSuite(precommitTestTimeout))
	if res.Blocked {
		os.Stderr.WriteString(res.Message + "\n")
		os.Exit(1)
	}
}

// laneWithPrecommitHook is a Go repo with a linked lane whose pre-commit hook
// runs the gate in this test binary.
func laneWithPrecommitHook(t *testing.T) (lane, hooks string) {
	t.Helper()
	_, lane = tddtest.GoPrimaryWithLane(t)
	hooks = t.TempDir()
	script := "#!/bin/sh\n" + hookHelperEnv + "=\"$GIT_INDEX_FILE\" exec '" + filepath.ToSlash(os.Args[0]) +
		"' -test.run='^TestHookHelper_Precommit$' -test.count=1\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, lane, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, lane, "add", ".")
	gitDo(t, lane, "commit", "-qm", "widget")
	return lane, hooks
}

func stagedWidgetChange(t *testing.T, lane string) {
	t.Helper()
	write(t, lane, "widget.go", "package m\n\nfunc Widget() int { return 2 }\n")
	write(t, lane, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidgetIsTwo(t *testing.T) {\n\tif Widget() != 2 {\n\t\tt.Fatal(Widget())\n\t}\n}\n")
}

func commitThroughTheGate(t *testing.T, lane, hooks string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=" + filepath.ToSlash(hooks), "commit", "-q", "-m", "Make the widget return two"}, args...)...)
	cmd.Dir = lane
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit through the gate: %v\n%s", err, out)
	}
}

func filesOfHead(t *testing.T, lane string) string {
	t.Helper()
	cmd := exec.Command("git", "show", "--name-only", "--format=", "HEAD")
	cmd.Dir = lane
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git show HEAD: %v", err)
	}
	return strings.Join(strings.Fields(string(out)), ",")
}

// The gate checks HEAD out into a worktree of its own. A plain commit's hook
// names the lane's index in GIT_INDEX_FILE, and that checkout used to inherit
// it, reset the index to HEAD and let git record an empty commit.
func TestPrecommitHook_APlainCommitInALaneCarriesTheStagedFiles(t *testing.T) {
	lane, hooks := laneWithPrecommitHook(t)
	stagedWidgetChange(t, lane)
	gitDo(t, lane, "add", ".")

	commitThroughTheGate(t, lane, hooks)

	if got := filesOfHead(t, lane); got != "widget.go,widget_test.go" {
		t.Fatalf("the commit carries %q, want widget.go,widget_test.go", got)
	}
}

// `git commit -a` builds a temporary index the hook must read, and must not
// hand to a checkout either.
func TestPrecommitHook_ACommitDashAInALaneCarriesTheStagedFiles(t *testing.T) {
	lane, hooks := laneWithPrecommitHook(t)
	stagedWidgetChange(t, lane)
	gitDo(t, lane, "add", "widget_test.go")

	commitThroughTheGate(t, lane, hooks, "-a")

	if got := filesOfHead(t, lane); got != "widget.go,widget_test.go" {
		t.Fatalf("the commit carries %q, want widget.go,widget_test.go", got)
	}
}
