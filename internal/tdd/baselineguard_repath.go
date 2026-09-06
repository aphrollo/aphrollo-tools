package tdd

import "github.com/aphrollo/aphrollo-tools/internal/ratchet"

// countedForm reports whether text parses as a Counted baseline (`<key> |
// <count>`, one line per key) — the same first-try test baselineCounts
// itself uses to tell the two shapes apart. Only a count-keyed baseline's
// identity IS the file path, so only there can a row that vanished at one
// path and reappeared at another be read as a re-path rather than a new key;
// a line-keyed (MultisetByText) baseline already drops the path from its
// identity and needs none of this.
func countedForm(text string) bool {
	_, err := ratchet.ParseBaseline(text, ratchet.Counted)
	return err == nil
}

// repathCountedKeys pairs a key that disappeared between old and now with one
// that appeared, when both carry the SAME count and the SAME git blob
// content, and rewrites old in place so the raise scan in raisedKeys reads
// the pair as unchanged rather than a brand-new key over the ceiling.
//
// The pairing RULE — exact count match, then a content comparison — is
// ratchet.RepathCountedKeys, shared with `ratchet check`'s own tighten path
// (#490 asked for one mechanism, not two copies). Reading the content stays
// local to this package: every git subprocess a hook spawns goes through
// gitBlob's scrubbed environment, because a nested git call inheriting the
// hook's own GIT_DIR/GIT_INDEX_FILE would run against the wrong repo state —
// a hazard ratchet's own git plumbing, run outside a hook, does not share.
func repathCountedKeys(repoRoot, base string, old, now map[string]int) {
	pairs := ratchet.RepathCountedKeys(old, now,
		func(rel string) (string, bool) { return gitBlob(repoRoot, base+":"+rel) },
		// ":" + rel is git's own bare syntax for the STAGED INDEX — the
		// pre-commit view of every added key.
		func(rel string) (string, bool) { return gitBlob(repoRoot, ":"+rel) },
	)
	for _, newKey := range pairs {
		old[newKey] = now[newKey]
	}
}
