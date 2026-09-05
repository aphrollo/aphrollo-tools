package tdd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A queued job can sit behind the box-wide run lock long enough for its lane
// to be reset past the commit it was recorded against. Before this, the
// worktree add that followed failed with "Could not parse object" and was
// logged as a plain worktree failure — indistinguishable from a real fault in
// the runner, and the merge later refused for a receipt that reads as "you
// did not run mutants" rather than "your run was recorded against a commit
// you deleted" (issue #367).
func TestRunMutantsJob_LogsTipRewrittenInsteadOfWorktreeFailedWhenTheLaneMovesOn(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := optedInLane(t)
	j := laneJob(t, root)

	// The lane moves on: the commit the job was recorded against stops being
	// reachable from anything — including the worktree the job's own start
	// created, which is removed here so nothing keeps the object alive — and
	// gc reclaims it. The real sequence the issue's own session hit.
	gitDo(t, root, "worktree", "remove", "--force", j.Worktree)
	gitDo(t, root, "reset", "--hard", "HEAD~1")
	gitDo(t, root, "reflog", "expire", "--expire=now", "--all")
	gitDo(t, root, "gc", "--prune=now")

	var out bytes.Buffer
	if code := runMutantsJob(j, &out); code != 0 {
		t.Fatalf("runMutantsJob = %d, want 0 — a rewritten tip is not an error", code)
	}
	log := gateLogText(t, cfg)
	if !strings.Contains(log, "mutants-tip-rewritten:") {
		t.Fatalf("gate.log never recorded the rewritten tip:\n%s", log)
	}
	if strings.Contains(log, "mutants-worktree-failed") {
		t.Fatalf("a rewritten tip must not read as a worktree failure:\n%s", log)
	}
	if _, err := os.Stat(j.Worktree); err == nil {
		t.Fatalf("a dropped job must leave no partial worktree at %s", j.Worktree)
	}
}

// git itself failing to run — a missing binary, a broken queue shim, a gone
// working directory — is an infra fault, not evidence the tip was rewritten:
// mutantsTipResolvable's own git invocation never got the chance to RUN and
// report the object missing, so this must fall through to the ordinary
// mutants-worktree-failed path (a real error, exit 1) rather than being
// read, silently and successfully, as a clean drop (issue #367 review).
func TestRunMutantsJob_TreatsAGitFailureAsWorktreeFailedNotARewrittenTip(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := optedInLane(t)
	j := laneJob(t, root)

	// A path that cannot possibly be an executable: exec.Command given an
	// absolute path skips PATH lookup entirely, so this fails to START
	// rather than exiting non-zero — the *exec.Error/path-error shape a
	// missing binary or a broken queue shim also produces, never an
	// *exec.ExitError.
	t.Setenv(realGitEnv, filepath.Join(t.TempDir(), "no-such-git-binary"))

	var out bytes.Buffer
	if code := runMutantsJob(j, &out); code != 1 {
		t.Fatalf("runMutantsJob = %d, want 1 — git itself failing to run is an error, not a clean drop", code)
	}
	log := gateLogText(t, cfg)
	if !strings.Contains(log, "mutants-worktree-failed") {
		t.Fatalf("gate.log never recorded the worktree failure:\n%s", log)
	}
	if strings.Contains(log, "mutants-tip-rewritten") {
		t.Fatalf("a git failure must not read as a rewritten tip:\n%s", log)
	}
}
