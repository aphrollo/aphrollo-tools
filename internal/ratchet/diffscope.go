package ratchet

import (
	"fmt"
	"sort"

	"github.com/aphrollo/aphrollo-tools/internal/diff"
)

// FileDiff is one in-scope changed file's before/after content and the
// line-level diff between them — the diff-relational engine input #315
// exists for: a matcher judging what a commit CHANGED, not what the tree
// holds. Pre is "" when the file is new to this diff (no pre-image, every op
// is '+'); Post is whatever the tree scan already read for it (the index
// blob at commit time, the current worktree file for a fixture).
type FileDiff struct {
	Pre, Post string
	Ops       []diff.LineOp
}

// changedInput resolves a `[scope] changed` law's two diff-relational
// inputs from Options: the PRE-image reader and the file list this scope
// kind names as "changed". Neither is computed here — see StagedFiles,
// LaneFiles, Base and LaneBase on Options — because the commit and merge
// gates already compute both sets for their own use (baselineguard.go's
// stagedFiles, precommit_fmtscope.go's laneChangedPaths); this is the one
// place that ROUTES them into the law engine rather than re-deriving them.
func changedInput(law Law, opts Options) (base BaseReader, files []string) {
	switch law.Scope.Changed {
	case ChangedStaged:
		base, files = resolveBaseTree(opts), opts.StagedFiles
	case ChangedLane:
		base, files = resolveLaneBaseTree(opts), opts.LaneFiles
	default:
		return nil, nil
	}
	if base != nil && len(opts.Renames) > 0 {
		base = renamingReader{BaseReader: base, from: opts.Renames}
	}
	return base, files
}

// renamingReader answers a renamed file's pre-image from the path it was
// renamed from, keyed by its new path, so every diff-relational law sees a
// pure move as unchanged content.
type renamingReader struct {
	BaseReader
	from map[string]string
}

func (r renamingReader) Read(path string) ([]byte, error) {
	if old, ok := r.from[path]; ok {
		return r.BaseReader.Read(old)
	}
	return r.BaseReader.Read(path)
}

func (r renamingReader) ReadAll(paths []string) (map[string][]byte, error) {
	asked := make([]string, len(paths))
	for i, p := range paths {
		asked[i] = p
		if old, ok := r.from[p]; ok {
			asked[i] = old
		}
	}
	got, err := r.BaseReader.ReadAll(asked)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(got))
	for i, p := range paths {
		if data, ok := got[asked[i]]; ok {
			out[p] = data
		}
	}
	return out, nil
}

// changedFileDiffs resolves a diff-relational law's input set into one
// FileDiff per file: every path in `changed` that also matches the law's own
// glob scope, its PRE content read from base (one batched round trip, like
// symbol-removed's), its POST content from `content` (whatever the tree scan
// already read — real run or fixture). A changed path with no POST content
// (the tree scan never read it, or it was deleted this commit) is skipped —
// a diff-relational law judges what a hunk touches, and a deleted file
// carries no surviving declaration for a hit to point at.
func changedFileDiffs(law Law, base BaseReader, changed []string, content map[string]string) (map[string]FileDiff, error) {
	var matched []string
	for _, rel := range changed {
		if law.Scope.Matches(rel) {
			matched = append(matched, rel)
		}
	}
	sort.Strings(matched)

	var pre map[string][]byte
	if base != nil && len(matched) > 0 {
		var err error
		pre, err = base.ReadAll(matched)
		if err != nil {
			return nil, fmt.Errorf("law %q: reading base tree: %w", law.Name, err)
		}
	}

	out := map[string]FileDiff{}
	for _, rel := range matched {
		post, ok := content[rel]
		if !ok {
			continue
		}
		preText := ""
		if data, ok := pre[rel]; ok {
			preText = string(data)
		}
		out[rel] = FileDiff{Pre: preText, Post: post, Ops: diff.Lines(preText, post)}
	}
	return out, nil
}

// hunkTouches reports whether any changed (non-context) op falls within
// [start,end], a 1-based POST line range. A '+' op's NewPos is a real POST
// line, checked directly; a '-' op has no POST line of its own, so its
// NewPos (the POST position it would have landed at) is checked against
// [start-1,end] — inclusive of the line just above the range, since a
// deletion at the very top of a declaration reports there. This is a
// heuristic over line numbers, not a parser: precise to within the one line
// a removal can land ambiguously on, which is the same tolerance
// `codeLines` already documents for this engine's other line-based scanners.
func hunkTouches(ops []diff.LineOp, start, end int) bool {
	for _, o := range ops {
		if o.Kind == ' ' {
			continue
		}
		lo := start
		if o.Kind == '-' {
			lo = start - 1
		}
		if o.NewPos >= lo && o.NewPos <= end {
			return true
		}
	}
	return false
}

// changedLawHits is a diff-relational whole-tree kind's entry point from
// Check(): with no base and no changed-file list, the law answers nothing
// rather than guessing, and notes the skip once — the same tolerance
// symbolRemovedLawHits already has for "no --base given".
func changedLawHits(law Law, opts Options, files []string, content map[string]string, res *Result,
	compute func(base BaseReader, changed []string, content map[string]string) ([]Hit, error)) ([]Hit, error) {
	base, changed := changedInput(law, opts)
	if base == nil || len(changed) == 0 {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"%s: skipped, no %s changed-set given", law.Name, law.Scope.Changed))
		return nil, nil
	}
	return compute(base, changed, content)
}
