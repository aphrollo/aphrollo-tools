package tdd

import (
	"regexp"
	"strings"
)

// mutantsProducerFlags is cargo-mutants' own flags as handed to the CONSUMING
// repo's runner script through APHROLLO_MUTANTS_ARGS. It is the OLD path,
// left standing beside MutantsArgv -- the in-binary runner's own argv, in
// mutants_measure.go -- only until the script and everything that addresses
// it go.
//
// It is cargo-mutants' own flags for a lane run: mutate the warm
// worktree IN PLACE (never a tree copy), only inside the lane's diff, and run
// the suite through nextest. baselineSkip drops the unmutated baseline run,
// which is sound only when the gate already proved that same tree green.
// packages narrows both the mutant pool and — the point of issue #251 — the
// unmutated BASELINE to the touched crates' own suites, via mutantsTouchedPackages;
// empty falls back to today's whole-workspace scope rather than measuring
// nothing. judged names the mutants an interrupted earlier attempt already
// reached a verdict for; they are excluded so a restart measures only what is
// left. excludeFilter is the repo's own mutation-baseline-exclude, already
// combined into one nextest filterset by mutationBaselineExclude — "" for a
// repo that declares none, which is today's argv unchanged.
func mutantsProducerFlags(diffPath string, baselineSkip bool, judged []string, packages []string, excludeFilter string) []string {
	argv := []string{"--in-place", "--in-diff", diffPath, "--test-tool=nextest"}
	if baselineSkip {
		argv = append(argv, "--baseline", "skip")
	}
	for _, pkg := range packages {
		argv = append(argv, "--package", pkg)
	}
	// Every exclusion rides to the runner inside one environment variable, and
	// Windows caps the whole environment block at 32,767 characters: an
	// unbounded list of a few thousand judged mutants truncates the run's own
	// arguments. So the list is bounded, and the overflow is simply
	// re-measured — dropping an exclusion costs one mutant's runtime, dropping
	// the arguments costs the run.
	size := len(strings.Join(argv, " "))
	for _, name := range judged {
		re := "^" + regexp.QuoteMeta(name) + "$"
		cost := len("--exclude-re ") + len(re) + 1
		if size+cost > mutantsArgvBudget {
			break
		}
		argv = append(argv, "--exclude-re", re)
		size += cost
	}
	// Everything after `--` is cargo-mutants' own passthrough to the test
	// tool, and cargo-mutants invokes that SAME test command for both the
	// unmutated baseline and every mutant it measures — one flag here is
	// what makes one declared exclusion cover both, with no separate lever
	// for either phase (issue #265).
	if excludeFilter != "" {
		argv = append(argv, "--", "-E", excludeFilter)
	}
	return argv
}

// mutantsArgvBudget is how long the runner's argument string may get. Well
// under the 32,767-character Windows environment block, because the block
// holds the rest of the environment too.
const mutantsArgvBudget = 8000
