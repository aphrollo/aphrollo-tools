package tdd

import (
	"strings"
)

// LogToken makes one field safe for a whitespace-separated log line: the
// stats parser reads fields, and a pattern or path carrying a space would
// silently shift the verdict column. Exported for a caller outside this
// package that appends its own field (a cwd, an argv, a reason) to a gate.log
// line via AppendGateLog and must keep that field whitespace-safe the same way
// this package's own callers do.
func LogToken(s string) string {
	if s = strings.Join(strings.Fields(s), "_"); s == "" {
		return "-"
	}
	return s
}
