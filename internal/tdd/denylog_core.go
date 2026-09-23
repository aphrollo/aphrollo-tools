package tdd

import (
	"strings"
)

// logToken makes one field safe for a whitespace-separated log line: the
// stats parser reads fields, and a pattern or path carrying a space would
// silently shift the verdict column.
func logToken(s string) string {
	if s = strings.Join(strings.Fields(s), "_"); s == "" {
		return "-"
	}
	return s
}
