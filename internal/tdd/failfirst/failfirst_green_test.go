package failfirst

import (
	"strings"
	"testing"
)

// A GREEN run that failed without a parseable test name (a compile error
// the staged change itself introduced, say) still refuses, and the message
// says the selection is what failed rather than naming nothing.
func TestStillRedMessage_NamesTheSelectionWhenNoFailingTestParses(t *testing.T) {
	g := greenProof{ran: true, res: SuiteResult{Passed: false, Output: "error[E0425]: cannot find value `x` in this scope\n"}}

	msg := stillRedMessage("/r", "cargo test -p m --test latches", g)

	if !strings.Contains(msg, "still RED with the staged change: the selected tests.") {
		t.Fatalf("with no test name to report, the refusal must name the selection, got: %s", msg)
	}
}
