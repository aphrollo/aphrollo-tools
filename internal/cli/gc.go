package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runTDDGC is `aphrollo tdd gc`: report (default) or reclaim (--apply) the
// stale build directories this binary's own gates create and use. Dry-run by
// default, like every other tdd/refactor mutation — a disk sweep that
// deletes without being asked is the one failure this feature cannot have.
//
// --quiet is what the detached session-start sweep runs with: it has nowhere
// to print, so it stays silent and leaves its result in the state dir for
// the next session start to surface as one line.
func runTDDGC(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		repo      = fs.String("repo", ".", "workspace whose target dir to sweep")
		olderThan = fs.String("older-than", "3d", "reclaim incremental caches idle longer than this (e.g. 3d, 12h)")
		apply     = fs.Bool("apply", false, "delete the candidates (default: print them and stop)")
		quiet     = fs.Bool("quiet", false, "print nothing (the detached session-start sweep)")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	age, err := tdd.ParseGCAge(*olderThan)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo tdd gc: %v\n", err)
		return 2
	}

	cands := tdd.ScanGC(*repo, age, tdd.AllGCScopes())
	if !*apply {
		if !*quiet {
			fmt.Fprint(stdout, tdd.RenderGC(cands, false, 0))
		}
		return 0
	}

	freed, refused, skipped := tdd.ApplyGCFor(*repo, cands)
	tdd.RecordGCSweep(freed, len(cands)-skipped-len(refused))
	if *quiet {
		return 0
	}
	fmt.Fprint(stdout, tdd.RenderGC(cands, true, freed))
	if skipped > 0 {
		fmt.Fprintf(stdout, "%d incremental cache(s) left for next time — a build holds every slot for this target dir\n", skipped)
	}
	for _, r := range refused {
		fmt.Fprintf(stderr, "aphrollo tdd gc: refused %s\n", r)
	}
	return 0
}
