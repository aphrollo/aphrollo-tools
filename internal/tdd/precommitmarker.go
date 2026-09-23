package tdd

import (
	"bufio"
	"os"
	"path/filepath"
	"time"
)

// Precommit runs the commit-time TDD wall (precommitDecide, unchanged) and
// leaves a bare "ran" marker in gate.log unconditionally — independent of
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
	appendGateLog("precommit", repoRoot, "gate", "ran", 0)
	return res
}

// PrecommitRanSince reports whether the pre-commit gate left its "ran"
// marker for root at or after `at`. `at` is exclusive of anything strictly
// before it, matching greenLoggedSince's own inclusive-at convention (the
// log stamps whole seconds, so a run that finished within the same second
// still counts).
func PrecommitRanSince(root string, at time.Time) bool {
	dir := stateDir()
	if dir == "" {
		return false
	}
	f, err := os.Open(filepath.Join(dir, "gate.log"))
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		e, ok := parseGateLine(sc.Text())
		if !ok || e.Stage != "precommit" || e.Verdict != "ran" || e.At.Before(at) {
			continue
		}
		if sameProject(e.Root, root) {
			return true
		}
	}
	return false
}
