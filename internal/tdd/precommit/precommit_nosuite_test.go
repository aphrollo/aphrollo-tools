package precommit

import "testing"

// precommit_concurrent_test.go is deleted with the concurrency it covered, so
// its tombstones stand here, beside the change that made them dead: with no
// suite left at commit time there is no second stage for the fail-first proof
// to overlap with, and the pairing #545 built has nothing to pair.
//
// ratchet: test_removed TestPrecommit_ConcurrentPair_ActuallyOverlapsInWallClock: it measured the fail-first proof and the mechanical suite running at the same wall-clock moment; the commit gate now runs only the first of the two.
// ratchet: test_removed TestPrecommit_ConcurrentPair_BothStagesRunEvenWhenFailFirstRejects: it pinned that a fail-first rejection did not cancel the suite already in flight beside it; there is no such suite.
// ratchet: test_removed TestPrecommit_ConcurrentPair_CapsGoTestParallelism: it capped the two concurrent stages' combined -parallel so they did not oversubscribe the box; one stage cannot oversubscribe against itself.
// ratchet: test_removed TestPrecommit_ConcurrentPair_RejectionsAreDistinguishable: it proved a rejection named which of the two concurrent stages produced it; with one stage the name is never ambiguous.
// ratchet: test_removed TestGoTestJobsFor_matches_closed_form: goTestJobsFor divided the box's cores between those two concurrent stages, and is deleted with them.

// TestPrecommit_RunsNoSuiteAtCommitTime pins the removal of the mechanical
// suite from the commit gate.
//
// Measured over 90 days of gate.log on this tool's own consumer: the
// commit-time suite ran 2353 times and reported a plain assertion failure 0
// times, while rejecting 58 commits for exceeding its own budget under box
// load. Every one of the 2801 real failures in that record came from the
// post-edit hook, which runs the same scoped suite after each edit and
// reports RED there — so by commit time the suite has already run green on
// the same code, under a stricter timeout profile, and the commit gate was
// paying to re-run it. It could not even reuse that result: mechKey includes
// the full argv, and the two stages differ in both profile and package set,
// so their cache entries never collide.
//
// What the commit gate keeps is every stage that does reject: baseline,
// ratchet laws, docs citations, the suppression anti-cheat, fmt, vet, lint,
// and the fail-first RED proof — 206 blocks over the same window. The full
// suite lives at the merge gate, which is the last thing before main.
func TestPrecommit_RunsNoSuiteAtCommitTime(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Precommit(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if len(seen) != 0 {
		t.Fatalf("commit gate ran %d suite command(s), want none: %+v", len(seen), seen)
	}
}

// TestMechanical_StillRunsTheSuiteAtMerge is the other half of the same
// change: the suite MOVED, it did not disappear. A merge still runs the
// touched packages' tests, so nothing reaches main untested — the coverage
// the commit gate used to duplicate now lands here once.
func TestMechanical_StillRunsTheSuiteAtMerge(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if len(seen) == 0 {
		t.Fatal("merge gate ran no suite; the full suite must survive the commit-gate removal")
	}
}
