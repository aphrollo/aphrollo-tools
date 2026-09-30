package cli

import (
	"flag"
	"fmt"
	"io"
)

// mutFlags is the one mutation model every mutating verb shares: the verb
// executes by default, --dry prints the plan and stops, and --apply is a
// legacy no-op kept for one release so a script written for the old
// preview-by-default model still runs.
type mutFlags struct {
	dry         *bool
	legacyApply *bool
}

// addMutFlags registers --dry and the legacy --apply on fs.
func addMutFlags(fs *flag.FlagSet) mutFlags {
	return mutFlags{
		dry:         fs.Bool("dry", false, "print the plan and stop (default: execute)"),
		legacyApply: fs.Bool("apply", false, "legacy no-op: the verb executes by default (use --dry to preview)"),
	}
}

// parse reads args with flags allowed anywhere, so a flag written after a
// positional is honoured rather than silently left in the positionals, and an
// unknown flag is refused by fs in its own words. It returns the positionals.
// A legacy --apply prints its one-line notice on stderr. verb names the
// command in that notice.
func (m mutFlags) parse(fs *flag.FlagSet, verb string, args []string, stderr io.Writer) ([]string, error) {
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return nil, err
	}
	if *m.legacyApply {
		fmt.Fprintf(stderr, "aphrollo %s: --apply is a no-op, the verb executes by default (--dry previews); the flag is removed next release\n", verb)
	}
	return pos, nil
}

// execute reports whether the verb carries its plan out: everything but --dry.
func (m mutFlags) execute() bool { return !*m.dry }

// refuseArgs reports an unexpected positional for a verb that takes none,
// so a stray word is an error and never a silently dropped argument.
func refuseArgs(verb string, pos []string, stderr io.Writer) bool {
	if len(pos) == 0 {
		return false
	}
	fmt.Fprintf(stderr, "aphrollo %s: unexpected argument %q\n", verb, pos[0])
	return true
}
