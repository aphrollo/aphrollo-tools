package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// bashWriteTargets does not evaluate the shell. Its own contract says so:
// "Command substitution, variable expansion and globbing are not evaluated; a
// write hidden behind any of them is missed, which is the fail-open direction
// the edit-time gate owes (a false block wedges a session, a miss costs one
// commit-gate rejection)."
//
// A RELATIVE target defeated that. `cat > "$S/a.md"` is not absolute, so it
// was joined onto the running directory and reported as a write into whatever
// repo that directory belongs to — the exact false block the contract rules
// out, and it wedged a session writing to a scratchpad outside every repo.

// TestBashWriteTargets_ClaimsNothingForAnUnexpandedVariablePath is the case
// that wedged a real session: the variable is assigned in the same command,
// and the path it holds is nowhere near the repo the shell happens to sit in.
func TestBashWriteTargets_ClaimsNothingForAnUnexpandedVariablePath(t *testing.T) {
	repo := filepath.FromSlash("/repo")
	cmd := `cd /repo && S="/tmp/scratch" && cat > "$S/a.md"`

	for _, got := range bashWriteTargets(cmd, repo) {
		if strings.Contains(got, "$") || strings.HasPrefix(got, repo) {
			t.Errorf("bashWriteTargets claimed %q for an unexpanded variable path — it must claim nothing it cannot resolve", got)
		}
	}
}

// TestBashWriteTargets_ClaimsNothingForACommandSubstitutionPath is the same
// rule for the other unevaluated form.
func TestBashWriteTargets_ClaimsNothingForACommandSubstitutionPath(t *testing.T) {
	repo := filepath.FromSlash("/repo")
	cmd := "cd /repo && echo hi > $(mktemp)/out.txt"

	for _, got := range bashWriteTargets(cmd, repo) {
		if strings.HasPrefix(got, repo) {
			t.Errorf("bashWriteTargets claimed %q for a command-substitution path — it must claim nothing it cannot resolve", got)
		}
	}
}

// TestBashWriteTargets_StillClaimsAPlainRelativeWrite guards the other
// direction: dropping unresolvable paths must not drop ordinary ones. A plain
// relative redirect after a cd is exactly what the guard is for.
func TestBashWriteTargets_StillClaimsAPlainRelativeWrite(t *testing.T) {
	repo := filepath.FromSlash("/repo")
	cmd := "cd /repo && echo hi > notes.txt"

	want := filepath.Join(repo, "notes.txt")
	found := false
	for _, got := range bashWriteTargets(cmd, repo) {
		if got == want {
			found = true
		}
	}
	if !found {
		t.Errorf("bashWriteTargets(%q) did not claim %q — a resolvable relative write must still be seen", cmd, want)
	}
}

// TestBashWriteTargets_StillClaimsAnAbsoluteWriteThatEmbedsAVariableElsewhere
// keeps the rule narrow: the path is unresolvable only when the VARIABLE is
// part of the path. An absolute path is resolvable whatever else the command
// line mentions.
func TestBashWriteTargets_StillClaimsAnAbsoluteWriteThatEmbedsAVariableElsewhere(t *testing.T) {
	repo := filepath.FromSlash("/repo")
	cmd := `cd /elsewhere && MSG="$USER" && echo "$MSG" > /repo/notes.txt`

	want := filepath.Join(repo, "notes.txt")
	found := false
	for _, got := range bashWriteTargets(cmd, repo) {
		if got == want {
			found = true
		}
	}
	if !found {
		t.Errorf("bashWriteTargets(%q) did not claim %q — only the TARGET carrying a variable is unresolvable", cmd, want)
	}
}

// TestBashWriteTargets_ClaimsNothingAfterABareCd covers the other half of the
// same rule. A bare `cd` moves to $HOME, which this scanner cannot read, so
// every LATER relative write in that command line resolves against a
// directory it does not know. Keeping the previous directory instead would
// resolve those writes into the repo the shell started in and block them —
// the same false block an unexpanded variable caused.
func TestBashWriteTargets_ClaimsNothingAfterABareCd(t *testing.T) {
	repo := filepath.FromSlash("/repo")

	for _, cmd := range []string{"cd && echo hi > f.txt", "cd - && echo hi > f.txt"} {
		if got := bashWriteTargets(cmd, repo); len(got) != 0 {
			t.Errorf("bashWriteTargets(%q) = %q, want nothing — a write after an unresolvable cd is not known to be in the repo", cmd, got)
		}
	}
}
