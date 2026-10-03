package lawgate

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// `git commit -a` and `git commit <paths>` build a temporary index and run the
// pre-commit hook with GIT_INDEX_FILE naming it; the default index is stale and
// locked for the length of the commit. A stage that reads the staged tree from
// the default index judges the wrong tree, or cannot read it at all.

// The helper runs inside the hook, in a copy of this test binary the hook script
// starts: the isolation every test binary opens with drops GIT_INDEX_FILE, so the
// script hands it over under another name.
const (
	hookResultEnv = "HOOKHELPER_RESULT"
	hookIndexEnv  = "HOOKHELPER_INDEX"
	hookRootEnv   = "HOOKHELPER_ROOT"
)

func TestHookHelper_CommitRatchetStage(t *testing.T) {
	result := os.Getenv(hookResultEnv)
	if result == "" {
		t.Skip("runs only as the pre-commit hook of TestCommitRatchetStage_JudgesWhatCommitAStages") // skip-ok: a helper process, not a test of its own.
	}
	t.Setenv("GIT_INDEX_FILE", os.Getenv(hookIndexEnv))
	res := commitRatchetStage(os.Getenv(hookRootEnv))
	text := "blocked=false\n"
	if res.Blocked {
		text = "blocked=true\n" + res.Message
	}
	if err := os.WriteFile(result, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCommitRatchetStage_JudgesWhatCommitAStages(t *testing.T) {
	root := goGraphTree(t)
	// Unstaged on purpose: `git commit -a` is what stages it.
	mustWrite(t, filepath.Join(root, "go.mod"), goGraphMod("./dep2"))

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hooks := t.TempDir()
	script := "#!/bin/sh\n" + hookIndexEnv + "=\"$GIT_INDEX_FILE\" exec '" + filepath.ToSlash(exe) +
		"' -test.run '^TestHookHelper_CommitRatchetStage$' -test.count=1 >/dev/null 2>&1\n"
	hook := filepath.Join(hooks, "pre-commit")
	mustWrite(t, hook, script)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(hook, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	result := filepath.Join(t.TempDir(), "result.txt")

	cmd := exec.Command("git", "-C", root, "-c", "core.hooksPath="+filepath.ToSlash(hooks), "commit", "-a", "-q", "-m", "stage the replace")
	cmd.Env = append(os.Environ(), hookResultEnv+"="+result, hookRootEnv+"="+root)
	cmd.Stdin = nil
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("git commit: %v\n%s", err, out) // the hook exits 0, so a failure here is git's own
	}

	got, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("the hook helper left no verdict: %v", err)
	}
	if !strings.HasPrefix(string(got), "blocked=true") || !strings.Contains(string(got), "example.com/dep/bad") {
		t.Fatalf("the commit stages a go.mod that reaches a forbidden package and must be rejected for it, got:\n%s", got)
	}
}
