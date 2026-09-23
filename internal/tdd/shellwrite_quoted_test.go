package tdd

import (
	"path/filepath"
	"testing"
)

// A `>` or `>>` inside a quoted argument is data, not shell syntax — issue
// #725. `grep -n "<<<<<<<\|=======\|>>>>>>>" file` searches for a merge
// conflict marker; it writes nothing. The PreToolUse hook refused it anyway
// because writeTargets classified the QUOTED pattern's own `>>>>>>>` as a
// redirect once the tokenizer had already stripped the quotes that marked it
// as an argument.
func TestBashWriteTargets_QuotedRedirectCharsAreNotAWrite(t *testing.T) {
	t.Parallel()
	repo := filepath.FromSlash("/repo")
	cmds := []string{
		`grep -n "<<<<<<<\|=======\|>>>>>>>" decisions.md`,
		`grep -n ">>>>>>> branch" decisions.md`,
		`echo ">file"`,
		`grep -n "5>out" decisions.md`,
	}
	for _, cmd := range cmds {
		if got := bashWriteTargets(cmd, repo); len(got) != 0 {
			t.Errorf("bashWriteTargets(%q) = %q, want no targets — the `>` is inside a quoted argument, not a redirect", cmd, got)
		}
	}
}

// A backslash-escaped `>` outside quotes is the same case by the same rule:
// the shell hands the command a literal `>`, never a redirect.
func TestBashWriteTargets_BackslashEscapedRedirectCharIsNotAWrite(t *testing.T) {
	t.Parallel()
	repo := filepath.FromSlash("/repo")
	cmd := `echo \>notes.txt`
	if got := bashWriteTargets(cmd, repo); len(got) != 0 {
		t.Errorf("bashWriteTargets(%q) = %q, want no targets — the `>` is backslash-escaped, not a redirect", cmd, got)
	}
}

// The other half of the same property: a genuine, unquoted redirect must
// still be caught — this rule only exempts a `>` the shell itself would
// never treat as an operator.
func TestBashWriteTargets_StillCatchesAnUnquotedRedirectNextToQuotedText(t *testing.T) {
	t.Parallel()
	repo := filepath.FromSlash("/repo")
	cmd := `echo "safe > text" > notes.txt`
	want := filepath.Join(repo, "notes.txt")
	found := false
	for _, got := range bashWriteTargets(cmd, repo) {
		if got == want {
			found = true
		}
	}
	if !found {
		t.Errorf("bashWriteTargets(%q) did not claim %q — the unquoted redirect after the quoted text must still be caught", cmd, want)
	}
}
