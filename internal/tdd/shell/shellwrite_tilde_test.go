package shell

import (
	"path/filepath"
	"testing"
)

// A write target's leading `~` was joined onto cwd like any other relative
// operand — `git stash show -p stash@{0} > ~/.claude/gate-state/x.patch`,
// run with cwd the primary checkout, resolved to
// <primary>/~/.claude/gate-state/x.patch instead of the session's actual
// HOME. An unquoted leading `~` (exactly "~" or "~/...") expands to HOME the
// way bash expands it, and once expanded it is absolute, so it is never
// joined onto cwd at all.
func TestBashWriteTargets_UnquotedTildeExpandsToHomeNotCwd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd := filepath.FromSlash("/home/debian/spaces/aphrollo/aphrollo-tools")

	cmd := `git stash show -p stash@{0} > ~/.claude/gate-state/x.patch`
	got := bashWriteTargets(cmd, cwd)
	want := filepath.Join(home, ".claude", "gate-state", "x.patch")

	if len(got) != 1 || got[0] != want {
		t.Fatalf("bashWriteTargets(%q, %q) = %v, want [%q]", cmd, cwd, got, want)
	}
}

// The same expansion applies after a `cd` segment: the write target's own
// tilde is what must expand, regardless of what directory the command cd'd
// into first.
func TestBashWriteTargets_UnquotedTildeAfterCdStillExpandsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	primary := filepath.FromSlash("/home/debian/spaces/aphrollo/aphrollo-tools")

	cmd := "cd " + primary + " && echo x > ~/f"
	got := bashWriteTargets(cmd, "/somewhere/else")
	want := filepath.Join(home, "f")

	if len(got) != 1 || got[0] != want {
		t.Fatalf("bashWriteTargets(%q) = %v, want [%q]", cmd, got, want)
	}
}

// A QUOTED tilde is bash's own signal not to expand it: `'~/x'` names the
// literal two-character sequence `~/x`, an ordinary relative path like any
// other, so it stays literal and joins onto cwd exactly as any other
// relative target would.
func TestBashWriteTargets_QuotedTildeStaysLiteralAndJoinsOnCwd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd := filepath.FromSlash("/repo")

	cmd := `echo x > '~/f'`
	got := bashWriteTargets(cmd, cwd)
	want := filepath.Join(cwd, "~", "f")

	if len(got) != 1 || got[0] != want {
		t.Fatalf("bashWriteTargets(%q) = %v, want [%q] — a quoted tilde must not expand", cmd, got, want)
	}
}

// `~user/...` names a different user's home directory, which this scanner
// does not evaluate — the same fail-open answer unresolvable() already gives
// a variable or a command substitution it cannot read either: dropped
// rather than guessed at, never joined onto cwd as though it were a
// relative operand.
func TestBashWriteTargets_ForeignUserTildeIsDroppedNotJoinedOnCwd(t *testing.T) {
	cwd := filepath.FromSlash("/repo")
	cmd := `echo x > ~otheruser/f`
	if got := bashWriteTargets(cmd, cwd); len(got) != 0 {
		t.Fatalf("bashWriteTargets(%q) = %v, want no targets — a foreign user's home is outside this scanner's business", cmd, got)
	}
}

// A bare `~` alone (no trailing slash) is the whole of HOME.
func TestBashWriteTargets_BareTildeAloneIsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cmd := `cp f ~`
	got := bashWriteTargets(cmd, filepath.FromSlash("/repo"))
	if len(got) != 1 || got[0] != filepath.Clean(home) {
		t.Fatalf("bashWriteTargets(%q) = %v, want [%q]", cmd, got, filepath.Clean(home))
	}
}

// The destination operand of cp/mv/tee/sed -i is as much a write target as a
// redirect, and meets the exact same double-join bug when it carries an
// unquoted tilde.
func TestBashWriteTargets_TildeDestinationOfCpExpandsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cmd := `cp notes.txt ~/notes.txt`
	got := bashWriteTargets(cmd, filepath.FromSlash("/repo"))
	want := filepath.Join(home, "notes.txt")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("bashWriteTargets(%q) = %v, want [%q]", cmd, got, want)
	}
}
