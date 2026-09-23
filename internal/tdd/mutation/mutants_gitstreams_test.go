package mutation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// warningOnStderr is what a box with core.autocrlf on prints the first time
// git looks at a file whose worktree bytes are LF — advice, on stderr, about
// a file nothing changed.
const warningOnStderr = "warning: in the working copy of 'crates/a/src/lib.rs', " +
	"LF will be replaced by CRLF the next time Git touches it\n"

// stubGitDiff routes the calls a test wants to speak for and hands every
// other one to the real git, so a fixture stays a fixture: a stub that
// answered EVERY git call would have to reimplement the ones the test is not
// about.
func stubGitDiff(t *testing.T, reply func(args []string) (stdout, stderr string, err error, handled bool)) {
	t.Helper()
	t.Cleanup(setGitDiffOutForTest(func(dir string, args ...string) (string, string, error) {
		if stdout, stderr, err, handled := reply(args); handled {
			return stdout, stderr, err
		}
		return gitDiffOut(dir, args...)
	}))
}

// git that could not answer is not a tree that did not change. The snapshot
// swallowed the failure and reported "not ok", which the guard read as
// nothing to compare and let the merge through: a broken git — a corrupt
// index, a repo that vanished under the run, a killed process — would have
// waved through exactly the tree this stage exists to check. It refuses, with
// git's own words in the message.
func TestMeasure_GitFailureRefusesNotPasses(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	const said = "fatal: not a git repository"
	stubGitDiff(t, func(args []string) (string, string, error, bool) {
		if len(args) == 1 && args[0] == "diff" {
			return "", said + "\n", errors.New("exit status 128"), true
		}
		return "", "", nil, false
	})
	stubMutantsExec(t, func(context.Context, int, measuredCall) (int, error) {
		writeOutcomes(t, root, MutantOutcome{File: "crates/a/src/lib.rs", Line: 1, Col: 36,
			Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if !v.Refused {
		t.Fatalf("verdict = %+v, want a refusal: git could not read the tree, so nothing was compared", v)
	}
	if !strings.Contains(v.Message, said) {
		t.Errorf("message = %q, want git's own words in it", v.Message)
	}
}

// The changed-path list is a verdict too: it decides what is measured at all.
// Read from combined output, a warning line becomes a path, and a path that
// is really a sentence about line endings is one more file the scope claims
// to cover. Stdout alone answers.
func TestMeasure_ChangedPathsIgnoreGitWarnings(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	stubGitDiff(t, func(args []string) (string, string, error, bool) {
		if len(args) > 1 && args[1] == "--name-only" {
			return "crates/a/src/other.rs\n", warningOnStderr, nil, true
		}
		return "", "", nil, false
	})

	paths, ok := worktreeChangedPaths(root, base)

	if !ok {
		t.Fatal("worktreeChangedPaths said git could not answer, and it answered")
	}
	if len(paths) != 1 || paths[0] != "crates/a/src/other.rs" {
		t.Fatalf("paths = %q, want only what git printed on stdout", paths)
	}
}

// The diff FILE is handed to cargo-mutants as --in-diff, which parses it. A
// warning line lands ahead of the first `diff --git`, where a patch parser
// has no place to put it: the same autocrlf shape, one step further along.
// What is written is what git printed on stdout, byte for byte.
func TestMeasure_InDiffFileStartsWithDiffGitNotAWarning(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	const patch = "diff --git a/crates/a/src/other.rs b/crates/a/src/other.rs\n" +
		"@@ -1 +1 @@\n-a + b\n+a - b\n"
	stubGitDiff(t, func(args []string) (string, string, error, bool) {
		if len(args) > 1 && args[1] == base {
			return patch, warningOnStderr, nil, true
		}
		return "", "", nil, false
	})

	path, err := writeMeasureDiff(root, base, []string{"crates/a/src/lib.rs"})

	if err != nil {
		t.Fatalf("writeMeasureDiff: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no diff written for the runner: %v", err)
	}
	if string(data) != patch {
		t.Fatalf("changed.diff =\n%s\nwant exactly what git printed on stdout:\n%s", data, patch)
	}
	if first, _, _ := strings.Cut(string(data), "\n"); !strings.HasPrefix(first, "diff --git") {
		t.Errorf("changed.diff starts with %q, want the first line of a patch", first)
	}
	if filepath.Base(path) != "changed.diff" {
		t.Errorf("diff written to %q, want the run's own changed.diff", path)
	}
}
