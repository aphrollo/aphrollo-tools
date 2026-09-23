package core

import (
	"strings"
)

// Outcome is the classified result of a test run after an edit. It is the
// signal PostToolUse reports back to the model. The vocabulary is deliberately
// small: enough to tell "keep going" from "you broke something" from "your
// test can't fail", without the speculative sub-categories that made the
// original classifier brittle.
type Outcome string

// IsRed reports whether the outcome is actionable failure the agent should see.
// PostToolUse is silent unless the outcome IsRed, so green/writing-test/no-delta
// runs add no noise.
func (o Outcome) IsRed() bool {
	return strings.HasPrefix(string(o), "red")
}
