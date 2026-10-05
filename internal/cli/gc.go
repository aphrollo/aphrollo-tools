package cli

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// runGateGC is `aphrollo gate gc`: reclaim the stale build directories this
// binary's own gates create and use, or with --dry report them and stop.
//
// --quiet is what the detached session-start sweep runs with: it has nowhere
// to print, so it stays silent and leaves its result in the state dir for
// the next session start to surface as one line.
func runGateGC(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		repo      = fs.String("repo", ".", "workspace whose target dir to sweep")
		olderThan = fs.String("older-than", "3d", "reclaim incremental caches idle longer than this (e.g. 3d, 12h)")
		quiet     = fs.Bool("quiet", false, "print nothing (the detached session-start sweep)")
		lockAge   = fs.String("lock-age", "1d", "reclaim unheld aphrollo lock files idle longer than this")
		known     = fs.Bool("known", false, "also sweep every repo the gate has worked in lately (the detached session-start sweep)")
		mut       = addMutFlags(fs)
	)
	pos, err := mut.parse(fs, "gate gc", args, stderr)
	if err != nil || refuseArgs("gate gc", pos, stderr) {
		return 2
	}
	apply := mut.execute()
	age, err := tdd.ParseGCAge(*olderThan)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate gc: %v\n", err)
		return 2
	}

	scope, err := gcScopeFromFlags(*lockAge)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate gc: %v\n", err)
		return 2
	}

	cacheSettings, err := tdd.ReadGoCacheSettings(*repo)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate gc: %v\n", err)
		return 2
	}
	if cacheSettings.Warning != "" {
		fmt.Fprintf(stderr, "aphrollo gate gc: warning: %s\n", cacheSettings.Warning)
	}

	repos := []string{*repo}
	if *known {
		repos = append(repos, tdd.KnownGCRepos()...)
	}
	// The OS temp dirs are the same for every repo, so their scratch is swept
	// once, on its own, and every repo's own areas are swept without it. Each
	// is scanned and then applied before the next, so a later scan sees the
	// earlier one's deletions.
	scratch := tdd.GCScope{TempScratch: scope.TempScratch}
	scope.TempScratch = false
	var (
		cands   []tdd.GCCandidate
		freed   int64
		refused []string
		skipped int
	)
	sweep := func(r string, sc tdd.GCScope) {
		found := tdd.ScanGC(r, age, sc)
		cands = append(cands, found...)
		if !apply {
			return
		}
		f, ref, sk := tdd.ApplyGCFor(r, found)
		freed += f
		refused = append(refused, ref...)
		skipped += sk
	}
	sweep(*repo, scratch)
	for _, r := range repos {
		sweep(r, scope)
	}
	cacheLine := sweepGoCache(cacheSettings, apply, *known, *quiet)
	if !apply {
		if !*quiet {
			fmt.Fprint(stdout, tdd.RenderGC(cands, false, 0))
			fmt.Fprint(stdout, cacheLine)
			retainState(repos, true, stdout, stderr)
			writeMutantsInUse(stdout)
			writeProbeBackups(stdout)
		}
		return 0
	}

	if *quiet {
		retainState(repos, false, io.Discard, stderr)
		tdd.RecordGCSweep(freed, len(cands)-skipped-len(refused))
		return 0
	}
	tdd.RecordGCSweep(freed, len(cands)-skipped-len(refused))
	retainState(repos, false, stdout, stderr)
	fmt.Fprint(stdout, tdd.RenderGC(cands, true, freed))
	fmt.Fprint(stdout, cacheLine)
	writeMutantsInUse(stdout)
	writeProbeBackups(stdout)
	if skipped > 0 {
		fmt.Fprintf(stdout, "%d candidate(s) inside the target dir left for next time — a build holds every slot for this target dir\n", skipped)
	}
	for _, r := range refused {
		fmt.Fprintf(stderr, "aphrollo gate gc: refused %s\n", r)
	}
	return 0
}

// writeMutantsInUse accounts for the cargo-mutants tree copies the sweep
// deliberately left: a copy a run still owns is not garbage, and a report
// that silently omitted it would leave an operator wondering where the
// gigabytes went.
func writeMutantsInUse(stdout io.Writer) {
	for _, line := range tdd.MutantsCopiesInUse(tdd.MutantsTempDirs()) {
		fmt.Fprintln(stdout, line)
	}
	for _, line := range tdd.TempTargetsInUse(tdd.MutantsTempDirs()) {
		fmt.Fprintln(stdout, line)
	}
}

// gcScopeFromFlags builds the sweep's scope: everything, with the lock-litter
// bar the operator asked for.
func gcScopeFromFlags(lockAge string) (tdd.GCScope, error) {
	age, err := tdd.ParseGCAge(lockAge)
	if err != nil {
		return tdd.GCScope{}, err
	}
	scope := tdd.AllGCScopes()
	scope.LockAge = age
	return scope, nil
}

// writeProbeBackups lists the probe discard backups. They are never a
// candidate: a backup is what makes a discard undoable, so only the operator
// removes one.
func writeProbeBackups(stdout io.Writer) {
	for _, line := range probeBackupListing(time.Now()) {
		fmt.Fprintln(stdout, line)
	}
}
