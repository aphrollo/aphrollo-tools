package mutation

import "strconv"

// cargoMutantsArgv is the whole command line: cargo-mutants is a cargo
// subcommand, so the program is cargo and the tool is its first argument.
func cargoMutantsArgv(flags []string) []string {
	return append([]string{"cargo", "mutants"}, flags...)
}

// MutantsArgv is cargo-mutants' own flags for a lane measurement, built in
// one place so what the tool is asked to do is one literal a reviewer reads:
// mutate a copy of the SOURCE tree, only inside the lane's diff, in the order
// the diff names them, through nextest, with a timeout budget derived from
// the last measured baseline.
//
// There is no `--in-place`: it mutates the one checkout and forbids `--jobs`
// with it, which made the run one job by construction — 739 mutants in 16 h
// on a box that could measure eight at once. An argv carrying both exits
// before the first mutant with "error: the argument '--in-place' cannot be
// used with '--jobs <JOBS>'" (issue #592). There is no `--jobs` here either:
// concurrency is one PROCESS per shard, and mutantsShardArgv gives each of
// them `--jobs 1`.
//
// packages narrows both the mutant pool and the unmutated BASELINE to the
// crates the diff touches; empty falls back to the whole workspace rather
// than measuring nothing. excludeFilter is the repo's own
// mutation-baseline-exclude, already combined into one nextest filterset —
// passed after `--`, which is where cargo-mutants forwards it to the SAME
// test command it runs for both the baseline and every mutant.
func MutantsArgv(diffPath string, minTestTimeout int, packages []string, excludeFilter string) []string {
	if minTestTimeout < mutantsMinTestTimeoutFloor {
		minTestTimeout = mutantsMinTestTimeoutFloor
	}
	argv := []string{
		// --no-shuffle: two runs of the same tree must name their mutants in
		// the same order, or a report is not comparable with the one before
		// it.
		// --copy-target=false: the copy carries the SOURCE TREE ONLY. With
		// it true every copy also carried the workspace target dir — on the
		// repo that produced this design, 294 GB of build products against
		// 37 MB of sources, copied byte for byte because NTFS has no
		// reflink: 375 GB in 1 h 47 min, then a full drive and no verdict.
		// The warm build products live outside the copies instead, one
		// PERSISTENT target dir per shard (mutants_shards.go), so a copy is
		// megabytes and still builds incrementally.
		"--copy-target=false", "--in-diff", diffPath, "--no-shuffle", "--test-tool=nextest",
		"--minimum-test-timeout", strconv.Itoa(minTestTimeout),
		"--timeout-multiplier", strconv.Itoa(mutantsTimeoutMultiplier),
	}
	for _, pkg := range packages {
		argv = append(argv, "--package", pkg)
	}
	if excludeFilter != "" {
		argv = append(argv, "--", "-E", excludeFilter)
	}
	return argv
}
