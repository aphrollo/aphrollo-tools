package refactor

import (
	"errors"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/lsp"
)

// TestIsLoadingError_TreatsRenamesNoReferencesAtPositionAsNotReady: a rename
// sent before rust-analyzer has built its crate graph is answered with
// `-32602: No references found at position` (seen on Windows, where the
// content-modified answer never comes), yet a definition always references
// itself once loaded — so that exact rename phrasing is not-ready, while the
// bare "no references found" of an empty references result stays terminal.
func TestIsLoadingError_TreatsRenamesNoReferencesAtPositionAsNotReady(t *testing.T) {
	if !isLoadingError(errors.New("rename: rpc error -32602: No references found at position")) {
		t.Error("rename's not-ready answer must be retried")
	}
	if isLoadingError(errors.New("no references found")) {
		t.Error("an empty references result stays terminal")
	}
}

// TestEmptyEditIsNotReady_TurnsANullRenameAnswerIntoALoadingError: between
// its not-ready errors and its real edits rust-analyzer has a window where a
// rename answers `result: null`. A definition always references itself once
// loaded, so an empty edit is not-ready, retried within the same budget; a
// real edit and a real error pass through untouched.
func TestEmptyEditIsNotReady_TurnsANullRenameAnswerIntoALoadingError(t *testing.T) {
	_, err := emptyEditIsNotReady(lsp.WorkspaceEdit{}, nil)
	if !isLoadingError(err) {
		t.Errorf("empty edit must be a loading error, got %v", err)
	}
	real := lsp.WorkspaceEdit{Changes: map[lsp.DocumentURI][]lsp.TextEdit{"file:///a.rs": {{NewText: "x"}}}}
	if got, err := emptyEditIsNotReady(real, nil); err != nil || len(got.Changes) != 1 {
		t.Errorf("a real edit passes through, got %v %v", got, err)
	}
	if _, err := emptyEditIsNotReady(lsp.WorkspaceEdit{}, errors.New("symbol not found")); err == nil || err.Error() != "symbol not found" {
		t.Errorf("a real error passes through, got %v", err)
	}
}

func TestIsLoadingError(t *testing.T) {
	transient := []string{
		"request failed: -32801 content modified",
		"waiting for cargo metadata",
		"server is still loading the project",
		"rust-analyzer is loading",
		"content is outdated",
		"server is not ready",
	}
	for _, m := range transient {
		if !isLoadingError(errors.New(m)) {
			t.Errorf("isLoadingError(%q) = false, want true (transient)", m)
		}
	}

	terminal := []string{
		// A genuinely zero-reference symbol must NOT be treated as transient —
		// otherwise the retry budget burns the full 25s and then errors instead
		// of returning the (empty) result immediately.
		"no references found",
		// "loading" must not match as a bare substring: an unrelated failure that
		// merely mentions the word is a real error, not a not-ready signal.
		"failed loading workspace: permission denied",
		"symbol not found",
	}
	for _, m := range terminal {
		if isLoadingError(errors.New(m)) {
			t.Errorf("isLoadingError(%q) = true, want false (terminal)", m)
		}
	}

	if isLoadingError(nil) {
		t.Errorf("isLoadingError(nil) = true, want false")
	}
}
