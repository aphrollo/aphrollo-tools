package tdd

import (
	"strings"
)

// isSettledVerdict reports whether a gate.log verdict means a suite ran to
// completion and produced an answer — pass, fail, or a law/lint block — as
// opposed to a run that never finished. Matched by explicit prefix rather
// than by excluding a "not settled" list: an unrecognised verdict must read
// as "no fresh answer", never as one.
func isSettledVerdict(v string) bool {
	return v == "green" || strings.HasPrefix(v, "green-") ||
		v == "red" || strings.HasPrefix(v, "red-") ||
		v == "no-delta" || strings.HasSuffix(v, "-blocked")
}
