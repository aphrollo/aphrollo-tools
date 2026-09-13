package ratchet

// The scan cache's contract is that an entry answers a file whose bytes have
// not moved. That holds for every matcher whose verdict the file's own text
// decides, and it does not hold for doc-path-resolves: its verdict also reads
// the oracle the run was handed and, through it, the index. Two runs over an
// identical NOTES.md legitimately disagree — the commit gate judging the
// commit, a bare `ratchet check` judging the disk — and whichever ran first
// used to answer for both.
//
// So an entry keeps the two apart: the citations (text) are recorded, the
// resolution (oracle) is redone. The cost is a map lookup per citation on a
// cache hit, against a full re-read and re-regex of the file if the whole
// entry were thrown away instead.

// contentHits is the part of a freshly-scanned file's hits an entry may keep:
// everything except a doc-path-resolves law's, which is rebuilt from Cited.
// A nil result keeps an entry that cites things but offends nothing small.
func (c *scanCache) contentHits(hits map[string][]Hit) map[string][]Hit {
	if len(c.docLaws) == 0 || len(hits) == 0 {
		return hits
	}
	kept := make(map[string][]Hit, len(hits))
	for name, h := range hits {
		if c.isDocLaw(name) {
			continue
		}
		kept[name] = h
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// resolveCited rebuilds one cached entry's full hit set for THIS run: the
// recorded content hits as they stand, plus every recorded citation this
// run's oracle cannot account for. The entry is never mutated — it is still
// the record the next lookup and the save() at the end of the scan read.
func (c *scanCache) resolveCited(rel string, e cacheEntry) map[string][]Hit {
	if len(e.Cited) == 0 {
		return e.Hits
	}
	out := make(map[string][]Hit, len(e.Hits)+len(e.Cited))
	for name, h := range e.Hits {
		out[name] = h
	}
	for _, l := range c.docLaws {
		cited, ok := e.Cited[l.Name]
		if !ok {
			continue
		}
		if h := l.docPathUnresolved(rel, cited); len(h) > 0 {
			out[l.Name] = h
		}
	}
	return out
}

func (c *scanCache) isDocLaw(name string) bool {
	for _, l := range c.docLaws {
		if l.Name == name {
			return true
		}
	}
	return false
}
