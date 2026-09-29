//go:build unix

package postedit

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
	"github.com/aphrollo/aphrollo-tools/internal/proc"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
)

// TestGitPathSet_BatchesAPathListPastTheBudget is #960 for the trunk-sync
// scope: the ls-tree and diff --name-only calls over every changed path run
// as several git calls, each within the budget, and answer the set one call
// over every path answers.
func TestGitPathSet_BatchesAPathListPastTheBudget(t *testing.T) {
	real, ok := gitx.GitBinaryOnPath()
	if !ok {
		t.Fatal("no git on PATH")
	}
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(real, append([]string{"-C", repo}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %s: %v", args[0], err)
		}
		return string(out)
	}
	run("init", "-q")
	if err := os.MkdirAll(filepath.Join(repo, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i := range 150 {
		p := fmt.Sprintf("dir/pathspec_batching_fixture_file_number_%03d.txt", i)
		if err := os.WriteFile(filepath.Join(repo, p), []byte("a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	run("add", "dir")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base")
	for _, p := range paths[:75] {
		if err := os.WriteFile(filepath.Join(repo, p), []byte("b\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	log := filepath.Join(t.TempDir(), "argv.log")
	wrapper := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := proc.WriteExecutable(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APHROLLO_REAL_GIT", wrapper)

	for _, prefix := range [][]string{
		{"ls-tree", "-r", "--name-only", "HEAD", "--"},
		{"diff", "--name-only", "--no-renames", "HEAD", "--"},
	} {
		want := map[string]bool{}
		for line := range strings.SplitSeq(strings.TrimSpace(run(append(prefix, paths...)...)), "\n") {
			want[line] = true
		}
		got, err := gitPathSet(repo, prefix, paths)
		if err != nil {
			t.Fatalf("gitPathSet %s: %v", prefix[0], err)
		}
		if !maps.Equal(got, want) {
			t.Errorf("gitPathSet %s: %d paths, want the %d one call over every path answers", prefix[0], len(got), len(want))
		}
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
