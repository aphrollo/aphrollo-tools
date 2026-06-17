package refactor

import (
	"errors"
	"testing"
)

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
