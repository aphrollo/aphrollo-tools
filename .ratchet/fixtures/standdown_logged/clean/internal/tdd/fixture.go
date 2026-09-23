package fixture

import (
	"fmt"
	"os"
)

// checkStageLogged is the fixed shape: the stand-down message is paired with
// an appendGateLog call within the window, so the stand-down is counted, not
// just printed.
func checkStageLogged(gateName, root string) bool {
	fmt.Fprintf(os.Stderr, "gate %s: check → skipped (no cargo package owns anything staged)\n", gateName)
	appendGateLog(gateName, root, "", "clippy-scope-empty-skipped", 0)
	return false
}

// checkStageDeferred is the genuine case: the message and the eventual
// appendGateLog call are far apart because a switch sits between them, and
// the escape says where the line actually lands.
func checkStageDeferred(gateName, root string) bool {
	verdict := "inconclusive (fail-open)" // standdown-logged: passed to appendGateLog after the switch below
	switch root {
	case "":
		verdict = "vacuous-rejected"
	}
	appendGateLog(gateName, root, "", verdict, 0)
	return false
}

// foreignStagedLogged is bashedit.go's shape and the one #658 read as an
// offence: the appendGateLog call is a CODE token on the line DIRECTLY ABOVE
// the message it records, so a marker walk that tests the comment run first
// stops before ever matching it.
func foreignStagedLogged(gateName, root string) string {
	appendGateLog(gateName, root, "", "foreign-staged-skipped", 0)
	return fmt.Sprintf("gate: → skipped in %s (the code was NOT tested)", root)
}

// notAStandDown mentions "skipped" only in a comment, never in a string
// literal a stand-down would actually print — code_only strips it before the
// trigger ever sees it.
func notAStandDown() {
	// a skipped test reports green while proving nothing
}

// checkStageLoggedQualified is checkStageLogged's shape after the split
// moves appendGateLog into another package (`core`): the stand-down message
// is paired with a QUALIFIED, capitalized `core.AppendGateLog` call within
// the window, which must count exactly like the unqualified lowercase call
// does.
func checkStageLoggedQualified(gateName, root string) bool {
	fmt.Fprintf(os.Stderr, "gate %s: check → skipped (no cargo package owns anything staged)\n", gateName)
	core.AppendGateLog(gateName, root, "", "clippy-scope-empty-skipped", 0)
	return false
}
