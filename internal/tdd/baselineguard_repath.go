package tdd

import (
	"sort"

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
// Pairing is by CONTENT, never by count alone: two different files landing
// at the same line count must never swap identities, and a count that also
// rose is never eligible in the first place, since the counts must match
// exactly for a pair to be considered at all.
func repathCountedKeys(repoRoot, base string, old, now map[string]int) {
	var removed []string
	for k := range old {
		if _, ok := now[k]; !ok {
			removed = append(removed, k)
		}
	}
	if len(removed) == 0 {
		return
	}
	sort.Strings(removed)

	var added []string
	for k := range now {
		if _, ok := old[k]; !ok {
			added = append(added, k)
		}
	}
	sort.Strings(added)

	paired := make(map[string]bool, len(removed))
	for _, addedKey := range added {
		for _, removedKey := range removed {
			if paired[removedKey] {
				continue
			}
			if old[removedKey] != now[addedKey] {
				continue
			}
			if !sameStagedBlob(repoRoot, base, removedKey, addedKey) {
				continue
			}
			old[addedKey] = now[addedKey]
			paired[removedKey] = true
			break
		}
	}
}

// sameStagedBlob compares the git blob of oldRel at the base ref against the
// blob of newRel in the staged index — content identity, not merely an
// unchanged line count, so a rename that also edited the file still reads as
// a new key over the ceiling.
func sameStagedBlob(repoRoot, base, oldRel, newRel string) bool {
	before, ok := gitBlob(repoRoot, base+":"+oldRel)
	if !ok {
		return false
	}
	after, ok := gitBlob(repoRoot, ":"+newRel)
	if !ok {
		return false
	}
	return before == after
}
