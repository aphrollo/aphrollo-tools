package escape

import (
	"io"
	"strings"
	"testing"
)

// A runner whose tests changed the real git state records an escape naming the
// runner, the repository and what changed.
func TestNoteGitWorldEscape_RecordsTheRunnerTheRepositoryAndWhatChanged(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// The fixture repo has no GitHub remote, which keeps the recorder from
	// opening an issue.
	root := makeGoRepo(t)

	NoteGitWorldEscape(root, "test-map build", "the repository's config changed\n+ user.name = t", io.Discard)

	recs := loadEscapes()
	if len(recs) != 1 {
		t.Fatalf("recorded %d escapes, want 1", len(recs))
	}
	r := recs[0]
	if r.Kind != EscapeKind {
		t.Errorf("kind = %q, want %q", r.Kind, EscapeKind)
	}
	for _, want := range []string{"test-map build", root} {
		if !strings.Contains(r.Reason, want) {
			t.Errorf("reason %q lacks %q", r.Reason, want)
		}
	}
	if !strings.Contains(r.Evidence, "user.name = t") {
		t.Errorf("evidence = %q, want what changed", r.Evidence)
	}
	if r.Check != "gitworld:test-map build" {
		t.Errorf("check = %q, want the stage that would have caught it", r.Check)
	}
}

// The same runner changing the same thing again inside the window is one record.
func TestNoteGitWorldEscape_ARepeatOfTheSameChangeIsOneRecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)

	NoteGitWorldEscape(root, "measurement", "the branches changed", io.Discard)
	NoteGitWorldEscape(root, "measurement", "the branches changed", io.Discard)
	NoteGitWorldEscape(root, "proof", "the branches changed", io.Discard)

	if got := len(loadEscapes()); got != 2 {
		t.Errorf("recorded %d escapes, want 2: one per runner", got)
	}
}
