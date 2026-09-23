package tdd

import (
	"strings"
)

// applyEdit is the Edit tool's own substitution: the first occurrence, or every
// one under replace_all. An empty old string means the edit creates the file.
func applyEdit(content, old, replacement string, all bool) string {
	if old == "" {
		return replacement
	}
	if all {
		return strings.ReplaceAll(content, old, replacement)
	}
	return strings.Replace(content, old, replacement, 1)
}
