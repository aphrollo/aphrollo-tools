//go:build unix

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
	"github.com/aphrollo/aphrollo-tools/internal/proc"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
)

// TestGitAdd_ReadsItsPathsFromStdin is #960 for staging what a split
// wrote: `git add` reads the paths from stdin, so its line stays the same
// length whatever the list, and every path is staged.
func TestGitAdd_ReadsItsPathsFromStdin(t *testing.T) {
	real, ok := gitx.GitBinaryOnPath()
	if !ok {
		t.Fatal("no git on PATH")
	}
	repo := t.TempDir()
	if out, err := exec.Command(real, "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(repo, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i := range 150 {
		p := fmt.Sprintf("dir/pathspec_batching_fixture_file_number_%03d.go", i)
		if err := os.WriteFile(filepath.Join(repo, p), []byte("package dir\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	log := filepath.Join(t.TempDir(), "argv.log")
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := proc.WriteExecutable(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := gitAdd(repo, paths); err != nil {
		t.Fatalf("gitAdd: %v", err)
	}

	staged, err := exec.Command(real, "-C", repo, "diff", "--cached", "--name-only").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(staged)), strings.Join(paths, "\n"); got != want {
		t.Fatalf("staged %d paths, want all %d", len(strings.Fields(got)), len(paths))
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

// TestGitAdd_ReportsAFailingAdd pins that a path git cannot stage is an
// error, never a split that claims its files staged.
func TestGitAdd_ReportsAFailingAdd(t *testing.T) {
	real, ok := gitx.GitBinaryOnPath()
	if !ok {
		t.Fatal("no git on PATH")
	}
	repo := t.TempDir()
	if out, err := exec.Command(real, "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	if err := gitAdd(repo, []string{"does-not-exist.go"}); err == nil {
		t.Fatal("gitAdd of a missing path = nil, want git's refusal")
	}
}
