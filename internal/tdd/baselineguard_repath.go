package tdd

import (
	"os/exec"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

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
// The pairing RULE — exact count match, then a batched content comparison —
// is ratchet.RepathCountedKeys, shared with `ratchet check`'s own tighten
// path (#490 asked for one mechanism, not two copies; #489 is the batching
// this buys, one `git cat-file --batch` process for every removed key and
// one for every added key, rather than up to two `git show` calls per
// candidate PAIR). What stays local to this package is HOW the content is
// read: every git subprocess a hook spawns goes through cleanGitEnv
// (gitBatchBlobs below), because a nested git call inheriting the hook's own
// GIT_DIR/GIT_INDEX_FILE would run against the wrong repo state — a hazard
// ratchet's own git plumbing, run outside a hook, does not share.
func repathCountedKeys(repoRoot, base string, old, now map[string]int) {
	pairs := ratchet.RepathCountedKeys(old, now,
		func(rels []string) map[string]string { return gitBatchBlobs(repoRoot, base, rels) },
		// "" is git's own bare `:path` syntax for the STAGED INDEX — the
		// pre-commit view of every added key.
		func(rels []string) map[string]string { return gitBatchBlobs(repoRoot, "", rels) },
	)
	for _, newKey := range pairs {
		old[newKey] = now[newKey]
	}
}

// gitBatchBlobs reads every rel's blob content at ref (ref == "" reads the
// staged index) in ONE `git cat-file --batch` process, through this
// package's scrubbed-environment plumbing — rather than one `git show` per
// path, which is what made #489 O(N^2) in subprocess spawns. Any failure
// (git missing, a malformed batch stream) answers an empty map: every
// candidate then reads as "content unknown", which RepathCountedKeys treats
// as never eligible to pair — a hook that cannot run git must refuse a
// laundered raise, never wave one through.
func gitBatchBlobs(repoRoot, ref string, rels []string) map[string]string {
	if len(rels) == 0 {
		return map[string]string{}
	}
	var stdin strings.Builder
	for _, rel := range rels {
		stdin.WriteString(ref + ":" + rel + "\n")
	}
	cmd := exec.Command(gitBinary(), "-C", repoRoot, "cat-file", "--batch")
	cmd.Env = cleanGitEnv()
	cmd.Stdin = strings.NewReader(stdin.String())
	out, err := cmd.Output() // stderr-ok: a failed batch read reads as "content unknown" for every candidate below, never surfaced
	if err != nil {
		return map[string]string{} // absence-ok: a failed batch read reads as "content unknown" for every candidate, never eligible to pair
	}
	blobs, err := ratchet.ParseCatFileBatch(out, rels)
	if err != nil {
		return map[string]string{} // absence-ok: a malformed batch stream reads as "content unknown" for every candidate, never eligible to pair
	}
	result := make(map[string]string, len(blobs))
	for k, v := range blobs {
		result[k] = string(v)
	}
	return result
}
