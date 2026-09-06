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

// notAStandDown mentions "skipped" only in a comment, never in a string
// literal a stand-down would actually print — code_only strips it before the
// trigger ever sees it.
func notAStandDown() {
	// a skipped test reports green while proving nothing
}
