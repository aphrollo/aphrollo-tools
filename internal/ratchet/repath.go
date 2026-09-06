package ratchet

import (
	"path/filepath"
	"sort"
)

// RepathCountedKeys pairs a key that disappeared between old and now with a
// key that appeared, when both carry the SAME count and IDENTICAL content —
// the signature of a file that moved rather than a new file crossing a
// ceiling for the first time. Only a Counted (file-identity) baseline's rows
// can even be candidates: its identity IS the file path, so a vanished path
// and a newly-appeared one are the only shape where "this looks like a
// rename" is a question worth asking at all (every other baseline form
// already drops the path from its identity, see Baseline.identity).
//
// It returns the pairing as old-key -> new-key, one entry per matched
// rename, and never mutates old or now itself: a caller applies the pairing
// to whatever its own baseline representation is — a bare map (the staged-
// baseline guard, which never writes a file) or a *Baseline's line (ratchet
// check's tighten path, which does).
//
// oldContent and newContent each answer every CANDIDATE path's content in
// ONE round trip — a git batch read or a plain map lookup — rather than one
// call per pair, so pairing N removed keys against M added ones costs
// O(N+M) content fetches, never the O(N*M) `git show` calls a naive nested
// loop pays (#489, measured at ~168x for a 90-rename batch: 32.0s vs
// 190.6ms). A path absent from the result reads as "content unknown", never
// eligible to pair.
func RepathCountedKeys(old, now map[string]int, oldContent, newContent func(rels []string) map[string]string) map[string]string {
	var removed []string
	for k := range old {
		if _, ok := now[k]; !ok {
			removed = append(removed, k)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	sort.Strings(removed)

	var added []string
	for k := range now {
		if _, ok := old[k]; !ok {
			added = append(added, k)
		}
	}
	if len(added) == 0 {
		return nil
	}
	sort.Strings(added)

	// Indexing removed keys by count means each added key only ever walks
	// the rows that could possibly match it, instead of every removed row —
	// the other half of #489's fix, independent of the batched content read.
	byCount := map[int][]string{}
	for _, k := range removed {
		byCount[old[k]] = append(byCount[old[k]], k)
	}

	oldBlobs := oldContent(removed)
	newBlobs := newContent(added)

	pairs := make(map[string]string, len(removed))
	paired := make(map[string]bool, len(removed))
	for _, addedKey := range added {
		newBlob, ok := newBlobs[addedKey]
		if !ok {
			continue
		}
		for _, removedKey := range byCount[now[addedKey]] {
			if paired[removedKey] {
				continue
			}
			oldBlob, ok := oldBlobs[removedKey]
			if !ok || oldBlob != newBlob {
				continue
			}
			pairs[removedKey] = addedKey
			paired[removedKey] = true
			break
		}
	}
	return pairs
}

// GitBatchBlobs reads every rel's blob content at ref in one `git cat-file
// --batch` process (gitBaseReader.ReadAll) — ref == "" reads the STAGED
// INDEX, via git's own bare `:path` revision syntax. A caller outside this
// package (the staged-baseline guard, which judges git refs directly rather
// than through a BaseReader, and runs every git subprocess through its own
// scrubbed environment) uses ParseCatFileBatch directly instead, over its
// own exec.Command.
func GitBatchBlobs(root, ref string, rels []string) (map[string]string, error) {
	data, err := (&gitBaseReader{root: root, ref: ref}).ReadAll(rels)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(data))
	for k, v := range data {
		out[k] = string(v)
	}
	return out, nil
}

// diskBlobs reads every rel straight off root's working tree — the "new"
// side of a Counted-form repath, which (unlike the vanished old path) is
// still sitting on disk and needs no git call at all. A rel this cannot
// read (permission, or a race with a concurrent delete) is simply absent
// from the result, the same "content unknown, never eligible" tolerance
// RepathCountedKeys already has for a missing git blob.
func diskBlobs(root string, rels []string) map[string]string {
	out := make(map[string]string, len(rels))
	for _, rel := range rels {
		data, err := readFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue // absence-ok: unreadable on disk reads as "content unknown", never eligible to pair
		}
		out[rel] = string(data)
	}
	return out
}

// renameKey rewrites the one Counted-form line carrying key `from` to key
// `to`, in place. A Counted baseline has at most one row per key
// (ParseBaseline rejects a duplicate), so this touches at most one line.
func (b *Baseline) renameKey(from, to string) {
	for i := range b.lines {
		if b.lines[i].data && b.lines[i].key == from {
			b.lines[i].key = to
			return
		}
	}
}

// repathCountedBaseline pairs baseline rows against what this run just
// measured and re-paths every match onto its new key, so a file that moved
// keeps its row instead of reading as a brand-new file over the ceiling
// (#490) — the false positive `raisedKeys`' tdd twin already avoided by
// running the same pairing before the staged-baseline guard ever refuses.
// It is a no-op for any form but Counted, whose identity is the file path.
//
// base defaults to HEAD when the caller named none: `ratchet check` run by
// hand carries no --base, and HEAD is the one ref that always means
// "what this commit is about to sit on top of" for the working tree it just
// scanned.
func repathCountedBaseline(opts Options, baseline *Baseline, measured map[string]int) {
	if baseline.form != Counted {
		return
	}
	base := opts.Base
	if base == "" {
		base = "HEAD"
	}
	pairs := RepathCountedKeys(baseline.Counts(), measured,
		func(rels []string) map[string]string {
			blobs, err := GitBatchBlobs(opts.Root, base, rels)
			if err != nil {
				return nil // absence-ok: a failed batch read reads as "content unknown" for every candidate, never eligible to pair
			}
			return blobs
		},
		func(rels []string) map[string]string { return diskBlobs(opts.Root, rels) },
	)
	for oldKey, newKey := range pairs {
		baseline.renameKey(oldKey, newKey)
	}
}
