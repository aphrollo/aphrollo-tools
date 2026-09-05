package tdd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// A blob and a fence are the whole invalidation story for a mutant that was
// already measured — but they describe the SOURCE, never the TOOL. Upgrading
// gremlins or cargo-mutants can add a mutator with no change to either, and
// PlanDiffFiles/measuredUnchanged (mutants_treestate.go) would then skip a
// file forever on the strength of an outcome the new mutator was never even
// offered a chance to join (issue #298). Folding the producer's own version
// into the comparison closes that: an upgrade changes the version, a version
// mismatch is read the same as a blob or fence mismatch, and the file is
// walked again exactly once before settling back into the cache.

// mutantsProducerVersionCmd names the command that answers for the mutator
// set a file in worktree would be analysed with: cargo-mutants for a Cargo
// workspace, gremlins for a Go one. Neither goes through `cargo` itself —
// cargo-mutants ships its own `cargo-mutants` executable (what `cargo
// mutants` dispatches to), and calling it directly here skips the aphrollo
// cargo shim's block on a bare `cargo mutants`: that block protects an
// actual mutation RUN from the queue and the box-wide lock, neither of which
// a version query needs or should wait behind.
func mutantsProducerVersionCmd(worktree string) (name string, args []string, ok bool) {
	if fileExists(filepath.Join(worktree, "tools", "mutation_gate.sh")) {
		return "cargo-mutants", []string{"mutants", "--version"}, true
	}
	if fileExists(filepath.Join(worktree, "go.mod")) {
		return gremlinsBin, []string{"--version"}, true
	}
	return "", nil, false
}

// mutantsProducerVersionRunFn is the seam a test overrides instead of
// spawning a real toolchain.
var mutantsProducerVersionRunFn = func(name string, args []string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// mutantsProducerVersionWarnOnce keeps a failed version probe to one line per
// process: every plan asks, and a screenful of identical complaints would
// bury the one fact that matters.
var mutantsProducerVersionWarnOnce sync.Once

// mutantsProducerVersion is the producer's own version string, "" when it
// cannot be read — no producer detected in worktree, the binary is missing,
// or the command errored. "" is the same, already-tested behaviour this
// store had before this field existed (every cached entry's ProducerVersion
// is also "" until this ships), rather than a hard failure over what is only
// a diagnostic query.
func mutantsProducerVersion(worktree string) string {
	name, args, ok := mutantsProducerVersionCmd(worktree)
	if !ok {
		return ""
	}
	out, err := mutantsProducerVersionRunFn(name, args)
	if err != nil {
		mutantsProducerVersionWarnOnce.Do(func() {
			fmt.Fprintf(os.Stderr,
				"gate: could not read the mutation producer's version (%v) — every cached outcome is treated as measured at an unknown tool version\n", err)
		})
		return ""
	}
	return strings.TrimSpace(out)
}

// stampProducerVersion returns a COPY of outcomes with every entry's
// ProducerVersion set to version, leaving the input slice untouched — the
// same shape as stampTreeState, which this always runs alongside.
func stampProducerVersion(outcomes []MutantOutcome, version string) []MutantOutcome {
	out := make([]MutantOutcome, len(outcomes))
	for i, m := range outcomes {
		m.ProducerVersion = version
		out[i] = m
	}
	return out
}
