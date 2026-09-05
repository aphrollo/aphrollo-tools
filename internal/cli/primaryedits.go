package cli

import (
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGatePrimaryEdits waives or restores the primary-checkout merge-only rule
// for the current session. Pre-rename spelling, retiring next release: it
// dispatches through the exact same code as `gate allow primary` / `gate
// revoke primary`, not a second copy that happens to agree.
//
// The refusal has always named two overrides, and from inside a turn neither
// worked: `/tdd primary-edits on` is a UserPromptSubmit hook, so it fires only
// on text a person types, and APHROLLO_PRIMARY_EDITS is read by the hook
// process rather than by the shell that would export it. This is the route
// that works from where the refusal is actually read.
func runGatePrimaryEdits(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		fmt.Fprintln(stderr, "usage: aphrollo gate primary-edits on|off (alias; retiring next release)")
		return 2
	}
	if args[0] == "on" {
		return runGateAllow([]string{tdd.WallPrimary}, stdout, stderr)
	}
	return runGateRevoke([]string{tdd.WallPrimary}, stdout, stderr)
}
