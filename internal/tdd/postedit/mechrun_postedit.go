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

// redLineBudget is the most bytes a red gate line may carry: 400 tokens by the
// repo's own brief counter (measure.Tokens is bytes plus three, over four).
const redLineBudget = 400*4 - 3

const truncatedHead, truncatedTail = "\n…[truncated]", "…[truncated]\n"

// headWithin is the first lines of s that fit in limit bytes, with a marker
// where the rest was cut. s whole when it fits.
func headWithin(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	keep := max(limit-len(truncatedHead), 0)
	cut := s[:keep]
	if nl := strings.LastIndexByte(cut, '\n'); nl > 0 {
		cut = cut[:nl]
	}
	return cut + truncatedHead
}

// tailWithin is the last lines of s that fit in limit bytes, with a marker
// where the start was cut: a failure's detail is at the end of a run.
func tailWithin(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	keep := max(limit-len(truncatedTail), 0)
	cut := s[len(s)-keep:]
	if nl := strings.IndexByte(cut, '\n'); nl >= 0 && nl+1 < len(cut) {
		cut = cut[nl+1:]
	}
	return truncatedTail + cut
}
