package ratchet

import (
	"path/filepath"
	"strings"
	"testing"
)

// A denial that names the offence and stops is a denial that sends the reader
// to open the law. Every hit line ends with the way through, so the fix is
// legible from the line that refused the write.
func TestFindingLineEndsWithTheLawsEscape(t *testing.T) {
	root := repoWithNanGuard(t)
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")

	res, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	lines := res.Lines()
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want one per hit", lines)
	}
	const want = "— escape: // nan-safe: <why> on the line or the line above"
	if !strings.HasSuffix(lines[0], want) {
		t.Fatalf("line %q does not end with %q", lines[0], want)
	}
}

// A law with no escape has exactly one way through, and saying nothing reads
// as "there must be a marker for this somewhere".
func TestFindingLineSaysLowerTheCodeWhenThereIsNoEscape(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "no-todo", `
name = "no-todo"
description = "no TODO markers"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	write(t, filepath.Join(root, "a.go"), "// TODO: later\n")

	res, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Lines()) != 1 || !strings.HasSuffix(res.Lines()[0], "— no escape: lower the code") {
		t.Fatalf("lines = %v", res.Lines())
	}
}

// A count-keyed law is not escaped, it is paid down, and the number it is
// paid down TO is the one thing the reader needs.
func TestFindingLineSaysSplitTheFileForACountedLaw(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "big-file", `
name = "big-file"
description = "modules stay small"
severity = "deny"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "line-count"
max = 600
`)
	write(t, filepath.Join(root, "crates", "a", "src", "big.rs"), strings.Repeat("let a = 1;\n", 601))

	res, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Lines()) != 1 || !strings.HasSuffix(res.Lines()[0], "— split the file; the ceiling is 600") {
		t.Fatalf("lines = %v", res.Lines())
	}
}

// The remedy is worth nothing if it is off the end of a wrapped line, so the
// offending source is what gets truncated, not the advice.
func TestFindingLineFitsInOneHundredSixtyCharacters(t *testing.T) {
	root := repoWithNanGuard(t)
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = "+strings.Repeat("very_long_identifier_", 20)+"y.clamp(0.0, 1.0);\n")

	res, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	line := res.Lines()[0]
	if len([]rune(line)) > 160 {
		t.Fatalf("line is %d chars, want <= 160:\n%s", len([]rune(line)), line)
	}
	if !strings.HasSuffix(line, "on the line or the line above") {
		t.Fatalf("truncation must never eat the remedy:\n%s", line)
	}
}

// An escape that must sit ON the trigger line says so: telling the author it
// may go above would be advice that does not work.
func TestFindingLineNamesAZeroLineEscapeWindowExactly(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "no-todo", `
name = "no-todo"
description = "no TODO markers"
severity = "deny"
escape = "// todo-ok:"
escape_lines = 0

[scope]
include = ["**/*.go"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	write(t, filepath.Join(root, "a.go"), "x := 1 // TODO: later\n")

	res, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Lines()) != 1 || !strings.HasSuffix(res.Lines()[0], "— escape: // todo-ok: <why> on the line") {
		t.Fatalf("lines = %v", res.Lines())
	}
}
