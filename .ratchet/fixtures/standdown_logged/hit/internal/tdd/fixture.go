package fixture

import (
	"fmt"
	"os"
)

// checkStage mirrors precommit_gateroot.go's pre-#320 shape: a genuine
// stand-down that prints to stderr and then returns with nothing recorded
// in gate.log — see .ratchet/laws/standdown_logged.toml.
func checkStage(gateName, root string) bool {
	fmt.Fprintf(os.Stderr, "gate %s: check → skipped (no cargo package owns anything staged)\n", gateName)
	return false
}
