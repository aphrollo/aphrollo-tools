package ratchet

import "fmt"

// How a Result reports itself: whether it blocks, the lines it renders, and
// the remedy and escape wording a rejection carries. Split from check.go,
// which decides what the findings ARE — this file only says how they read.

func lineModeNotes(law string, ceiling, measured map[string]int) []string {
	var notes []string
	for _, key := range sortedKeys(ceiling) {
		if base, m := ceiling[key], measured[key]; base > m {
			notes = append(notes, fmt.Sprintf(
				"%s: %s baseline is %d, code counting now measures %d — regenerate by running without --no-tighten",
				law, key, base, m))
		}
	}
	return notes
}

// NewerLaw is one law read leniently, with the version it declared.
type NewerLaw struct {
	Name   string `json:"law"`
	Schema int    `json:"schema"`
}

// SkippedLaw is one law this binary could not judge at all: its
// `[matcher].kind` is not in the compiled matcherKeys table, so nothing ran
// for it. See UnknownMatcherKindError and Law.UnknownKind.
type SkippedLaw struct {
	Name string `json:"law"`
	Kind string `json:"kind"`
}

// Blocked reports whether any deny law regressed — the exit-1 condition.
func (r Result) Blocked() bool {
	for _, f := range r.Findings {
		if f.Severity == Deny.String() {
			return true
		}
	}
	return false
}

// maxFindingLine is the width one hit gets. A denial is read in a terminal
// and in a hook envelope; past this the remedy at the END of the line is the
// part that scrolls away, which is the part that matters.
const maxFindingLine = 160

// Lines renders one output line per finding: where, what, the counts, and the
// way through. The OFFENDING TEXT is what gets truncated when the line is too
// long — never the remedy.
func (r Result) Lines() []string {
	out := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		where := f.File
		if f.Line > 0 {
			where = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		head := fmt.Sprintf("%s: %s ", f.Law, where)
		tail := fmt.Sprintf(" (baseline %d, now %d)", f.Baseline, f.Measured)
		if f.Remedy != "" {
			tail += " — " + f.Remedy
		}
		out = append(out, head+fitWhat(f.What, maxFindingLine-len([]rune(head))-len([]rune(tail)))+tail)
	}
	return out
}

// fitWhat shortens the offending text to at most n runes, marking that it was
// cut. A budget too small to say anything yields nothing rather than a line of
// ellipsis.
func fitWhat(what string, n int) string {
	runes := []rune(what)
	if len(runes) <= n {
		return what
	}
	if n < 4 {
		return ""
	}
	return string(runes[:n-1]) + "…"
}

// remedyFor is the one imperative sentence a denial ends with. A count-keyed
// law is not escaped, it is paid down, and the number it is paid down TO is
// what the reader needs; a law with no escape has exactly one way through,
// and saying nothing there reads as "there must be a marker somewhere".
func remedyFor(law Law) string {
	if law.Matcher.Kind == KindLineCount {
		return fmt.Sprintf("split the file; the ceiling is %d", law.Matcher.Max)
	}
	if law.Matcher.Kind == KindSymbolRemoved {
		return fmt.Sprintf("test removed without a tombstone; add `// ratchet: %s <name>: <why>` where it stood — or, when the whole test FILE went away with its subject, one `// ratchet: %s <path>: <why>` anywhere in scope — or restore it", law.Name, law.Name)
	}
	if law.Matcher.Kind == KindCoChange {
		return fmt.Sprintf("escape: %s <why> on the marker line, or update the twin", coChangeEscapeToken)
	}
	if law.Matcher.Kind == KindHunkRegex && law.Matcher.NameGroup {
		return "add a `Removes-test: <name>: <why>` trailer to the commit message, or restore the declaration"
	}
	if law.Matcher.Kind == KindMarkerWithinLines {
		// The marker is what vouches for the declaration, and it may sit on
		// the trigger's OWN line at no line cost — a separate line above it
		// grows the file instead, which in a file already at its ceiling
		// raises a module_size baseline and turns a fixed hit into a new
		// one (issue #652).
		return fmt.Sprintf("add `%s <why>` — on the trigger's own line costs no lines; a line above it does and can raise a module_size baseline", law.Matcher.Marker.String())
	}
	if law.Escape == "" {
		return "no escape: lower the code"
	}
	if law.Matcher.Kind == KindDepGraphCeiling {
		// A whole-root measurement is filed at line 0, so escapeWindow's
		// "on the line", the only window it knows, names a position that does
		// not exist. The kind reads its escape from the ROOT's own manifest,
		// in any `#` comment line of it.
		return fmt.Sprintf("escape: a `# %s <why>` comment line anywhere in <root>/Cargo.toml", law.Escape)
	}
	return fmt.Sprintf("escape: %s <why> %s", law.Escape, escapeWindow(law))
}

// escapeWindow says WHERE the escape comment is allowed to sit, in the words
// the author needs: advice that names a window the law does not accept is
// worse than none.
func escapeWindow(law Law) string {
	if law.Contiguous || law.EscapeLines > 0 {
		return "on the line or the line above"
	}
	return "on the line"
}

// Check scans the laws' scope once, applies every law, and compares each
// law's measured hits to its baseline.
