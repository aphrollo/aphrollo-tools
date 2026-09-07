package tdd

import (
	"fmt"
	"regexp"
	"strings"
)

// eolWorktreeRe and eolAttrRe pull the two fields eolDriftNote compares out
// of one `git ls-files --eol` line — the worktree's actual line ending and
// the eol value its attribute declares, if any. Both are `\S+`-bounded
// because the whitespace git pads the earlier fields with is not fixed-width
// across every git version.
var (
	eolWorktreeRe = regexp.MustCompile(`w/(\S+)`)
	eolAttrRe     = regexp.MustCompile(`eol=(\S+)`)
)

// eolDriftNote is a second, independent diagnostic a hand mutation proof can
// carry alongside its `git diff --numstat` check: relPath's on-disk
// (worktree) line endings may disagree with what `.gitattributes` declares
// for it, which is exactly the shape issue #519 reports — a `--old` pattern
// authored against LF bytes silently matches nothing against a file that is
// CRLF on disk, and `git status` reads clean throughout because git
// normalizes on READ for that comparison. `git ls-files --eol` reports
// index, worktree and attribute in one call, so the comparison needs no
// heuristics of its own.
//
// Empty when nothing is declared for relPath, or when the worktree already
// agrees — there is nothing to warn about either way, and this is never
// itself a refusal: it names a plausible CAUSE alongside a refusal that
// already happened for its own, EOL-independent reason (an empty diff, or a
// pattern that matched zero or more than once).
func eolDriftNote(repoRoot, relPath string) string {
	out, err := git(repoRoot, "ls-files", "--eol", "--", relPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) != relPath {
			continue
		}
		w := eolWorktreeRe.FindStringSubmatch(parts[0])
		attr := eolAttrRe.FindStringSubmatch(parts[0])
		if w == nil || attr == nil || w[1] == attr[1] {
			return ""
		}
		return fmt.Sprintf("%s's on-disk line endings are %s; its declared attribute says eol=%s. "+
			"git diff/status can read clean either way — the fix is a re-checkout of the file, not "+
			"`git add --renormalize` (the index already matches the declared attribute; only the "+
			"worktree does not)", relPath, w[1], attr[1])
	}
	return ""
}
