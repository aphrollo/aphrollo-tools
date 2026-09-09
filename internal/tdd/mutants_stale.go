package tdd

import (
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// A shard's build dir persists so the next run copies megabytes and still
// builds incrementally. What persists with it is whatever the LAST mutant
// built, and that is the trap: cargo decides freshness by mtime, the source
// tree cargo-mutants copies for the next run carries the ORIGINAL mtimes, and
// those are older than the artifacts already sitting there. cargo then rebuilds
// nothing and the baseline links the previous run's MUTANT, reporting its
// behaviour as the tree's own.
//
// Measured on a real run: the shard's test binaries were dated an hour NEWER
// than the source they were told to test, the build step reported `Finished
// 'test' profile [optimized + debuginfo] target(s) in 0.74s`, and the baseline
// then failed three different behavioural assertions at once — one of them a
// value the current source cannot produce. A loud failure was the lucky shape.
// The same staleness can leave a mutant's binary in place for a mutant that
// was meant to be caught, and report a verdict that is simply wrong.
//
// The rule: a MUTATED package's artifacts are never safe to carry into another
// run, whatever the source mtimes say, because what survives belongs to a
// mutant rather than to the tree. A DEPENDENCY's artifacts are always safe —
// nothing mutates them — and they are the whole value of keeping the directory.
// So the purge is surgical when the run names the packages it mutates, and
// total when it cannot: a run that names none mutates the whole workspace, and
// then nothing in the directory is provably unmutated.

// cargoCleanFn removes the named packages' artifacts from one build dir. A
// seam because the rules above must be testable without a cargo toolchain, and
// because the real call spawns a process.
var cargoCleanFn = cargoCleanPackages

// setCargoCleanForTest swaps the clean seam for the duration of a test.
func setCargoCleanForTest(fn func(root, target string, pkgs []string) error) (restore func()) {
	prev := cargoCleanFn
	cargoCleanFn = fn
	return func() { cargoCleanFn = prev }
}

// purgeMutatedArtifacts makes a persistent shard build dir safe to reuse,
// keeping the dependency artifacts that make it worth keeping.
//
// Every failure falls the same way, toward removing the directory whole: a
// clean that did not happen leaves a mutant's artifacts exactly where the next
// baseline will link them, and a cold rebuild costs minutes where a wrong
// verdict costs a merge.
func purgeMutatedArtifacts(root, target string, pkgs []string, log io.Writer) {
	if _, err := os.Stat(target); err != nil {
		// Nothing was kept, so nothing can be stale. A first run lands here.
		return
	}
	if len(pkgs) > 0 {
		if err := cargoCleanFn(root, target, pkgs); err == nil {
			return
		} else {
			logf(log, "mutants: could not clean %s from %s (%v) — removing the build dir whole so no "+
				"mutant's artifacts survive into this run", strings.Join(pkgs, ", "), target, err)
		}
	} else {
		logf(log, "mutants: the run names no package, so nothing in %s is provably unmutated — "+
			"removing it whole", target)
	}
	if err := os.RemoveAll(target); err != nil {
		logf(log, "mutants: could not remove %s (%v) — this run may link a previous mutant's artifacts",
			target, err)
	}
}

// packagesInArgv reads the packages a run mutates off its own argv, which is
// the one place they are already stated. Reading them here rather than
// threading a second copy through keeps the purge and the run agreeing by
// construction: a shard that mutates a package the purge did not clean is the
// whole bug, and two lists that can drift is how it would come back.
func packagesInArgv(argv []string) []string {
	var out []string
	for i, a := range argv {
		if a == "--" {
			break // everything after belongs to the test tool
		}
		if a == "--package" && i+1 < len(argv) {
			out = append(out, argv[i+1])
		}
	}
	return out
}

// cargoCleanTimeout bounds the clean. It reads a directory and unlinks files;
// one that has not finished in this long is not going to.
const cargoCleanTimeout = 2 * time.Minute

// cargoCleanPackages is the real clean: cargo's own, which knows which files
// in a build dir belong to a package and which belong to its dependencies.
// Deleting by filename pattern instead would be this side guessing at cargo's
// layout, and a guess that misses one file is the bug this exists to close.
func cargoCleanPackages(root, target string, pkgs []string) error {
	argv := []string{"cargo", "clean", "--target-dir", target}
	for _, p := range pkgs {
		argv = append(argv, "-p", p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cargoCleanTimeout)
	defer cancel()
	// The gate marker, for the same reason the run itself carries it: this
	// cargo is part of a measurement that already holds the box-wide mutation
	// lock, so it must not queue behind every editor on the machine.
	env := append(os.Environ(), MutationGateEnv+"="+MutationGateMarked, "CI=1", "NO_COLOR=1")
	code, err := runMutantsTool(ctx, root, env, argv, io.Discard)
	if err != nil {
		return err
	}
	if code != 0 {
		return &cleanExitError{code: code}
	}
	return nil
}

// cleanExitError is a non-zero cargo clean, named so the caller's message says
// what happened rather than printing a bare number.
type cleanExitError struct{ code int }

func (e *cleanExitError) Error() string {
	return "cargo clean exited " + strconv.Itoa(e.code)
}
