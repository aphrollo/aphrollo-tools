//go:build unix

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// probeBatchFixture is a repo with 150 tracked files whose paths together
// pass the argv budget, every one changed in the working copy, and a git
// wrapper that logs each call's arguments before running the real git.
func probeBatchFixture(t *testing.T) (repo, realGit, wrapper, log string, doomed []probeFile) {
	t.Helper()
	repo, realGit = newDiscardFixture(t)
	if err := os.MkdirAll(filepath.Join(repo, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 150 {
		rel := fmt.Sprintf("dir/pathspec_batching_fixture_file_number_%03d.txt", i)
		writeFixtureFile(t, repo, rel, []string{"orig"})
		doomed = append(doomed, probeFile{rel: rel, changed: true})
	}
	runFixtureGit(t, realGit, repo, "add", "dir")
	runFixtureGit(t, realGit, repo, "commit", "-qm", "base")
	for _, f := range doomed {
		writeFixtureFile(t, repo, f.rel, []string{"probe"})
	}
	log = filepath.Join(t.TempDir(), "argv.log")
	wrapper = filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexec '" + realGit + "' \"$@\"\n"
	if err := proc.WriteExecutable(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo, realGit, wrapper, log, doomed
}

func assertCallsWithinBudget(t *testing.T, log string) {
	t.Helper()
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

// TestProbeWriteBackup_BatchesAPathListPastTheBudget is #960 for the backup
// a discard writes first: the diff over every changed file runs as several
// git calls, each within the budget, and the backup carries the patch one
// call over every path prints.
func TestProbeWriteBackup_BatchesAPathListPastTheBudget(t *testing.T) {
	repo, realGit, wrapper, log, doomed := probeBatchFixture(t)
	args := []string{"--literal-pathspecs", "diff", "--binary", "--no-color", "--no-ext-diff", "--no-textconv", "HEAD", "--"}
	for _, f := range doomed {
		args = append(args, f.rel)
	}
	want := runFixtureGit(t, realGit, repo, args...)

	path := filepath.Join(t.TempDir(), "backup.patch")
	if err := probeWriteBackup(wrapper, repo, path, doomed); err != nil {
		t.Fatalf("probeWriteBackup: %v", err)
	}
	backup := readFixture(t, filepath.Dir(path), filepath.Base(path))
	at := strings.Index(backup, "diff --git")
	if at < 0 || backup[at:] != want {
		t.Fatalf("the backup's patch differs from one call over every path (%d vs %d bytes)", len(backup)-max(at, 0), len(want))
	}
	assertCallsWithinBudget(t, log)
}

// TestProbeRestore_ReadsItsPathsFromStdin is #960 for the discard itself:
// `git restore` reads the paths from stdin, so its line stays the same
// length whatever the list, and every file is back at HEAD.
func TestProbeRestore_ReadsItsPathsFromStdin(t *testing.T) {
	repo, _, wrapper, log, doomed := probeBatchFixture(t)

	if err := probeRestore(wrapper, repo, doomed); err != nil {
		t.Fatalf("probeRestore: %v", err)
	}
	for _, f := range doomed {
		if got := readFixture(t, repo, f.rel); got != "orig\n" {
			t.Fatalf("%s = %q, want it back at HEAD", f.rel, got)
		}
	}
	assertCallsWithinBudget(t, log)
}

// TestProbeRestore_ReportsAFailingRestore pins that a path git cannot
// restore (not in HEAD) is an error naming that path, never a silent
// success that leaves the probe arm on disk.
func TestProbeRestore_ReportsAFailingRestore(t *testing.T) {
	repo, realGit := newDiscardFixture(t)

	err := probeRestore(realGit, repo, []probeFile{{rel: "never-committed.txt", changed: true}})

	if err == nil || !strings.Contains(err.Error(), "git restore failed") || !strings.Contains(err.Error(), "never-committed.txt") {
		t.Fatalf("probeRestore = %v, want the restore's failure", err)
	}
}
