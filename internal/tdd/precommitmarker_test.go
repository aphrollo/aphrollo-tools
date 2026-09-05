package tdd

import (
	"testing"
	"time"
)

// Precommit must leave evidence that it actually ran, independent of the
// verdict its own stages logged (green, cache-hit, docs-only-fastpath, a
// block...) — a consumer outside this package (workspace/commit's stateful
// receipt) needs to tell a REAL run of this gate apart from a `git commit`
// that hit no hook at all.
func TestPrecommit_LeavesARanMarkerEvenOnTheDocsOnlyFastPath(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "NOTES.md", "# notes\n")
	gitDo(t, root, "add", ".")

	before := time.Now().Add(-2 * time.Second) // gate.log truncates to whole seconds; margin avoids a same-second flake
	var seen []Runner
	if res := Precommit(root, recordRunner(&seen, root)); res.Blocked {
		t.Fatalf("docs-only commit must not block: %s", res.Message)
	}
	if !PrecommitRanSince(root, before) {
		t.Fatal("Precommit must leave a marker PrecommitRanSince can see, even on the docs-only fast path")
	}
}

// A blocked run still ran — the marker records that the gate executed, not
// that the commit landed.
func TestPrecommit_LeavesARanMarkerEvenWhenBlocked(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "x_test.go", "package x\n\nfunc TestNothing() {}\n")
	write(t, root, "x.go", "package x\n")
	gitDo(t, root, "add", ".")

	before := time.Now().Add(-2 * time.Second) // gate.log truncates to whole seconds; margin avoids a same-second flake
	failing := func(r Runner, dir string) SuiteResult {
		return SuiteResult{Passed: false, Output: "--- FAIL: TestNothing"}
	}
	Precommit(root, failing)
	if !PrecommitRanSince(root, before) {
		t.Fatal("a blocked run still executed the gate — the marker must still be there")
	}
}

// No Precommit call for this root at all: PrecommitRanSince must say so
// rather than default to "yes".
func TestPrecommitRanSince_FalseWhenNothingWasEverLoggedForThisRoot(t *testing.T) {
	root := t.TempDir()
	if PrecommitRanSince(root, time.Now().Add(-time.Hour)) {
		t.Fatal("an untouched root must never read as having run the gate")
	}
}

// `at` is exclusive of entries strictly before it: a marker from a PRIOR
// commit's run must not be read as evidence for a run that starts later.
func TestPrecommitRanSince_FalseForAMarkerLoggedBeforeAt(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "NOTES.md", "# notes\n")
	gitDo(t, root, "add", ".")
	Precommit(root, recordRunner(&[]Runner{}, root))

	after := time.Now().Add(time.Second)
	if PrecommitRanSince(root, after) {
		t.Fatal("a marker logged BEFORE `at` must not count as evidence of a run at/after `at`")
	}
}
