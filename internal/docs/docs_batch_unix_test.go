//go:build unix

package docs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
	"github.com/aphrollo/aphrollo-tools/internal/proc"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
)

// TestTrackedMarkdown_BatchesAPathListPastTheBudget is #960 for the docs
// check: a pathspec list whose ls-files line would pass the budget runs as
// several calls, each within it, and lists exactly what one call over every
// pathspec lists — once each and in index order, even where two batches'
// pathspecs (a glob and a path it matches) name the same file.
func TestTrackedMarkdown_BatchesAPathListPastTheBudget(t *testing.T) {
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
	// Written in reverse, so index order is not the order the list names them.
	paths := []string{"*.md"}
	for i := 149; i >= 0; i-- {
		p := fmt.Sprintf("dir/pathspec_batching_fixture_doc_number_%03d.md", i)
		if err := os.WriteFile(filepath.Join(repo, p), []byte("# doc\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	run("add", "dir")
	var want []string
	for f := range strings.SplitSeq(strings.TrimRight(run(append([]string{"ls-files", "-z", "--"}, paths...)...), "\x00"), "\x00") {
		want = append(want, f)
	}

	log := filepath.Join(t.TempDir(), "argv.log")
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := proc.WriteExecutable(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := TrackedMarkdown(repo, paths)
	if err != nil {
		t.Fatalf("TrackedMarkdown: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("batched listing differs from one call over every pathspec: %d vs %d files\nfirst got %q, first want %q", len(got), len(want), got[:min(3, len(got))], want[:min(3, len(want))])
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

// TestTrackedMarkdown_CarriesGitsRefusalIntoTheError pins that a root git
// refuses is an error naming git's own reason, never an empty listing.
func TestTrackedMarkdown_CarriesGitsRefusalIntoTheError(t *testing.T) {
	_, err := TrackedMarkdown(t.TempDir(), []string{"a.md"})
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("TrackedMarkdown outside a repo = %v, want git's refusal", err)
	}
}
