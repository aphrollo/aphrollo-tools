//go:build unix

package gitx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// TestGitStaged_BatchesAPathListPastTheBudget is #960 for the fail-first
// diff: a staged path list whose command line would pass the budget runs as
// several git calls, each within it, and the patch they add up to is the
// one a single call over every path prints.
func TestGitStaged_BatchesAPathListPastTheBudget(t *testing.T) {
	real, ok := GitBinaryOnPath()
	if !ok {
		t.Fatal("no git on PATH")
	}
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(real, append([]string{"-C", repo}, args...)...)
		cmd.Env = cleanGitEnv()
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v", args[0], err)
		}
		return string(out)
	}
	run("init", "-q")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "base")
	var paths []string
	for i := range 150 {
		p := fmt.Sprintf("dir/pathspec_batching_fixture_file_number_%03d.txt", i)
		if err := os.MkdirAll(filepath.Join(repo, "dir"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, p), []byte(fmt.Sprintf("line %d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	run("add", "dir")
	want := run(append([]string{"diff", "--cached", "--"}, paths...)...)

	log := filepath.Join(t.TempDir(), "argv.log")
	wrapper := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := proc.WriteExecutable(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(realGitEnv, wrapper)

	got, err := gitStaged(repo, paths)
	if err != nil {
		t.Fatalf("gitStaged: %v", err)
	}
	if got != want {
		t.Fatalf("batched patch differs from one call over every path (%d vs %d bytes)", len(got), len(want))
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(calls)), "\n") {
		if len(line) > argvbatch.Budget {
			t.Errorf("a git call is %d chars, past the %d-char budget: %.80s…", len(line), argvbatch.Budget, line)
		}
	}
}

// TestGitStaged_MergeCommitStagedSetOfTwoHundredLongPathsStaysWithinTheCmdBudget
// is #996's shape: a lane 200 long paths ahead of trunk merges trunk in and
// stops before the commit; the pre-merge-commit gate judges what the lane
// still adds to trunk, so those 200 paths are the staged set, and no git
// call reading them may pass what cmd.exe can run.
func TestGitStaged_MergeCommitStagedSetOfTwoHundredLongPathsStaysWithinTheCmdBudget(t *testing.T) {
	real, ok := GitBinaryOnPath()
	if !ok {
		t.Fatal("no git on PATH")
	}
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(real, append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Env = cleanGitEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", args[0], err, out)
		}
		return string(out)
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "base")
	run("checkout", "-q", "-b", "lane")
	for i := range 200 {
		p := fmt.Sprintf("packages/storefront-checkout/src/components/payment-methods/Widget%03d.test.tsx", i)
		if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, p), []byte(fmt.Sprintf("export const n = %d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "packages")
	run("commit", "-q", "-m", "lane adds 200 files")
	run("checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(repo, "trunk.txt"), []byte("trunk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "trunk.txt")
	run("commit", "-q", "-m", "trunk moves")
	run("checkout", "-q", "lane")
	run("merge", "--no-commit", "--no-ff", "main")

	log := filepath.Join(t.TempDir(), "argv.log")
	wrapper := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := proc.WriteExecutable(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(realGitEnv, wrapper)

	staged, err := stagedFilesErr(repo)
	if err != nil || len(staged) != 200 {
		t.Fatalf("staged set = %d files, err %v; want the merge's 200", len(staged), err)
	}
	if _, err := gitStaged(repo, staged); err != nil {
		t.Fatalf("gitStaged: %v", err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for line := range strings.SplitSeq(strings.TrimSpace(string(calls)), "\n") {
		if strings.Contains(line, "--cached --no-renames") {
			n++
			if len(line) > argvbatch.Budget {
				t.Errorf("a git call is %d chars, past the %d-char budget", len(line), argvbatch.Budget)
			}
		}
	}
	if n < 3 {
		t.Fatalf("%d diff calls for 200 paths of 80 chars, want the list split", n)
	}
}
