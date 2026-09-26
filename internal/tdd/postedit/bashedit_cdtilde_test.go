package postedit

import (
	"path/filepath"
	"testing"
)

// A `cd` operand's own leading tilde used to be resolved as an ordinary
// relative path in this package's third cd-tracking copy too — `cd ~/lane`
// fed commandRunDir (and, through it, bashSnapshotDir) `<cwd>/~/lane`
// instead of the session's actual HOME (issue #867), the same double-join
// #849/#857 already fixed for internal/tdd/shell's write-target scanner and
// internal/tdd/suite's cd-tracking copy. An unquoted leading `~/...` expands
// to HOME the same way here.
func TestCommandRunDir_UnquotedTildeCdTargetExpandsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cmd := `cd ~/lane && echo hi`
	got := commandRunDir(filepath.FromSlash("/somewhere/else"), cmd)
	want := filepath.Join(home, "lane")

	if got != want {
		t.Fatalf("commandRunDir(%q) = %q, want %q", cmd, got, want)
	}
}

// A quoted `cd` operand stays literal, same as any other quoted tilde.
func TestCommandRunDir_QuotedTildeCdTargetStaysLiteral(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd := filepath.FromSlash("/repo")

	cmd := `cd '~/lane' && echo hi`
	got := commandRunDir(cwd, cmd)
	want := filepath.Join(cwd, "~", "lane")

	if got != want {
		t.Fatalf("commandRunDir(%q) = %q, want %q — a quoted tilde must not expand", cmd, got, want)
	}
}

// `cd ~other/...` names a different user's home directory, which this
// scanner cannot resolve, so it falls back to cwd exactly like a bare `cd`
// — a later segment's directory is unknown rather than guessed at.
func TestCommandRunDir_ForeignUserTildeCdTargetFallsBackToCwd(t *testing.T) {
	cwd := filepath.FromSlash("/repo")
	cmd := `cd ~other/x && echo hi`

	if got := commandRunDir(cwd, cmd); got != cwd {
		t.Fatalf("commandRunDir(%q) = %q, want %q — an unresolvable cd is not known to be in any tree", cmd, got, cwd)
	}
}

// bashSnapshotDir is the function actually wired into PreBash/PostBash; the
// fix has to reach it through commandRunDir, not just commandRunDir itself.
func TestBashSnapshotDir_UnquotedTildeCdTargetExpandsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cmd := `cd ~/lane && echo hi`
	got := bashSnapshotDir(filepath.FromSlash("/somewhere/else"), cmd)
	want := filepath.Join(home, "lane")

	if got != want {
		t.Fatalf("bashSnapshotDir(%q) = %q, want %q", cmd, got, want)
	}
}
