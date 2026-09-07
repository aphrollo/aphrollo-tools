package tdd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The exact shape issue #519 reported: a --old pattern that never matches the
// bytes on disk (stale CRLF, a typo, the wrong worktree) must never reach the
// suite at all — running it and reading "still green" as a survivor is the
// false positive the issue is about. Refusing BEFORE the run is what makes
// the fake runner below provably uncalled, not just cheap.
func TestRunMutantsProve_RefusesWhenTheOldPatternNeverMatches(t *testing.T) {
	root := makeGoRepo(t)
	src := "package m\n\nfunc Add(a, b int) int { return a + b }\n"
	write(t, root, "widget.go", src)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")

	called := false
	fakeRun := func(r Runner, root string) SuiteResult {
		called = true
		return SuiteResult{Passed: true}
	}

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     filepath.Join(root, "widget.go"),
		Old:      "return a * b", // not present — the real source says "a + b"
		New:      "return a - b",
		WantFail: "TestAdd",
	}, fakeRun, &out, &errb)

	if code != ExitMutantsProveRefused {
		t.Fatalf("exit = %d, want ExitMutantsProveRefused (%d)\nstdout: %s\nstderr: %s",
			code, ExitMutantsProveRefused, out.String(), errb.String())
	}
	if called {
		t.Fatal("the suite ran despite the mutation pattern matching nothing — exactly the false-survivor path issue #519 reports")
	}
	got, err := os.ReadFile(filepath.Join(root, "widget.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != src {
		t.Fatalf("source file changed despite a refused mutation: %q", got)
	}
	if !strings.Contains(errb.String(), "0 matches") {
		t.Fatalf("stderr never says the pattern matched zero times: %q", errb.String())
	}
}

// The positive case: a mutation that DOES register in git diff --numstat and
// DOES fail the predicted test is reported as killed, and the file is
// restored byte-identically afterward.
func TestRunMutantsProve_ReportsKilledAndRestoresTheFileWhenTheNamedTestFails(t *testing.T) {
	root := makeGoRepo(t)
	src := "package m\n\nfunc Add(a, b int) int { return a + b }\n"
	write(t, root, "widget.go", src)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")

	var out, errb bytes.Buffer
	code := RunMutantsProve(MutantsProveOptions{
		File:     filepath.Join(root, "widget.go"),
		Old:      "return a + b",
		New:      "return a - b",
		WantFail: "TestAdd",
	}, RunSuite(precommitTestTimeout), &out, &errb)

	if code != ExitMutantsProveKilled {
		t.Fatalf("exit = %d, want ExitMutantsProveKilled (%d)\nstdout: %s\nstderr: %s",
			code, ExitMutantsProveKilled, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "TestAdd") {
		t.Fatalf("report never names the killed test: %q", out.String())
	}
	got, err := os.ReadFile(filepath.Join(root, "widget.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != src {
		t.Fatalf("file was not restored byte-identically after the proof: %q", got)
	}
}

// The exact conflation issue #529 reports: `gitErr != nil ||
// strings.TrimSpace(numstat) == ""` used to report a relPath that resolves
// outside repoRoot with the SAME "reports no change" message as a file that
// genuinely did not change — a caller (or a human reading the refusal) cannot
// tell "your mutation did not apply" from "this path is not even in this
// repository" apart. classifyMutationDiff must pick the escape case first,
// regardless of what git itself said.
func TestClassifyMutationDiff_ReportsEscapesRepoNotNoChange(t *testing.T) {
	got := classifyMutationDiff(true, nil, "")
	if got != mutationDiffEscapesRepo {
		t.Fatalf("classifyMutationDiff(escapes=true, nil, \"\") = %v, want mutationDiffEscapesRepo (%v)",
			got, mutationDiffEscapesRepo)
	}
	// Even when git itself reports a change, an escaping relPath still wins:
	// numstat for a pathspec outside the repo is not evidence about the file
	// under test at all.
	if got := classifyMutationDiff(true, nil, "1\t1\twidget.go"); got != mutationDiffEscapesRepo {
		t.Fatalf("classifyMutationDiff(escapes=true, nil, non-empty numstat) = %v, want mutationDiffEscapesRepo (%v)",
			got, mutationDiffEscapesRepo)
	}
}

// The second half of the same conflation: a git invocation failure (the
// binary crashed, the pathspec was malformed, whatever) must be reported as
// a git failure carrying its own error, never folded into "no change" — the
// two causes call for different fixes and a caller told the wrong one cannot
// act on it.
func TestClassifyMutationDiff_ReportsGitFailureNotNoChange(t *testing.T) {
	gitErr := errors.New("exit status 128")
	got := classifyMutationDiff(false, gitErr, "")
	if got != mutationDiffGitFailed {
		t.Fatalf("classifyMutationDiff(false, %v, \"\") = %v, want mutationDiffGitFailed (%v)",
			gitErr, got, mutationDiffGitFailed)
	}
}

// The remaining, narrower case the old combined check was actually right
// about: git ran fine, inside the repo, and reported nothing to diff.
func TestClassifyMutationDiff_ReportsNoChangeOnlyWhenGitRanCleanAndEmpty(t *testing.T) {
	got := classifyMutationDiff(false, nil, "   \n")
	if got != mutationDiffNoChange {
		t.Fatalf("classifyMutationDiff(false, nil, whitespace) = %v, want mutationDiffNoChange (%v)",
			got, mutationDiffNoChange)
	}
	if got := classifyMutationDiff(false, nil, "1\t1\twidget.go"); got != mutationDiffChanged {
		t.Fatalf("classifyMutationDiff(false, nil, real numstat) = %v, want mutationDiffChanged (%v)",
			got, mutationDiffChanged)
	}
}

// The 8.3 short-name mechanism itself (repoRoot() and t.TempDir() can spell
// the same CI temp directory two different ways) is Windows-only and needs
// GetShortPathName, which internal/refactor/tempdir_windows_test.go already
// probes directly. A symlink is the portable stand-in for "two spellings,
// one location": repoRoot is named through an alias, absFile through the
// real directory it points at — the same shape as the CI bug, generalised
// past one platform's short-name mechanism. Drop the EvalSymlinks calls from
// repoRelSlashPath and this fails: rel becomes "../real/widget.go" (or
// similar), which escapes repoRoot despite naming a file inside it.
func TestRepoRelSlashPath_AgreesWhenRepoRootIsSpelledThroughASymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	absFile := filepath.Join(real, "widget.go")
	if err := os.WriteFile(absFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("cannot create a symlink on this platform/permission set: %v", err) // skip-ok: environment probe, not a disabled assertion
	}

	rel, ok := repoRelSlashPath(alias, absFile)
	if !ok {
		t.Fatalf("repoRelSlashPath(%q, %q) ok = false, want true", alias, absFile)
	}
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		t.Fatalf("repoRelSlashPath(%q, %q) = %q, escapes repoRoot despite absFile living inside it via a symlink",
			alias, absFile, rel)
	}
	if rel != "widget.go" {
		t.Fatalf("repoRelSlashPath(%q, %q) = %q, want %q", alias, absFile, rel, "widget.go")
	}
}
