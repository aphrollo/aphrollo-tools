package ratchet

// A doc-path-resolves verdict has two halves that cost wildly different
// things and depend on wildly different inputs. FINDING the citations in a
// file is a regex over every line of it — expensive, and a pure function of
// that file's bytes, so the scan cache is right to remember it. RESOLVING one
// is a single lookup — nearly free, and a function of the ORACLE the run was
// handed: the commit when it knows one, the working tree when it does not
// (docpath_committed.go). Caching the second half is what re-opened borld#301
// after the oracle fix landed: a bare `aphrollo ratchet check` resolves
// against the disk, records "clean" for a file whose size and mtime then
// never move, and the commit gate right after it is answered from that record
// instead of judging the commit. It is the same staleness when the oracle
// does not change at all and the INDEX does — a commit that drops the cited
// file leaves every citation to it reading clean.
//
// So the cache keeps the citations and the resolution is redone every run.

// docPathCitations is every citation this law's pattern finds in the file,
// resolved or not — the half that depends only on the file's own text.
func (l Law) docPathCitations(file string, code []string) []Hit {
	var cited []Hit
	for i, line := range code {
		if l.excluded(line) {
			continue
		}
		for _, loc := range l.Matcher.Pattern.FindAllStringSubmatchIndex(line, -1) {
			// The LAST capture group is the citation, by convention (a law's
			// pattern may wrap it in non-capturing alternation groups first).
			gStart, gEnd := loc[len(loc)-2], loc[len(loc)-1]
			if gStart < 0 {
				continue
			}
			if gEnd < len(line) && isPathContinuation(line[gEnd]) {
				// A regex has no notion of "the whole backtick-quoted token" —
				// it just finds the longest run this pattern can describe, which
				// for `refs/notes/gate` is the PREFIX `refs/notes/` (a valid
				// Form-B shape on its own). A citation is the whole token, so a
				// match immediately followed by more identifier or glob
				// characters is a false start, not a shorter citation.
				continue
			}
			cited = append(cited, l.hit(file, i+1, line[gStart:gEnd]))
		}
	}
	return cited
}

// docPathUnresolved is the other half: the citations this run's oracle cannot
// account for. Given the same citations it is deterministic, and given a
// different oracle it is deliberately a different answer — which is exactly
// why it is never the thing that gets cached.
func (l Law) docPathUnresolved(file string, cited []Hit) []Hit {
	var hits []Hit
	for _, c := range cited {
		if l.docResolves(file, c.What) {
			continue
		}
		hits = append(hits, c)
	}
	return hits
}

func (l Law) docPathHits(file string, code []string) []Hit {
	return l.docPathUnresolved(file, l.docPathCitations(file, code))
}
