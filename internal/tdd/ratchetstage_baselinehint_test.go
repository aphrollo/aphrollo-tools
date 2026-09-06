package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleSizeStageTree is a git repo carrying one line-count law keyed by
// file (a Counted baseline, the same shape module_size uses), one file at
// its recorded ceiling, committed clean.
func moduleSizeStageTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "module-size.toml"), `
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
	mustWrite(t, filepath.Join(root, "crates", "weather", "physics.rs"), strings.Repeat("let x = 1;\n", 6))
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "module-size.txt"), "crates/weather/physics.rs | 6\n")
	gitAddAll(t, root)
	commitAll(t, root)
	return root
}

// TestRatchetStage_NotesADeletedBaselineRowBesideAnUnrelatedRegression is
// #497's end-to-end proof at the actual commit-gate layer (ratchetStage,
// the git-hook-nested caller cleanGitEnv exists for): the aftermath of
// #496's third defect is a baseline row dropped on disk, never committed —
// simulated directly here — beside a genuine, unrelated rename that crosses
// the same law's ceiling.
func TestRatchetStage_NotesADeletedBaselineRowBesideAnUnrelatedRegression(t *testing.T) {
	root := moduleSizeStageTree(t)

	if err := os.Rename(
		filepath.Join(root, "crates", "weather", "physics.rs"),
		filepath.Join(root, "crates", "weather", "moved.rs")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "module-size.txt"), "")
	gitAddAll(t, root)

	var res GateResult
	stderr := captureStderr(t, func() {
		res = ratchetStage("precommit", root)
	})
	if !res.Blocked {
		t.Fatalf("a new file over the ceiling must reject the commit: %s", res.Message)
	}
	if !strings.Contains(res.Message, "module-size") || !strings.Contains(res.Message, "moved.rs") {
		t.Errorf("message %q does not name the regression", res.Message)
	}
	wantNote := ".ratchet/baselines/module-size.txt has 1 row deleted relative to HEAD that this run did not delete"
	if !strings.Contains(stderr, wantNote) {
		t.Errorf("stderr does not carry the baseline-history note:\n%s", stderr)
	}
	if !strings.Contains(stderr, "git diff .ratchet/baselines/module-size.txt") {
		t.Errorf("the note must name the command that distinguishes the cause:\n%s", stderr)
	}
}

// TestRatchetStage_TrimmedBaselineProducesNoNote pins the scoping call from
// #497 at the commit-gate layer: a baseline lowered the sanctioned way,
// beside an unrelated regression, must never carry the baseline-history
// note.
func TestRatchetStage_TrimmedBaselineProducesNoNote(t *testing.T) {
	root := moduleSizeStageTree(t)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), `
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
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"), "")
	mustWrite(t, filepath.Join(root, "crates", "b", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)
	commitAll(t, root)

	// module-size's own ceiling tightens the sanctioned way: the key
	// survives, only its recorded count drops.
	mustWrite(t, filepath.Join(root, "crates", "weather", "physics.rs"), strings.Repeat("let x = 1;\n", 5))
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "module-size.txt"), "crates/weather/physics.rs | 5\n")
	// nan-guard regresses: a second offending site appears.
	mustWrite(t, filepath.Join(root, "crates", "b", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)

	var res GateResult
	stderr := captureStderr(t, func() {
		res = ratchetStage("precommit", root)
	})
	if !res.Blocked {
		t.Fatalf("the actual regression must still reject the commit: %s", res.Message)
	}
	if !strings.Contains(res.Message, "nan-guard") {
		t.Errorf("message %q does not name the regression", res.Message)
	}
	if strings.Contains(stderr, "row deleted relative to HEAD") {
		t.Errorf("a sanctioned tightening must never carry the baseline-history note:\n%s", stderr)
	}
}
