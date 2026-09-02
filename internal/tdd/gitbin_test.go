package tdd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitShim writes a git that is NOT git: it records that it ran and fails.
// It carries the same tell the real queue shim does — it re-enters aphrollo —
// which is what marks a PATH entry as a shim dir rather than a git install.
func fakeGitShim(t *testing.T) (dir, marker string) {
	t.Helper()
	dir = t.TempDir()
	marker = filepath.Join(dir, "shim-ran.txt")
	cmd := "@echo off\r\nrem aphrollo git queue shim\r\necho %* > \"" + marker + "\"\r\nexit /b 128\r\n"
	if err := os.WriteFile(filepath.Join(dir, "git.cmd"), []byte(cmd), 0o755); err != nil {
		t.Fatal(err)
	}
	sh := "#!/bin/sh\n# aphrollo git queue shim\necho \"$@\" > \"" + marker + "\"\nexit 128\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, marker
}

// The gate's own git subprocesses must reach the REAL git. With the queue
// shim dir first on PATH a bare `git` resolves to git.cmd, whose cmd.exe
// wrapper silently mangles arguments — which is how `rev-parse
// MERGE_HEAD^{tree}` became `HEAD{tree}` and every merge was refused for
// having no lane tip.
func TestGit_RunsTheRealGitNotTheQueueShim(t *testing.T) {
	root := makeGoRepo(t)
	dir, marker := fakeGitShim(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("git through a shimmed PATH failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the gate ran the queue shim instead of git")
	}
	if len(strings.TrimSpace(out)) != 40 {
		t.Fatalf("rev-parse HEAD = %q, want a sha", out)
	}
}

// mergeTipTree is what the mutation receipt is keyed by, so an empty answer
// rejects every merge with "no lane tip to look one up by".
func TestMergeTipTree_NamesTheMergedTipsTree(t *testing.T) {
	root := makeGoRepo(t)
	base := gitValue(t, root, "rev-parse", "--abbrev-ref", "HEAD")

	gitDo(t, root, "checkout", "-q", "-b", "lane")
	write(t, root, "conflict.go", "package m\n\nconst V = \"lane\"\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane")
	want := gitValue(t, root, "rev-parse", "lane:")

	gitDo(t, root, "checkout", "-q", base)
	write(t, root, "conflict.go", "package m\n\nconst V = \"base\"\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base-side")
	// Conflicts on purpose: that is the state a merge gate runs in.
	_ = exec.Command("git", "-C", root, "merge", "lane").Run()

	if got := mergeTipTree(root); got != want {
		t.Fatalf("mergeTipTree = %q, want the lane tip's tree %q", got, want)
	}
}

// gitValue reads one git value in a test, without going through the helper
// under test.
func gitValue(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
