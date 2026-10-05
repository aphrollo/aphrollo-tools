package ratchet

import (
	"fmt"
	"path/filepath"
)

// lineKeyedKinds are the matchers whose hits are `<path> | <trimmed line>`:
// one offending LINE inside one in-scope file. Their debt is a multiset of
// TEXT, so moving the file that carries a line is not a regression. The
// whole-tree kinds are excluded on purpose — a dependency PATH, a registry
// name or a containment capture is already path-free, and stripping at the
// first ` | ` there would only blur two categories into one.
var lineKeyedKinds = map[MatcherKind]bool{
	KindRegexAbsent:       true,
	KindMarkerWithinLines: true,
	KindRegexNear:         true,
	KindMarkerInPackage:   true,
	KindDocPathResolves:   true,
	KindHunkRegex:         true,
	KindOracleSmell:       true,
}

// baselineForm is the shape a law's baseline file is read in: counted per
// file, a multiset of exact keys, or a multiset of offending text.
func baselineForm(law Law) Form {
	switch {
	case law.Matcher.Key == KeyFile:
		return Counted
	case lineKeyedKinds[law.Matcher.Kind]:
		return MultisetByText
	default:
		return Multiset
	}
}

// loadLawBaseline reads a law's baseline in the form its key kind implies. A
// law with no baseline declared is judged at a bar of zero. A run handed the
// baseline's content (Options.Proposed — the commit gate's staged tree) reads
// that, never the copy on disk, so the baseline and the files it is compared
// with come from the same tree.
func loadLawBaseline(opts Options, law Law) (*Baseline, string, error) {
	form := baselineForm(law)
	if law.Baseline == "" {
		return &Baseline{form: form}, "", nil
	}
	path := filepath.Join(opts.Root, filepath.FromSlash(law.Baseline))
	if text, ok := opts.Proposed[law.Baseline]; ok {
		b, err := ParseBaseline(text, form)
		if err != nil {
			return nil, path, fmt.Errorf("%s: %w", law.Baseline, err)
		}
		b.path = path
		return b, path, nil
	}
	b, err := LoadBaseline(path, form)
	return b, path, err
}
