package refactor

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/lsp"
)

// readyRetryBudget bounds how long we retry position-based requests while a
// language server is still loading its project. rust-analyzer in particular
// answers requests with errors until `cargo metadata` finishes; gopls and the
// others are usually ready immediately.
const readyRetryBudget = 25 * time.Second

const readyRetryInterval = 250 * time.Millisecond

// isLoadingError reports whether err looks like a transient "server not ready
// yet" response rather than a genuine failure. Matching is on the server's
// message/code text so it stays server-agnostic.
func isLoadingError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// Match SPECIFIC not-ready signals only. "no references found" is omitted on
	// purpose: it is the legitimate empty result for a zero-reference symbol, so
	// retrying it would burn the whole 25s budget and then error instead of
	// returning empty. Bare "loading" is omitted too — it would match unrelated
	// failures that merely mention the word ("failed loading workspace: …"); the
	// real not-ready phrasings ("still loading", "is loading") are kept.
	for _, s := range []string{
		"-32801",                     // ContentModified
		"content modified",           //
		"waiting for cargo metadata", //
		"cargo metadata",             //
		"still loading",              //
		"is loading",                 //
		"not yet ready",              //
		"server is not ready",        //
		"content is outdated",        //
		// rust-analyzer's rename answer before the crate graph is built (seen
		// on Windows, where no content-modified answer precedes it); a
		// definition always references itself once loaded, so for a RENAME
		// this exact phrasing is not-ready. The bare "no references found"
		// above stays terminal.
		"no references found at position",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// errEmptyEditWhileLoading is the rename answer `result: null` read as a
// not-ready signal: rust-analyzer has a window, after its "no references
// found at position" errors and before its real edits, where a rename
// answers with no edit at all. A definition always references itself once
// the crate graph is built, so an empty edit is retried within the budget.
var errEmptyEditWhileLoading = errors.New("server answered an empty edit: still loading")

// emptyEditIsNotReady wraps a rename answer so an empty edit retries like a
// loading error; a real edit and a real error pass through untouched.
func emptyEditIsNotReady(we lsp.WorkspaceEdit, err error) (lsp.WorkspaceEdit, error) {
	if err == nil && len(we.Changes) == 0 && len(we.DocumentChanges) == 0 {
		return we, errEmptyEditWhileLoading
	}
	return we, err
}

// retryWhileLoading runs fn, retrying on transient loading errors until the
// budget elapses or ctx is cancelled. On exhaustion it returns the last server
// error (more informative than a bare deadline).
func retryWhileLoading[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	budget, cancel := context.WithTimeout(ctx, readyRetryBudget)
	defer cancel()

	res, err := fn()
	for isLoadingError(err) {
		select {
		case <-budget.Done():
			return res, err
		case <-time.After(readyRetryInterval):
		}
		res, err = fn()
	}
	return res, err
}
