package mutation

import (
	"bytes"
	"fmt"
	"strings"
)

// mutationLandedEvidence is the check that a proof's mutation actually
// changed the file: the content git would store for relPath, hashed from the
// bytes the proof started with and again from the file on disk after the
// write. "" means the two are the same object, so nothing was mutated.
//
// Against the proof's own starting bytes, not against the index: a file that
// already carries unstaged work diffs non-empty whatever the mutation did,
// and an untracked file has no index entry to diff against at all. Hashed
// through `--path`, so the repository's clean filters and line-ending
// attributes apply: an edit git normalises away (a CRLF where the attributes
// say LF) is no change to the content any tool reads, and is not a mutation.
func mutationLandedEvidence(repoRoot, relPath string, before []byte) (string, error) {
	was, err := gitStdin(repoRoot, bytes.NewReader(before), "hash-object", "--path="+relPath, "--stdin")
	if err != nil {
		return "", fmt.Errorf("git hash-object of the starting content: %v: %s", err, strings.TrimSpace(was))
	}
	now, err := git(repoRoot, "hash-object", "--path="+relPath, "--", relPath)
	if err != nil {
		return "", fmt.Errorf("git hash-object of the mutated file: %v: %s", err, strings.TrimSpace(now))
	}
	was, now = strings.TrimSpace(was), strings.TrimSpace(now)
	if was == now {
		return "", nil
	}
	return fmt.Sprintf("content %s -> %s", was, now), nil
}
