package ratchet

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/diff"
)

// KindHunkRegex: a regex judged over a changed file's REMOVED lines, ADDED
// lines, or a positionally-paired substitution of the two — #318's engine
// for "a test removed, an assertion weakened, or a golden value moved in the
// same commit as the source change it should have caught." Diff-scoped: it
// answers nothing without a changed-set (see changedInput).
const KindHunkRegex MatcherKind = "hunk-regex"

// HunkRegexMode is a paired hunk-regex law's comparison: Forbid (the default)
// fires whenever both sides match, Differs additionally requires the single
// capture group both sides share to hold a DIFFERENT value — the same shape,
// a different literal.
type HunkRegexMode string

const (
	HunkForbid  HunkRegexMode = "forbid"
	HunkDiffers HunkRegexMode = "differs"
)

// removesTestTrailerRe is a name_group law's OWN escape, fixed like
// symbol-removed's tombstone: a commit-message trailer, because the removed
// line it excuses no longer exists anywhere for an in-file comment to sit
// beside.
var removesTestTrailerRe = regexp.MustCompile(`(?m)^Removes-test:[ \t]*(\S+):[ \t]*\S`)

// commitTrailerEscapes collects every name a `Removes-test: <name>: <why>`
// trailer admits.
func commitTrailerEscapes(msg string) map[string]bool {
	out := map[string]bool{}
	for _, m := range removesTestTrailerRe.FindAllStringSubmatch(msg, -1) {
		out[m[1]] = true
	}
	return out
}

// opPair is one substitution: a removed line and the added line that
// positionally replaced it.
type opPair struct {
	removed, added diff.LineOp
}

// pairedSubstitutions zips each maximal run of removed ops with the maximal
// run of added ops immediately following it (no context line between them) —
// the shape a one-line edit produces. A run-length mismatch pairs only the
// shorter count; the excess lines are a genuine insertion or deletion, not a
// substitution, and are not this matcher's business.
func pairedSubstitutions(ops []diff.LineOp) []opPair {
	var pairs []opPair
	i := 0
	for i < len(ops) {
		if ops[i].Kind != '-' {
			i++
			continue
		}
		start := i
		for i < len(ops) && ops[i].Kind == '-' {
			i++
		}
		removedRun := ops[start:i]
		addStart := i
		for i < len(ops) && ops[i].Kind == '+' {
			i++
		}
		addedRun := ops[addStart:i]
		n := min(len(removedRun), len(addedRun))
		for k := 0; k < n; k++ {
			pairs = append(pairs, opPair{removed: removedRun[k], added: addedRun[k]})
		}
	}
	return pairs
}

// hunkRegexHits is Check()'s entry point for KindHunkRegex, dispatching to
// the mode the law's fields select.
func hunkRegexHits(law Law, base BaseReader, changed []string, content map[string]string, commitMessage string) ([]Hit, error) {
	diffs, err := changedFileDiffs(law, base, changed, content)
	if err != nil {
		return nil, err
	}
	if len(diffs) == 0 {
		return nil, nil
	}
	escapedNames := commitTrailerEscapes(commitMessage)

	var files []string
	for rel := range diffs {
		files = append(files, rel)
	}
	sort.Strings(files)

	var hits []Hit
	for _, rel := range files {
		fd := diffs[rel]
		raw := splitLines(fd.Post)
		switch {
		case law.Matcher.NameGroup:
			hits = append(hits, nameGroupHits(law, rel, fd, escapedNames)...)
		case law.Matcher.Paired:
			hits = append(hits, pairedHits(law, rel, fd, raw)...)
		default:
			hits = append(hits, standaloneHits(law, rel, fd, raw)...)
		}
	}
	return hits, nil
}

// standaloneHits judges removed and added lines independently: a removed
// line has no surviving position for an escape comment, so only an added-line
// hit ever checks one.
func standaloneHits(law Law, rel string, fd FileDiff, raw []string) []Hit {
	var hits []Hit
	for _, o := range fd.Ops {
		switch {
		case o.Kind == '-' && law.Matcher.Removed != nil && law.Matcher.Removed.MatchString(o.Line):
			hits = append(hits, Hit{
				Law: law.Name, File: rel, Line: o.OldPos, Weight: 1,
				Key:  rel + " | removed:" + strings.TrimSpace(o.Line),
				What: "removed line matches " + law.Matcher.Removed.String(),
			})
		case o.Kind == '+' && law.Matcher.Added != nil && law.Matcher.Added.MatchString(o.Line):
			if law.escaped(rel, raw, o.NewPos-1) {
				continue
			}
			hits = append(hits, Hit{
				Law: law.Name, File: rel, Line: o.NewPos, Weight: 1,
				Key:  rel + " | added:" + strings.TrimSpace(o.Line),
				What: "added line matches " + law.Matcher.Added.String(),
			})
		}
	}
	return hits
}

// pairedHits judges each removed/added substitution pair: Forbid fires when
// both sides match their own pattern; Differs additionally requires the
// shared capture group's value to have changed — the same shape holding a
// different literal.
func pairedHits(law Law, rel string, fd FileDiff, raw []string) []Hit {
	var hits []Hit
	for _, p := range pairedSubstitutions(fd.Ops) {
		rm := law.Matcher.Removed.FindStringSubmatch(p.removed.Line)
		am := law.Matcher.Added.FindStringSubmatch(p.added.Line)
		if rm == nil || am == nil {
			continue
		}
		if law.Matcher.HunkMode == HunkDiffers {
			rc, ac := lastCapture(rm), lastCapture(am)
			if rc == "" || ac == "" || rc == ac {
				continue
			}
		}
		idx := p.added.NewPos - 1
		if law.escaped(rel, raw, idx) {
			continue
		}
		hits = append(hits, Hit{
			Law: law.Name, File: rel, Line: p.added.NewPos, Weight: 1,
			Key:  rel + " | " + strings.TrimSpace(p.removed.Line) + " -> " + strings.TrimSpace(p.added.Line),
			What: fmt.Sprintf("%q became %q", strings.TrimSpace(p.removed.Line), strings.TrimSpace(p.added.Line)),
		})
	}
	return hits
}

// nameGroupHits is the "removed with no re-add of the same name" mode: every
// removed line's captured name is checked against every added line's
// captured name in the SAME file's diff — a rename or a genuine move within
// the file is not a hit, only a name that vanished outright. Escaped by a
// `Removes-test: <name>: <why>` commit trailer, never an in-file comment,
// because the removed line leaves nothing to attach one to.
func nameGroupHits(law Law, rel string, fd FileDiff, escapedNames map[string]bool) []Hit {
	added := map[string]bool{}
	for _, o := range fd.Ops {
		if o.Kind != '+' {
			continue
		}
		if m := law.Matcher.Added.FindStringSubmatch(o.Line); m != nil {
			if name := lastCapture(m); name != "" {
				added[name] = true
			}
		}
	}
	var hits []Hit
	for _, o := range fd.Ops {
		if o.Kind != '-' {
			continue
		}
		m := law.Matcher.Removed.FindStringSubmatch(o.Line)
		if m == nil {
			continue
		}
		name := lastCapture(m)
		if name == "" {
			continue
		}
		if added[name] || escapedNames[name] {
			continue
		}
		// No Line: the removed declaration has no surviving position at
		// tip, so this is a whole-tree, key-identified hit exactly like
		// symbol-removed's — a fixture's expected.txt names it by Key, not
		// by a line number that points at nothing.
		hits = append(hits, Hit{
			Law: law.Name, File: rel, Weight: 1,
			Key: rel + ":" + name,
			What: fmt.Sprintf(
				"%s removed with no added declaration of the same name — escape with a commit trailer `Removes-test: %s: <why>`",
				name, name),
		})
	}
	return hits
}
