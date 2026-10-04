package precommit

import (
	"time"
)

// Precommit runs the commit-time TDD wall (precommitDecide, unchanged) and
// leaves a bare "ran" marker in the event log unconditionally — independent of
// whatever verdict the wall's own stages already logged (green, cache-hit,
// docs-only-fastpath, a block reason...). A consumer outside this package
// (workspace/commit's stateful receipt — receipt-word-ok: that verb family's
// own live artifact, not the deleted mutation document) reads the marker via
// PrecommitRanSince to tell a REAL run of this gate apart from a `git commit`
// that hit no hook at all: with no core.hooksPath configured (a fresh clone,
// a broken profile, or a test harness's own GIT_CONFIG_SYSTEM=/dev/null
// isolation) `git commit` still exits 0 having verified nothing, and a line
// there claiming "gate TDD pass" is a claim of verification that never
// happened.
func Precommit(repoRoot string, run SuiteRunner) GateResult {
	resetSuiteProof()
	res := precommitDecide(repoRoot, run)
	AppendGateLog("precommit", repoRoot, "gate", "ran", 0)
	return res
}

// PrecommitRanSince reports whether the pre-commit gate left its "ran"
// marker for root at or after `at`. `at` is exclusive of anything strictly
// before it, matching greenLoggedSince's own inclusive-at convention (the
// log stamps whole seconds, so a run that finished within the same second
// still counts).
func PrecommitRanSince(root string, at time.Time) bool {
	for _, e := range readGateEntries(root, at) {
		if e.Stage == "precommit" && e.Verdict == "ran" && !e.At.Before(at) && sameProject(e.Root, root) {
			return true
		}
	}
	return false
}
