package ratchet

import "path/filepath"

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
// check's tighten path, which does). This is the pairing RULE shared by both
// callers (#490 asked for one mechanism, not two copies); #489 is about the
// COST of the oldContent/newContent reads this makes, tracked separately.
func RepathCountedKeys(old, now map[string]int, oldContent, newContent func(rel string) (string, bool)) map[string]string {
	var removed []string
	for k := range old {
		if _, ok := now[k]; !ok {
			removed = append(removed, k)
		}
	}
	if len(removed) == 0 {
		return nil
	}

	var added []string
	for k := range now {
		if _, ok := old[k]; !ok {
			added = append(added, k)
		}
	}
	if len(added) == 0 {
		return nil
	}

	pairs := make(map[string]string, len(removed))
	paired := make(map[string]bool, len(removed))
	for _, addedKey := range added {
		for _, removedKey := range removed {
			if paired[removedKey] {
				continue
			}
			if old[removedKey] != now[addedKey] {
				continue
			}
			oldBlob, ok := oldContent(removedKey)
			if !ok {
				continue
			}
			newBlob, ok := newContent(addedKey)
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

// GitShowBlob reads one path's content at ref via a single `git show` — the
// "old" side of a Counted-form repath, whose file no longer exists on disk
// once it has moved.
func GitShowBlob(root, ref, rel string) (string, bool) {
	data, err := (&gitBaseReader{root: root, ref: ref}).Read(rel)
	if err != nil {
		return "", false // absence-ok: absent at ref reads as "content unknown", never eligible to pair
	}
	return string(data), true
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
		func(rel string) (string, bool) { return GitShowBlob(opts.Root, base, rel) },
		func(rel string) (string, bool) {
			data, err := readFile(filepath.Join(opts.Root, filepath.FromSlash(rel)))
			if err != nil {
				return "", false // absence-ok: unreadable on disk reads as "content unknown", never eligible to pair
			}
			return string(data), true
		},
	)
	for oldKey, newKey := range pairs {
		baseline.renameKey(oldKey, newKey)
	}
}
