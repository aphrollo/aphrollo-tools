package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGateMutantsHold is `gate mutants hold <file>...`: the declaration step of
// a HAND mutation proof run by editor rather than by `mutants prove`. It
// copies each file's current WORKING bytes into the session's hold store, so
// the restore afterwards — `MUTATION=1 git checkout -- <file>`, served by the
// git shim from those bytes — puts back the state the proof started from
// rather than what the index holds. Mid-lane the two differ by exactly the
// uncommitted work `git checkout --` was deleting (issue #650).
//
// Every named file is checked readable BEFORE the first hold is taken: a
// proof that believes half its files are restorable is the failure this verb
// exists to prevent, and a partial hold reads identically to a complete one
// at restore time.
func runGateMutantsHold(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: aphrollo gate mutants hold <file>... — takes the pre-mutation working state of each file, "+
			"so MUTATION=1 git checkout -- <file> can restore it afterwards")
		return 2
	}
	for _, path := range args {
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo gate mutants hold: cannot read %s to hold its working state: %v; nothing was held\n", path, err)
			return 1
		}
		_ = f.Close()
	}
	for _, path := range args {
		hold, err := tdd.HoldMutation(path)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo gate mutants hold: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "gate: mutation proof — holding %s (%d bytes); restore it with MUTATION=1 git checkout -- %s\n",
			hold.Path, hold.Size, path)
	}
	return 0
}
