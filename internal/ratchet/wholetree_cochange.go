package ratchet

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// KindCoChange: a function annotated as a TWIN of another must change
// whenever its twin changes. Source carries `// twin: <path>#<func>` (or a
// bare `<path>` for a whole-file twin) directly above the declaration it
// marks; the relation is symmetric by construction — the marker names both
// endpoints outright, so `A twin B` needs no matching annotation on B for the
// engine to also check `B twin A`. Diff-scoped: it answers nothing without a
// changed-set (see changedInput).
const KindCoChange MatcherKind = "co-change"

// coChangeMarkerRe matches a `// twin: <target>` marker line, capturing the
// target text verbatim — `<path>#<func>` or a bare `<path>`.
var coChangeMarkerRe = regexp.MustCompile(`^[ \t]*//[ \t]*twin:[ \t]*(\S+)[ \t]*$`)

// coChangeEscapeToken is co-change's own escape, fixed like symbol-removed's
// tombstone rather than configured per-law: `// twin-diverges-ok: <why>` on
// the marker line, or in the comment run directly beside it, waives one
// marked declaration's divergence from its twin for this commit.
const coChangeEscapeToken = "twin-diverges-ok:"

// coChangeTarget is one parsed `// twin:` marker: which file it names, and
// which function within it (empty for a whole-file twin).
type coChangeTarget struct {
	Path, Func string
}

func parseCoChangeTarget(raw string) coChangeTarget {
	path, fn, _ := strings.Cut(raw, "#")
	return coChangeTarget{Path: path, Func: fn}
}

// declLineFor is the first line at or after a marker that is not itself part
// of the marker's own comment run — the declaration the marker describes,
// matching every other marker-shaped rule in this engine (`// bound:`,
// `// nan-safe:`): the marker sits ABOVE what it marks.
func declLineFor(raw []string, markerIdx int) int {
	i := markerIdx + 1
	for i < len(raw) && inCommentRun(raw[i], "//") {
		i++
	}
	return i
}

// funcExtent is the brace-balanced body starting at declIdx (0-based),
// returned as a [start,end] 1-based POST line range. It is a heuristic, not
// a parser — plain brace counting over raw text — sufficient for the
// curly-brace languages (Go, Rust) the seed pass annotates; a declaration
// with no `{` at all (an interface method signature) reports just its own
// line, both ends equal.
func funcExtent(raw []string, declIdx int) (start, end int) {
	start = declIdx + 1
	if declIdx >= len(raw) {
		return start, start
	}
	depth, opened := 0, false
	for i := declIdx; i < len(raw); i++ {
		for _, c := range raw[i] {
			switch c {
			case '{':
				depth++
				opened = true
			case '}':
				depth--
			}
		}
		if opened && depth <= 0 {
			return start, i + 1
		}
	}
	return start, len(raw)
}

// coChangeEscaped reports whether the marker at raw[idx] carries co-change's
// escape token on its own line or in the contiguous comment run around it.
func coChangeEscaped(raw []string, idx int) bool {
	if escapeCarriesReason(raw[idx], coChangeEscapeToken) {
		return true
	}
	for i := idx - 1; i >= 0 && inCommentRun(raw[i], "//"); i-- {
		if escapeCarriesReason(raw[i], coChangeEscapeToken) {
			return true
		}
	}
	for i := idx + 1; i < len(raw) && inCommentRun(raw[i], "//"); i++ {
		if escapeCarriesReason(raw[i], coChangeEscapeToken) {
			return true
		}
	}
	return false
}

// funcNameFromTarget is the bare identifier a twin target names: a marker
// may write a receiver-qualified name (`PR.Apply`) to read naturally beside
// the type it belongs to, but the declaration search only has the identifier
// itself to look for.
func funcNameFromTarget(target string) string {
	if i := strings.LastIndex(target, "."); i >= 0 {
		return target[i+1:]
	}
	return target
}

// goFuncDeclRe matches a Go function or method declaration line naming name
// — a plain `func name(` or a method `func (recv Type) name(`.
func goFuncDeclRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`^func (?:\([^)]*\)\s*)?` + regexp.QuoteMeta(name) + `\b`)
}

// twinTouched reports whether the twin target's own declaration (or, for a
// whole-file twin, the file at all) carries a changed op in diffs — false
// both when the target file never changed at all (absent from diffs
// entirely) and when a named function's declaration cannot be found in its
// current text (the fail-safe direction: a co-change law that cannot prove
// the twin moved together must not stay silent).
func twinTouched(diffs map[string]FileDiff, target coChangeTarget) bool {
	fd, ok := diffs[target.Path]
	if !ok {
		return false
	}
	raw := splitLines(fd.Post)
	if target.Func == "" {
		return hunkTouches(fd.Ops, 1, len(raw))
	}
	re := goFuncDeclRe(funcNameFromTarget(target.Func))
	for i, line := range raw {
		if re.MatchString(line) {
			start, end := funcExtent(raw, i)
			return hunkTouches(fd.Ops, start, end)
		}
	}
	return false
}

// coChangeHits is #316's rule: for every `// twin:` marker in a changed
// file's current text, when the marked declaration's own extent carries a
// changed op, the named twin must ALSO carry one — in its own extent when the
// marker names a function, anywhere in the file for a whole-file twin.
func coChangeHits(law Law, base BaseReader, changed []string, content map[string]string) ([]Hit, error) {
	diffs, err := changedFileDiffs(law, base, changed, content)
	if err != nil {
		return nil, err
	}
	if len(diffs) == 0 {
		return nil, nil
	}

	type marker struct {
		file       string
		raw        []string
		lineIdx    int
		start, end int
		target     coChangeTarget
	}
	var markers []marker
	for rel, fd := range diffs {
		raw := splitLines(fd.Post)
		for i, line := range raw {
			m := coChangeMarkerRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			decl := declLineFor(raw, i)
			start, end := funcExtent(raw, decl)
			markers = append(markers, marker{file: rel, raw: raw, lineIdx: i, start: start, end: end, target: parseCoChangeTarget(m[1])})
		}
	}

	var hits []Hit
	for _, mk := range markers {
		fd := diffs[mk.file]
		if !hunkTouches(fd.Ops, mk.start, mk.end) {
			continue // the marked declaration itself did not change
		}
		if coChangeEscaped(mk.raw, mk.lineIdx) {
			continue
		}
		if twinTouched(diffs, mk.target) {
			continue
		}
		target := mk.target.Path
		if mk.target.Func != "" {
			target += "#" + mk.target.Func
		}
		hits = append(hits, Hit{
			Law: law.Name, File: mk.file, Line: mk.lineIdx + 1, Weight: 1,
			Key: mk.file + " | twin:" + target,
			What: fmt.Sprintf("changed but its twin %s did not — escape with `// %s <why>` on the marker line, or update the twin",
				target, coChangeEscapeToken),
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].File != hits[j].File {
			return hits[i].File < hits[j].File
		}
		return hits[i].Line < hits[j].Line
	})
	return hits, nil
}
