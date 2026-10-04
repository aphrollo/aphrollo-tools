package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/aphrollo/aphrollo-tools/internal/measure"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

const whyUsage = `usage: aphrollo why <seq> [--repo <path>] [--json]

Replays one event of the repo's event log (the current repo by default), by the
seq 'aphrollo stats --json' and the log lines show. Read-only.

  a deny        the rule, its cause, the recorded detail, the override it offered,
                whether an override followed within 10 minutes (a wrong block) and
                whether the agent complied, this rule's denies, overrides, wrong
                blocks and compliance over the log, and, when the kernel's rule
                table has the rule, its level, section and holdout arm
  a run.result  the verdict, cause, tree and the edit-to-verdict latency, and the
                not-tested cause when the run proved nothing
  any other     the record as logged

Shadow catches and passes are not recorded yet, and are said so, never zero.
A seq the log does not hold exits 1.

  --json    the answer as JSON
`

// runWhy is `aphrollo why <seq>`: one event of the repo's log, replayed.
func runWhy(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("why", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, whyUsage) }
	repo := fs.String("repo", ".", "repository path")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprint(stderr, whyUsage)
		return 2
	}
	seq, err := strconv.ParseInt(pos[0], 10, 64)
	if err != nil || seq <= 0 {
		fmt.Fprintf(stderr, "aphrollo why: %q is not an event seq (a positive integer)\n", pos[0])
		return 2
	}
	if _, err := os.Stat(*repo); err != nil {
		fmt.Fprintf(stderr, "aphrollo why: --repo: %v\n", err)
		return 2
	}
	note := horizonNote(*repo)
	w, ok := measure.Explain(core.ReadEvents(*repo), seq)
	if !ok {
		fmt.Fprintf(stderr, "aphrollo why: no event with seq %d in the log %s\n", seq, core.EventLogDir(*repo))
		if note != "" {
			fmt.Fprintln(stderr, note)
		}
		return 1
	}
	if *asJSON {
		if note != "" {
			fmt.Fprintln(stderr, note)
		}
		return printJSON(w, stdout, stderr)
	}
	fmt.Fprint(stdout, w.Text())
	if note != "" {
		fmt.Fprintln(stdout, note)
	}
	return 0
}
