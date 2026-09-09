package tdd

import (
	"strings"
	"testing"
)

// TestSkippedRootsPhrase_KeepsTheCountAndCapsTheNames pins the first half of
// issue #583: `git merge --no-ff lane/aniso-ramp` in a Cargo workspace made
// PostBash print one line naming all 25 roots the merge touched. A merge is
// EXPECTED to move many roots, so the line grew with the size of the merge
// rather than with anything a reader could act on. The fact — how many roots
// this command changed that no gate ran for — is the part that must survive;
// enumerating every one of them is what does not.
func TestSkippedRootsPhrase_KeepsTheCountAndCapsTheNames(t *testing.T) {
	roots := []string{}
	for _, name := range []string{"aero", "brakes", "chassis", "drive", "engine", "frame"} {
		roots = append(roots, "/repo/crates/"+name)
	}

	phrase := skippedRootsPhrase(roots)

	for _, named := range roots[:maxNamedSkippedRoots] {
		if !strings.Contains(phrase, named) {
			t.Errorf("phrase = %q, want the first roots still named (%s)", phrase, named)
		}
	}
	for _, hidden := range roots[maxNamedSkippedRoots:] {
		if strings.Contains(phrase, hidden) {
			t.Errorf("phrase = %q, want it to stop naming roots after %d, not list %s", phrase, maxNamedSkippedRoots, hidden)
		}
	}
	if !strings.Contains(phrase, "3 more") {
		t.Errorf("phrase = %q, want the roots it does not name still counted", phrase)
	}
}

// A handful of roots is exactly what a reader can act on, so a short list is
// named in full with no "and N more" tail to look up.
func TestSkippedRootsPhrase_NamesThemAllWhenThereAreFew(t *testing.T) {
	phrase := skippedRootsPhrase([]string{"/repo/pkga", "/repo/pkgb"})

	if !strings.Contains(phrase, "/repo/pkga") || !strings.Contains(phrase, "/repo/pkgb") {
		t.Errorf("phrase = %q, want both roots named", phrase)
	}
	if strings.Contains(phrase, "more") {
		t.Errorf("phrase = %q, want no truncation tail when nothing was truncated", phrase)
	}
}
