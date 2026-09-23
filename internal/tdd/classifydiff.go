package tdd

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// DiffClass is how much checking a committed change can owe, lightest to
// heaviest, as CI's `changes` job reads it from `aphrollo gate
// classify-diff`. The commit and merge gates take their own fast paths off
// the same per-file rules: ClassifyFile for a file's kind (with its
// //go:embed awareness) and commentOnlyChange for a source file's diff. A
// rule tightened in either reaches both sides.
type DiffClass string

const (
	// DiffDocsOnly: every changed file is prose (see proseFile).
	DiffDocsOnly DiffClass = "docs-only"
	// DiffCommentOnly: every changed source file is a .go or .rs file whose
	// diff changes no token outside a comment, and the rest is prose.
	DiffCommentOnly DiffClass = "comment-only"
	// DiffWorkflowOnly: every changed file sits under .github/, or is prose.
	DiffWorkflowOnly DiffClass = "workflow-only"
	// DiffCode: anything else, and the answer whenever the classification
	// itself could not be made.
	DiffCode DiffClass = "code"
)

// StagedFastPath is the fast path the commit and merge gates take for
// repoRoot's staged set, and what Precommit and Mechanical dispatch on:
// DiffDocsOnly when nothing staged is source or test (docsOnly),
// DiffCommentOnly when every staged source is a comment-only Go or Rust
// diff (commentOnlySource), DiffCode otherwise. It never answers
// DiffWorkflowOnly: that path is CI's alone.
func StagedFastPath(repoRoot string) DiffClass {
	if docsOnly(repoRoot) {
		return DiffDocsOnly
	}
	if commentOnlySource(repoRoot) {
		return DiffCommentOnly
	}
	return DiffCode
}

// ClassifyDiff classifies the change from base to head in repoRoot. head
// must be the checked-out commit and the process's working directory the
// checkout's root: the embed rule reads //go:embed directives from the files
// on disk, relative to it, exactly as the commit gate does.
//
// Every failure answers DiffCode alongside the error -- an unresolvable
// base, a head that is not the checkout, a git call that fails, an empty
// diff -- so a caller that only reads the class can never fall onto a fast
// path it did not earn.
//
// The docs-only set here is NARROWER than the commit gate's docsOnly, which
// waves through every file ClassifyFile calls Ignore: a test fixture under
// testdata/, a .ratchet/ law, .golangci.yml, a deploy script. Each of those
// changes what a check does, so CI keeps them on the full path.
func ClassifyDiff(repoRoot, base, head string) (DiffClass, error) {
	baseID, err := git(repoRoot, "rev-parse", "--verify", "--quiet", base+"^{commit}")
	if err != nil {
		return DiffCode, fmt.Errorf("base %q does not resolve to a commit in this clone", base)
	}
	headID, err := git(repoRoot, "rev-parse", "--verify", "--quiet", head+"^{commit}")
	if err != nil {
		return DiffCode, fmt.Errorf("head %q does not resolve to a commit in this clone", head)
	}
	checkout, err := git(repoRoot, "rev-parse", "--verify", "HEAD")
	if err != nil || strings.TrimSpace(checkout) != strings.TrimSpace(headID) {
		return DiffCode, fmt.Errorf("head %q is not the checked-out commit, and the embed rule reads the checkout", head)
	}
	baseID, headID = strings.TrimSpace(baseID), strings.TrimSpace(headID)
	out, err := git(repoRoot, "diff", "--name-only", "--no-renames", "-z", baseID, headID)
	if err != nil {
		return DiffCode, fmt.Errorf("git diff %s %s: %v", baseID, headID, err)
	}
	paths := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	if len(paths) == 0 || paths[0] == "" {
		return DiffCode, errors.New("the diff is empty, so nothing says which path it may take")
	}
	var workflow, comment bool
	for _, p := range paths {
		switch fileClass(repoRoot, baseID, headID, p) {
		case DiffCode:
			return DiffCode, nil
		case DiffWorkflowOnly:
			workflow = true
		case DiffCommentOnly:
			comment = true
		}
	}
	switch {
	case workflow && comment:
		// Neither lighter path runs both halves' checks.
		return DiffCode, nil
	case workflow:
		return DiffWorkflowOnly, nil
	case comment:
		return DiffCommentOnly, nil
	}
	return DiffDocsOnly, nil
}

// fileClass is one changed file's class: its own contribution to the whole
// diff's answer.
func fileClass(repoRoot, baseID, headID, p string) DiffClass {
	if strings.HasPrefix(p, ".github/") {
		return DiffWorkflowOnly
	}
	switch ClassifyFile(p) {
	case Test:
		return DiffCode
	case Source:
		// An added or deleted file has only one blob, so one of these reads
		// fails and the file is code.
		pre, err := git(repoRoot, "show", baseID+":"+p)
		if err != nil {
			return DiffCode
		}
		post, err := git(repoRoot, "show", headID+":"+p)
		if err != nil {
			return DiffCode
		}
		if commentOnlyChange(p, pre, post) {
			return DiffCommentOnly
		}
		return DiffCode
	}
	if proseFile(p) {
		return DiffDocsOnly
	}
	return DiffCode
}

// proseFile reports whether p, a file ClassifyFile already calls Ignore, is
// documentation: markdown, anything under docs/, the licence, or
// .gitignore. A file under a directory ClassifyFile skips (testdata/,
// .ratchet/, vendor/, ...) is a fixture or tool data, never prose, whatever
// its extension.
func proseFile(p string) bool {
	for seg := range strings.SplitSeq(path.Dir(p), "/") {
		if ignoredDirs[seg] {
			return false
		}
	}
	base := path.Base(p)
	return strings.EqualFold(path.Ext(base), ".md") ||
		strings.HasPrefix(p, "docs/") ||
		strings.HasPrefix(base, "LICENSE") ||
		p == ".gitignore"
}
