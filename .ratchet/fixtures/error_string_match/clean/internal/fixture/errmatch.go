package fixture

import (
	"errors"
	"strings"
)

var errNotFound = errors.New("not found")

// classifyBySentinel is the sanctioned shape: a sentinel checked with
// errors.Is, immune to a message that gains a prefix or a wrapper.
func classifyBySentinel(err error) bool {
	return errors.Is(err, errNotFound)
}

// classifyGitStderr is the genuine exception: git has no typed error for an
// unresolved ref, so its own stderr text is the only signal, and the escape
// says so on the line.
func classifyGitStderr(err error) bool {
	return strings.Contains(err.Error(), "unknown revision") // error-text-ok: git has no typed error for an unresolved ref
}
