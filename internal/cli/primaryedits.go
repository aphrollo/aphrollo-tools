package cli

import (
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGatePrimaryEdits waives or restores the primary-checkout merge-only rule
// for the current session.
//
// The refusal has always named two overrides, and from inside a turn neither
// worked: `/tdd primary-edits on` is a UserPromptSubmit hook, so it fires only
// on text a person types, and APHROLLO_PRIMARY_EDITS is read by the hook
// process rather than by the shell that would export it. This is the route
// that works from where the refusal is actually read.
func runGatePrimaryEdits(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		fmt.Fprintln(stderr, "usage: aphrollo gate primary-edits on|off")
		return 2
	}
	msg, err := tdd.SetPrimaryEditsForEnvSession(args[0] == "on")
	if err != nil {
		fmt.Fprintln(stderr, "aphrollo gate primary-edits: "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, msg)
	return 0
}
