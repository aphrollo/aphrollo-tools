package suite

import (
	"path/filepath"
	"testing"
)

// A `cd` operand's own leading tilde used to be resolved as an ordinary
// relative path in this package's separate cd-tracking copy too — `cd
// ~/lane && go test ./...` was judged against cwd/~/lane instead of the
// session's actual HOME (issue #867), the same double-join #849 already
// fixed for the write-target gate. An unquoted leading `~/...` expands to
// HOME the same way there.
func TestSuiteRunDirs_UnquotedTildeCdTargetExpandsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cmd := `cd ~/lane && go test ./...`
	got := suiteRunDirs(filepath.FromSlash("/somewhere/else"), cmd)
	want := filepath.Join(home, "lane")

	if len(got) != 1 || got[0] != want {
		t.Fatalf("suiteRunDirs(%q) = %v, want [%q]", cmd, got, want)
	}
}

// A quoted `cd` operand stays literal, same as any other quoted tilde.
func TestSuiteRunDirs_QuotedTildeCdTargetStaysLiteral(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd := filepath.FromSlash("/repo")

	cmd := `cd '~/lane' && go test ./...`
	got := suiteRunDirs(cwd, cmd)
	want := filepath.Join(cwd, "~", "lane")

	if len(got) != 1 || got[0] != want {
		t.Fatalf("suiteRunDirs(%q) = %v, want [%q] — a quoted tilde must not expand", cmd, got, want)
	}
}

// A leading `FOO=bar` env assignment must not stop the operand after `cd`
// from being found, or its own tilde from being expanded — the same rule as
// TestSuiteRunDirs_UnquotedTildeCdTargetExpandsToHome, just with
// dropLeadingEnvAssignmentWords in front of cdTargetTilde.
func TestSuiteRunDirs_EnvPrefixedUnquotedTildeCdTargetExpandsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cmd := `FOO=bar cd ~/lane && go test ./...`
	got := suiteRunDirs(filepath.FromSlash("/somewhere/else"), cmd)
	want := filepath.Join(home, "lane")

	if len(got) != 1 || got[0] != want {
		t.Fatalf("suiteRunDirs(%q) = %v, want [%q]", cmd, got, want)
	}
}

// A segment that is ENTIRELY env assignments (no `cd`, no runner, nothing
// after the last `=`) exercises dropLeadingEnvAssignmentWords' own loop
// bound at its edge: i walks off the end of texts, so an off-by-one there
// reads one past the slice instead of stopping.
func TestSuiteRunDirs_AllEnvAssignmentSegmentNamesNoInvocation(t *testing.T) {
	cwd := filepath.FromSlash("/repo")
	cmd := `FOO=bar`
	if got := suiteRunDirs(cwd, cmd); len(got) != 0 {
		t.Fatalf("suiteRunDirs(%q) = %v, want none — a bare env assignment names no suite invocation", cmd, got)
	}
}

// `cd ~other/...` names a different user's home directory, which this
// scanner cannot resolve, so it is dropped exactly like a bare `cd` — the
// later suite invocation's directory is unknown rather than guessed at.
func TestSuiteRunDirs_ForeignUserTildeCdTargetIsDropped(t *testing.T) {
	cwd := filepath.FromSlash("/repo")
	cmd := `cd ~other/x && go test ./...`
	got := suiteRunDirs(cwd, cmd)
	if len(got) != 1 || got[0] != "" {
		t.Fatalf("suiteRunDirs(%q) = %v, want [\"\"] — a run after an unresolvable cd is not known to be in any tree", cmd, got)
	}
}
