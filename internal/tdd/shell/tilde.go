package shell

import (
	"os"
	"path/filepath"
	"strings"
)

// A write target's leading `~` used to be joined onto cwd like any other
// relative operand: `git … > ~/.claude/gate-state/x.patch`, run with cwd the
// primary checkout, resolved to
// <primary>/~/.claude/gate-state/x.patch instead of the session's actual
// HOME (issue #849). This file is the one place `writeTargets` and
// `verbTargets` turn a leading `~` into what bash itself would actually
// write to.

// tildeQuoted reports whether the character at raw's own position 0 came
// from a quoted or backslash-escaped source — shellWordTokens' escapedRune
// sentinel, the same test splitRedirect already uses to tell a real `>` from
// one that only looks like one once quotes are stripped (issue #725). An
// empty raw reads as quoted, the conservative default: nothing here claims
// an unquoted `~` was ever seen.
func tildeQuoted(raw string) bool {
	r := []rune(raw)
	return len(r) == 0 || r[0] == escapedRune
}

// expandTildeTarget answers what a write target this scanner recognises
// actually resolves to, given raw's own leading rune to judge whether text
// came from a quoted or backslash-escaped source. Bash itself never
// tilde-expands a quoted or escaped `~` — `'~/x'` names the literal
// two-character sequence `~/x`, an ordinary relative path like any other —
// so a quoted leading `~` is returned unchanged and joins onto cwd exactly
// the way it always did. An unquoted leading `~` is answered by expandTilde.
func expandTildeTarget(raw, text string) (resolved string, keep bool) {
	if tildeQuoted(raw) {
		return text, true
	}
	return expandTilde(text)
}

// expandTildeTargets applies expandTildeTarget to every word in order,
// dropping any a foreign user's tilde (or an undeterminable HOME) refuses to
// resolve — every verbTargets case returns a flat []string, and this is
// where a quote-aware []shellWord operand list becomes one.
func expandTildeTargets(words []shellWord) []string {
	var out []string
	for _, w := range words {
		if resolved, keep := expandTildeTarget(w.raw, w.text); keep {
			out = append(out, resolved)
		}
	}
	return out
}

// expandTilde expands p's leading `~` (exactly "~", the whole of HOME, or
// "~/...") to the session's HOME, the way bash expands an unquoted tilde
// word — and once expanded the result is absolute, so resolveAgainst's own
// IsAbs check keeps it from ever being re-joined onto cwd. p with no leading
// `~` at all is returned unchanged.
//
// `~user` and `~user/...` — a different user's home directory — are
// answered the way unresolvable() answers a variable or a command
// substitution this scanner does not evaluate: ok=false, dropped exactly
// like any other operand it cannot resolve rather than guessed at or joined
// onto cwd as though it were a relative path. ok=false also covers a HOME
// this process cannot determine at all.
func expandTilde(p string) (resolved string, ok bool) {
	switch {
	case p == "~", strings.HasPrefix(p, "~/"):
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", false
		}
		if p == "~" {
			return home, true
		}
		return filepath.Join(home, filepath.FromSlash(p[2:])), true
	case strings.HasPrefix(p, "~"):
		return "", false
	default:
		return p, true
	}
}
