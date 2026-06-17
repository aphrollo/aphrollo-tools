package refactor

import (
	"context"
	"strings"
	"time"
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
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
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
