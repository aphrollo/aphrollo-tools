package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleSizeRepo builds a real git repo carrying one line-count law keyed by
// file (a Counted baseline, the same shape module_size uses), one file at
// its recorded ceiling, committed clean.
func moduleSizeRepo(t *testing.T) string {
	t.Helper()
	isolateGit(t)
	root := t.TempDir()
	gitInitRepo(t, root)
	writeFile(t, filepath.Join(root, ".ratchet", "laws", "module-size.toml"), `
name = "module-size"
description = "modules stay small"
severity = "deny"
baseline = ".ratchet/baselines/module-size.txt"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "line-count"
max = 5
`)
	writeFile(t, filepath.Join(root, "crates", "weather", "physics.rs"), strings.Repeat("let x = 1;\n", 6))
	writeFile(t, filepath.Join(root, ".ratchet", "baselines", "module-size.txt"), "crates/weather/physics.rs | 6\n")
	gitCommitAll(t, root, "seed")
	return root
}

// TestRatchetCheck_NotesADeletedBaselineRowBesideAnUnrelatedRegression
// reproduces #497 end to end: the aftermath of #496's third defect is a
// baseline row silently dropped on disk, never committed, indistinguishable
// from a genuine new regression once it is gone — here simulated directly by
// deleting the row from the working tree without touching HEAD, alongside an
// unrelated rename that genuinely does cross the SAME law's ceiling.
func TestRatchetCheck_NotesADeletedBaselineRowBesideAnUnrelatedRegression(t *testing.T) {
	root := moduleSizeRepo(t)

	if err := os.Rename(
		filepath.Join(root, "crates", "weather", "physics.rs"),
		filepath.Join(root, "crates", "weather", "moved.rs")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".ratchet", "baselines", "module-size.txt"), "")

	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "check", "--repo", root, "--no-cache"}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String() + errb.String()
	if !strings.Contains(got, "module-size: crates/weather/moved.rs") {
		t.Fatalf("the rename must still be reported as a regression:\n%s", got)
	}
	wantNote := ".ratchet/baselines/module-size.txt has 1 row deleted relative to HEAD that this run did not delete"
	if !strings.Contains(got, wantNote) {
		t.Errorf("output does not carry the baseline-history note:\n%s", got)
	}
	if !strings.Contains(got, "git diff .ratchet/baselines/module-size.txt") {
		t.Errorf("the note must name the command that distinguishes the cause:\n%s", got)
	}
	if got := readFile(t, filepath.Join(root, ".ratchet", "baselines", "module-size.txt")); got != "" {
		t.Errorf("a refusing run must never write a baseline: %q", got)
	}
}

// TestRatchetCheck_TrimmedBaselineProducesNoNote pins the scoping call from
// #497: a baseline lowered the sanctioned way (its ceiling dropped, the row's
// key untouched) alongside an unrelated regression must never carry the
// baseline-history note — that direction is the common case, and a note that
// fired on it would be noise within a week.
func TestRatchetCheck_TrimmedBaselineProducesNoNote(t *testing.T) {
	root := moduleSizeRepo(t)
	// A second law, unrelated, whose baseline this run regresses — module-size
	// itself is left alone so it stays a clean, lowered-in-a-PRIOR-commit
	// ceiling rather than part of the regression under test.
	writeFile(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), `
name = "nan-guard"
description = "A float clamp is not a NaN guard"
severity = "deny"
baseline = ".ratchet/baselines/nan-guard.txt"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "regex-absent"
pattern = "\\.clamp\\("
`)
	writeFile(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"), "")
	writeFile(t, filepath.Join(root, "crates", "b", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")
	gitCommitAll(t, root, "adopt nan-guard")

	// module-size's own ceiling tightens the sanctioned way: physics.rs
	// shrinks to 5 lines, at its new, lower recorded count — the key survives.
	writeFile(t, filepath.Join(root, "crates", "weather", "physics.rs"), strings.Repeat("let x = 1;\n", 5))
	writeFile(t, filepath.Join(root, ".ratchet", "baselines", "module-size.txt"), "crates/weather/physics.rs | 5\n")
	// nan-guard regresses: a second offending site appears.
	writeFile(t, filepath.Join(root, "crates", "b", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")

	var out, errb bytes.Buffer
	code := Run([]string{"ratchet", "check", "--repo", root, "--no-cache"}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String() + errb.String()
	if !strings.Contains(got, "nan-guard:") {
		t.Fatalf("the actual regression must still be reported:\n%s", got)
	}
	if strings.Contains(got, "row deleted relative to HEAD") {
		t.Errorf("a sanctioned tightening must never carry the baseline-history note:\n%s", got)
	}
}
