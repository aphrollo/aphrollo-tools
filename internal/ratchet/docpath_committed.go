package ratchet

import (
	"path/filepath"
	"strings"
)

// The oracle a citation is judged against. os.Stat answers "is this on THIS
// disk", which is a different question from the one a citation makes a
// promise about: "will the next reader find it". The two part company exactly
// where .gitignore does — borld#301 recorded a citation to
// crates/movement/docs/decisions.md that passed the gate because the file was
// on the author's disk, while `git add -A` had skipped it for matching the
// repo's own `*.md` ignore rule, so no other checkout had it at all.
//
// So when the run knows what the commit contains — the commit gate hands the
// checker `git ls-files` in Options.Tracked, for the same reason it uses that
// set to decide what to SCAN — the same set decides what RESOLVES. When it
// does not (the pre-edit hook, a bare `ratchet check`), the disk stays the
// oracle: a checker that answered "nothing resolves" there would refuse every
// citation in the repo over a set it was never given.

// CommittedPathSet turns the tracked file list into the set a citation is
// resolved against: every path, plus every ancestor DIRECTORY of one. Git
// tracks files and never directories, so a Form-B citation naming a directory
// (`internal/tdd/`) has to resolve through the files committed under it. nil
// for an empty list, which is what tells a law its commit is unknown.
func CommittedPathSet(tracked []string) map[string]bool {
	if len(tracked) == 0 {
		return nil
	}
	set := make(map[string]bool, 2*len(tracked))
	for _, rel := range tracked {
		for rel = citationKey(rel); rel != ""; rel = parentDir(rel) {
			if set[rel] {
				break // this path's ancestors are already in, and so are theirs
			}
			set[rel] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// citationKey is one path in the form the set is keyed by: slash-separated,
// with no leading or trailing slash, so a directory citation written with a
// trailing slash and the same directory derived from a file path agree.
func citationKey(rel string) string {
	return strings.Trim(normalizeSlashes(rel), "/")
}

// parentDir is the directory holding rel, "" once there is none left.
func parentDir(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i > 0 {
		return rel[:i]
	}
	return ""
}

// citedResolves answers whether ONE candidate repo-relative path is something
// this repo carries — against the commit when the law knows it, against the
// working tree when it does not.
func (l Law) citedResolves(rel string) bool {
	if l.Committed == nil {
		root := l.Root
		if root == "" {
			root = "."
		}
		return pathExists(filepath.Join(root, rel))
	}
	return l.Committed[citationKey(rel)]
}
