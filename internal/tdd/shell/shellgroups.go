package shell

import "strings"

// unwrapGroups strips the group syntax that rides on a segment's first and
// last words: a `(` (subshell) or `{` opening it, and `)` closing it. The
// tokenizer splits on separators and blanks only, so `(cd dir && go test)`
// arrives as the segments [`(cd` `dir`] and [`go` `test)`]. It returns the
// segment without those marks, how many subshells the segment opened, and how
// many it closed. A `)` counts as a close only while a subshell is open
// (openDepth plus the ones this segment opened) and only when the word holds
// more `)` than `(`, so a `$(pwd)` operand is never mistaken for one. A `{`
// or `}` word is dropped but never counted: a brace group runs in the
// current shell, so nothing inside it is scoped to it.
func unwrapGroups(seg []shellWord, openDepth int) (inner []shellWord, opened, closed int) {
	inner = seg
	// walk-terminates: every turn drops the first word and re-adds at most its text minus one byte, so the total length shrinks
	for len(inner) > 0 {
		first := inner[0]
		if first.text == "{" || first.text == "}" {
			inner = inner[1:]
			continue
		}
		if !strings.HasPrefix(first.text, "(") {
			break
		}
		opened++
		inner = inner[1:]
		if len(first.text) > 1 {
			inner = append([]shellWord{{text: first.text[1:], raw: first.raw[1:]}}, inner...)
		}
	}
	for len(inner) > 0 && closed < openDepth+opened {
		last := inner[len(inner)-1]
		if !strings.HasSuffix(last.text, ")") || strings.Count(last.text, ")") <= strings.Count(last.text, "(") {
			break
		}
		closed++
		inner = inner[: len(inner)-1 : len(inner)-1]
		if len(last.text) > 1 {
			inner = append(inner, shellWord{text: last.text[:len(last.text)-1], raw: last.raw[:len(last.raw)-1]})
		}
	}
	return inner, opened, closed
}
