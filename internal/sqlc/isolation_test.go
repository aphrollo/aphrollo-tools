package sqlc

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// This package's fixtures build real git repos in t.TempDir() and commit in
// them. On a box where the gate is installed, `git commit` runs the OPERATOR's
// own core.hooksPath: the gate fires against a throwaway tree and appends its
// verdict to the real ~/.claude/gate-state/gate.log. 944 lines of this
// package's temp-dir paths were in that log when the demotion trend was first
// read off it — TestGitShow_*, TestRegenScopedAppliesOnlyChangedQuery, one as
// recent as the same morning — and a trend that has to tell real refusals from
// fixture noise cannot do it once the fixtures are in the record. It is also
// minutes of a suite spent running a gate nobody asked for.
func TestFixtureGit_RunsNoneOfThisBoxsInstalledHooks(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")

	// --get exits 1 when the key is set nowhere, which is the answer this
	// test wants; only the VALUE decides the verdict.
	out, _ := exec.Command("git", "-C", repo, "config", "--get", "core.hooksPath").Output()
	got := strings.TrimSpace(string(out))
	// The isolation points it at an empty directory, which runs no hook at all.
	if entries, err := os.ReadDir(got); got != "" && (err != nil || len(entries) != 0) {
		t.Fatalf("a fixture repo resolves core.hooksPath = %q, so every fixture commit runs the box's real gate and writes its verdict to the real gate.log", got)
	}
}
