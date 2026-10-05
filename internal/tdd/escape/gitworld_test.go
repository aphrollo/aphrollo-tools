package escape

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// gitworldAddLane makes a linked worktree of root on a new branch, the way a
// lane is made, and answers its path.
func gitworldAddLane(t *testing.T, root, branch string) string {
	t.Helper()
	lane := filepath.Join(t.TempDir(), "lane")
	if out, err := git(root, "worktree", "add", "-b", branch, lane); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	return lane
}

// A sibling lane's new branch is named by a different ref in every sighting and
// every lane of the repository reports it from its own root: one class, one
// record, however many lanes and branch names it comes through.
func TestNoteGitWorldEscape_OneClassFromTwoLanesWithDifferentBranchesIsOneRecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := makeGoRepo(t)
	laneA := gitworldAddLane(t, root, "lane/a")
	laneB := gitworldAddLane(t, root, "lane/b")

	NoteGitWorldEscape(laneA, "test-map build", "the branches changed:\n+refs/heads/feat/one", io.Discard)
	NoteGitWorldEscape(laneB, "test-map build", "the branches changed:\n+refs/heads/feat/two", io.Discard)

	if got := len(loadEscapes()); got != 1 {
		t.Errorf("recorded %d escapes, want 1: the same stage and the same changed part", got)
	}
}

// What changed is part of the class: a changed worktree registration is a
// different miss from a new branch.
func TestNoteGitWorldEscape_ADifferentChangedPartIsAnotherRecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := makeGoRepo(t)

	NoteGitWorldEscape(root, "test-map build", "the branches changed:\n+refs/heads/feat/one", io.Discard)
	NoteGitWorldEscape(root, "test-map build", "the worktree registrations changed:\n+/x/y", io.Discard)

	if got := len(loadEscapes()); got != 2 {
		t.Errorf("recorded %d escapes, want 2: one per changed part", got)
	}
}

// Evidence that names no changed part falls back to what it says, so two
// different unlabelled sightings stay two classes.
func TestGitworldChangedParts_FallsBackWhenNoPartIsNamed(t *testing.T) {
	if got := gitworldChangedParts("something odd happened", "fallback line"); got != "fallback line" {
		t.Errorf("parts = %q, want the fallback", got)
	}
	if got := gitworldChangedParts(" changed\n+refs/heads/x", "fallback line"); got != "fallback line" {
		t.Errorf("a line with no label before it counted: %q", got)
	}
	if got := gitworldChangedParts("the branches changed:\n+a\nthe tip of main changed:\n-b", "fallback"); got != "the branches,the tip of main" {
		t.Errorf("parts = %q, want both labels sorted", got)
	}
}

// A class opened under the key the escape was filed by before the repository
// key still stands for itself: the first sighting after an upgrade is not a
// second issue.
func TestNoteGitWorldEscape_AClassOpenedUnderTheLegacyKeyIsNotOpenedAgain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root := makeGoRepo(t)
	evidence := "the branches changed:\n+refs/heads/feat/old"
	sum := sha256.Sum256([]byte(normalizeRepoSpelling(root) + "\ngitworld:test-map build\nthe branches changed:"))
	legacy := EscapeRecord{
		ID: "old", Kind: EscapeKind, At: time.Now().UTC(), Issue: "https://example.test/1", Number: 1,
		Fingerprint: hex.EncodeToString(sum[:8]),
	}
	if err := appendEscape(legacy); err != nil {
		t.Fatal(err)
	}

	NoteGitWorldEscape(root, "test-map build", evidence, io.Discard)

	if got := len(loadEscapes()); got != 1 {
		t.Errorf("recorded %d escapes, want the one already open", got)
	}
}
