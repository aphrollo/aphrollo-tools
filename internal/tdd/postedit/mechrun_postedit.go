package postedit

import (
	"strings"
)

// tailSnippet bounds runner output to its LAST maxSnippet chars — the mirror
// of snippet(): a suite's failure detail (the FAILED lines, the panic, the
// assertion diff) accumulates at the end of the run, so a bounded rejection
// must keep the tail and drop the head.
func tailSnippet(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxSnippet {
		return s
	}
	return "…[truncated]\n" + s[len(s)-maxSnippet:]
}
