package tdd

import (
	"fmt"
	"path/filepath"
)

// A blob and a fence describe the SOURCE a mutant was measured against, and
// ProducerVersion describes the TOOL. Neither describes HOW the tool was
// invoked: borld adding a scratch Postgres plus `--run-ignored all` to its
// mutation runs changed neither the source nor the tool, so every outcome
// the store already held carried forward unchanged — including the two
// mutants that change was written for, which stayed MISSED forever because
// the flag that would have caught them was never exercised against their
// cached verdict (issue #531).
//
// The fix is NOT folding the invocation into the fence: a fence is a
// property of a PACKAGE, reused by every outcome ever measured against it,
// so a global value folded in there invalidates the whole workspace's cache
// on every runner-script edit. InvocationVersion is instead a MutantOutcome
// field, stamped fresh at measurement time exactly the way ProducerVersion
// already is, and compared by carriesOver the same way: plain equality, so
// two runs whose invocation agrees never re-measure, and one whose does not
// is read the same as a blob or fence mismatch.

// MutantsInvocation is the subset of a run's own flags that can flip a
// mutant's VERDICT. It is deliberately narrow: mutantsProducerFlags's `--in-diff` is a
// temp path that differs every run, and its `--package` list and resume
// exclusions vary with what is being measured — hashing those would
// invalidate every cached outcome on every run, which is strictly worse than
// not having this axis at all. Only what is listed here ever reaches
// Version().
type MutantsInvocation struct {
	// TestTool is the suite runner driving the mutated build: "nextest" for
	// mutantsProducerFlags's Cargo workspace runs, "go test" for gremlins' Go ones —
	// the two producers this package knows about today.
	TestTool string
	// RunIgnored is cargo-mutants' own `--run-ignored` value, "" for its
	// default of never running an `#[ignore]`d test. Nothing in this package
	// sets it yet; it is the flag issue #531's own motivating case (a
	// database tier that activates `--run-ignored all` for the packages that
	// need it) will set once that lands.
	RunIgnored string
	// NextestProfile is the `--profile` mutantsProducerFlags passes nextest, "" for
	// its default profile. mutantsProducerFlags never sets one today.
	NextestProfile string
	// DatabaseProvisioned is whether this run stood up a scratch database for
	// the suite. Nothing in this package provisions one yet.
	DatabaseProvisioned bool
}

// Version is the canonical string InvocationVersion stamps, and the value
// carriesOver compares by plain equality — never a hash of the raw argv, so
// a run scoped to a different diff or package list still reads as the SAME
// invocation, and a run whose test tool, ignored-test policy, nextest
// profile, or database provisioning actually differs never does.
func (inv MutantsInvocation) Version() string {
	return fmt.Sprintf("test-tool=%s run-ignored=%s nextest-profile=%s db=%t",
		inv.TestTool, inv.RunIgnored, inv.NextestProfile, inv.DatabaseProvisioned)
}

// InvocationVersionFor is a run's invocation, PER PACKAGE. Every call site
// today builds one from mutantsInvocationVersionFor, which returns the same
// string for every package, because nothing in this codebase yet varies
// TestTool, RunIgnored, NextestProfile or DatabaseProvisioned by package —
// but the shape is per-package from the start. #531's own motivating case
// (a database tier active only for packages that need it, e.g. `persistence`
// or `dev_server`) is why: a single run-wide invocation string is the easy
// way to wire that, and it is wrong — it would stamp DatabaseProvisioned=true
// onto every OTHER package measured in the same run too, invalidating their
// cache the next time they are measured ALONE with the tier off, which is
// exactly the "strictly worse than not having the field at all" outcome
// #531 names. Carrying the invocation as a function of package, all the way
// through carriesOver's callers, means the day that split lands, only
// mutantsInvocationVersionFor's BODY changes — carriesOver, PlanMutants,
// PlanDiffFiles and stampTreeState never do.
type InvocationVersionFor func(pkg string) string

// mutantsInvocationVersion is the invocation this box would run worktree's
// suite with today, threaded through PlanMutants, PlanDiffFiles and
// stampTreeState the same way mutantsProducerVersion already is — so a
// future change to any of MutantsInvocation's fields invalidates exactly the
// outcomes it can affect, at the point they are next planned or stamped, and
// nothing else.
//
// producerVersion is mutantsProducerVersion's own answer for this SAME
// worktree, threaded in rather than re-queried (a second live version probe
// would double the subprocess cost of every run for no new information).
// Empty producerVersion means this box could not even confirm the producer
// runs — no producer detected, or the version query itself errored — and
// this degrades to "" for exactly the same reason: a producer whose
// identity cannot be confirmed leaves nothing this box can honestly claim
// about how it would be invoked either. Matching ProducerVersion's own
// degrade this way, rather than inventing a second convention (deciding
// determinability purely from which manifest file is present, independent
// of whether the tool actually answers), is what keeps every one of this
// package's own pre-existing test fixtures — none of which ever set
// InvocationVersion — carrying exactly as they did before this field
// existed, in test environments where the real producer binary cannot be
// queried.
func mutantsInvocationVersion(worktree, producerVersion string) string {
	if producerVersion == "" {
		return ""
	}
	var inv MutantsInvocation
	switch {
	case fileExists(filepath.Join(worktree, "tools", "mutation_gate.sh")):
		inv.TestTool = "nextest"
	case fileExists(filepath.Join(worktree, "go.mod")):
		inv.TestTool = "go test"
	}
	return inv.Version()
}

// mutantsInvocationVersionFor wraps mutantsInvocationVersion's single
// worktree-wide answer as an InvocationVersionFor: every package gets the
// same value, since the answer does not depend on pkg today. Every
// production call site uses this rather than calling mutantsInvocationVersion
// and wrapping it by hand, so there is one place that decision is made.
func mutantsInvocationVersionFor(worktree, producerVersion string) InvocationVersionFor {
	version := mutantsInvocationVersion(worktree, producerVersion)
	return func(string) string { return version }
}

// stampInvocationVersion returns a COPY of outcomes with every entry's
// InvocationVersion set from versionFor, resolved against ITS OWN package —
// m.Package when already stamped, now.Packages[m.File] otherwise, the same
// resolution stampTreeState applies — so a caller whose outcomes have not
// had Package stamped yet (the gremlins job path, whose raw report carries
// no package until goMutantsReceipt fills it in) still gets the RIGHT
// package's invocation rather than every entry silently resolving against
// pkg="". The input slice is left untouched, the same shape as
// stampProducerVersion, which the gremlins job path always runs alongside.
func stampInvocationVersion(outcomes []MutantOutcome, now TreeState, versionFor InvocationVersionFor) []MutantOutcome {
	out := make([]MutantOutcome, len(outcomes))
	for i, m := range outcomes {
		pkg := m.Package
		if pkg == "" {
			pkg = now.Packages[m.File]
		}
		m.InvocationVersion = versionFor(pkg)
		out[i] = m
	}
	return out
}
