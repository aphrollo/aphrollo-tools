package tdd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func fakeGitShim(t *testing.T) (dir, marker string) { t.Helper(); return tddtest.FakeGitShim(t) }

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

// TestGitInDir_SkipsAQueueDirWhoseGitIsAnExe is the exe-shim half of the same
// rule. The queue dir's Windows shim is now a COPY of the aphrollo binary
// named git.exe, and a `git` lookup tries git.exe FIRST — so a resolver that
// only reads scripts hands back the shim and every gate git call re-enters
// aphrollo. The extensionless sh script beside it is the tell that names the
// whole directory a queue dir.
func TestGitInDir_SkipsAQueueDirWhoseGitIsAnExe(t *testing.T) {
	dir := t.TempDir()
	// An .exe that reads as an opaque binary: nothing in its bytes says
	// aphrollo, exactly like the real 20 MB copy at the front of a lookup.
	if err := os.WriteFile(filepath.Join(dir, "git.exe"), []byte{0x4d, 0x5a, 0x90, 0x00}, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\nexec \"/bin/aphrollo\" gate git \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitInDir(dir); got != "" {
		t.Fatalf("gitInDir(%s) = %q, want \"\" — that is the queue shim, not git", dir, got)
	}
}

// TestGitInDir_FindsARealGitBesideNoShim is the other half: an ordinary bin
// dir must still resolve, or the skip above would blind the resolver to every
// git on PATH.
func TestGitInDir_FindsARealGitBesideNoShim(t *testing.T) {
	dir := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name = "git.exe"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte{0x4d, 0x5a, 0x90, 0x00}, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitInDir(dir); got != path {
		t.Fatalf("gitInDir(%s) = %q, want %q", dir, got, path)
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
	_ = exec.Command(gitBinary(), "-C", root, "merge", "lane").Run()

	if got := mergeTipTree(root); got != want {
		t.Fatalf("mergeTipTree = %q, want the lane tip's tree %q", got, want)
	}
}

func gitValue(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return tddtest.GitValue(t, dir, args...)
}
