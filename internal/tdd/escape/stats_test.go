package escape

import (
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// TestGateStats_TalliesTheLogByStageAndOutcome pins the point of the command:
// pipeline health should be a number, not a feeling. Before this the only way
// to know how often the gate timed out, queued or deferred was to read
// thousands of gate.log lines by eye.
func TestGateStats_TalliesTheLogByStageAndOutcome(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	log := strings.Join([]string{
		stamp(now.Add(-30*time.Minute), "postedit", `D:\repo\crates\server`, "cargo nextest run -p server", "green", 12.5),
		stamp(now.Add(-25*time.Minute), "postedit", `D:\repo\crates\server`, "cargo nextest run -p server", "timeout", 110),
		stamp(now.Add(-20*time.Minute), "postedit", `D:\repo\crates\pose`, "cargo nextest run -p pose", "deferred", 0),
		stamp(now.Add(-10*time.Minute), "precommit", `D:\repo`, "cargo nextest run -p server", "green", 60),
		stamp(now.Add(-5*time.Minute), "precommit", `D:\repo`, "cargo nextest run -p server", "timeout-rejected", 600),
		stamp(now.Add(-72*time.Hour), "precommit", `D:\repo`, "cargo nextest run -p server", "green", 1),
	}, "")

	got := GateStats(strings.NewReader(log), now.Add(-24*time.Hour))

	if n := got.Count("postedit", "green"); n != 1 {
		t.Errorf("postedit green = %d, want 1", n)
	}
	if n := got.Count("precommit", "green"); n != 1 {
		t.Errorf("precommit green = %d, want 1 — the 72h-old line is outside --since", n)
	}
	if n := got.Count("precommit", "timeout-rejected"); n != 1 {
		t.Errorf("precommit timeout-rejected = %d, want 1", n)
	}
	if n := got.Timeouts["server"]; n != 1 {
		t.Errorf("server timeouts = %d, want 1 (per-crate, from the root's last path element)", n)
	}
	if n := got.Deferred["pose"]; n != 1 {
		t.Errorf("pose deferred = %d, want 1", n)
	}
	if got.Max != 600 {
		t.Errorf("max seconds = %v, want 600", got.Max)
	}
	if got.Median != 60 {
		t.Errorf("median seconds = %v, want 60 (of 12.5, 110, 0, 60, 600)", got.Median)
	}
}

// TestGateStats_DerivesTheCrateFromEitherSeparator pins that the per-crate
// tallies survive the log crossing an OS boundary. gate.log is append-only
// text an operator copies around and CI reads: a run recorded on Windows
// names its root with backslashes, and filepath.Base on Linux does not split
// on those, so every Windows-written entry was tallied under the whole path
// as if it were one enormous crate name.
func TestGateStats_DerivesTheCrateFromEitherSeparator(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	log := stamp(at, "postedit", `D:\repo\crates\server`, "cargo nextest run -p server", "timeout", 110) +
		stamp(at, "postedit", "/home/runner/repo/crates/pose", "cargo nextest run -p pose", "timeout", 110) +
		stamp(at, "postedit", `D:\repo\crates\item\`, "cargo nextest run -p item", "deferred", 0)

	got := GateStats(strings.NewReader(log), time.Time{})

	if n := got.Timeouts["server"]; n != 1 {
		t.Errorf("server timeouts = %d, want 1 — a backslash root must yield its last element", n)
	}
	if n := got.Timeouts["pose"]; n != 1 {
		t.Errorf("pose timeouts = %d, want 1 — a slash root must yield its last element", n)
	}
	if n := got.Deferred["item"]; n != 1 {
		t.Errorf("item deferred = %d, want 1 — a trailing separator is not part of the name", n)
	}
}

// TestGateStats_WithoutASinceCoversTheWholeLog pins the default: an operator
// asking "how is the pipeline doing" with no window means all of it.
func TestGateStats_WithoutASinceCoversTheWholeLog(t *testing.T) {
	t.Parallel()
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	log := stamp(old, "precommit", `D:\repo`, "go test ./...", "green", 3)

	got := GateStats(strings.NewReader(log), time.Time{})
	if n := got.Count("precommit", "green"); n != 1 {
		t.Fatalf("green = %d, want the whole log counted when no window is given", n)
	}
}

// TestRenderGateStats_NamesEveryColumnItCounted keeps the table readable: a
// stage with no rows still appears, so "zero timeouts" and "never ran" are
// not the same blank.
func TestRenderGateStats_NamesEveryColumnItCounted(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	log := stamp(now, "postedit", `D:\repo\crates\pose`, "cargo nextest run -p pose", "queued-skipped", 0)
	out := RenderGateStats(GateStats(strings.NewReader(log), time.Time{}))
	for _, want := range []string{"postedit", "precommit", "queued-skipped", "green"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table is missing %q:\n%s", want, out)
		}
	}
}

// TestGateStats_CountsVacuousRejectedBesideTimeoutRejected pins #317's ask:
// a zero-tests-executed block is counted in `gate stats`, with its own
// column next to timeout-rejected — the two share the "nothing was tested"
// shape but have different causes, and collapsing them would hide which one
// a pipeline is actually seeing.
func TestGateStats_CountsVacuousRejectedBesideTimeoutRejected(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	log := stamp(now, "precommit", `D:\repo`, "go test .", "vacuous-rejected", 0.4)

	got := GateStats(strings.NewReader(log), time.Time{})
	if n := got.Count("precommit", "vacuous-rejected"); n != 1 {
		t.Errorf("precommit vacuous-rejected = %d, want 1", n)
	}
	if !strings.Contains(RenderGateStats(got), "vacuous-rejected") {
		t.Fatal("RenderGateStats table is missing the vacuous-rejected column")
	}
}

func stamp(at time.Time, stage, root, cmd, verdict string, secs float64) string {
	return tddtest.Stamp(at, stage, root, cmd, verdict, secs, formatFloat)
}

// A receipt that was hand-written or unsigned is a policy event: it reaches
// gate.log, and until now nothing in `gate stats` counted it, so the one
// number that would have shown a session hand-writing a receipt was invisible.
func TestGateStats_CountsTheReceiptVerdicts(t *testing.T) {
	t.Parallel()
	log := strings.Join([]string{
		stamp(time.Now().UTC(), "premergecommit", "/repo", "receipt", "receipt-forged", 0),
		stamp(time.Now().UTC(), "premergecommit", "/repo", "receipt", "receipt-unsigned", 0),
		stamp(time.Now().UTC(), "premergecommit", "/repo", "receipt", "receipt-unsigned", 0),
		stamp(time.Now().UTC(), "precommit", "/repo", "cargo", "green", 1.5),
	}, "\n") + "\n"

	s := GateStats(strings.NewReader(log), time.Time{})
	if s.Denies["receipt-forged"] != 1 || s.Denies["receipt-unsigned"] != 2 {
		t.Fatalf("denies = %v, want one forged and two unsigned", s.Denies)
	}
	out := RenderGateStats(s)
	for _, want := range []string{"receipt-forged", "receipt-unsigned"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered stats never mention %s:\n%s", want, out)
		}
	}
}

// Eleven blockReceipt/blockMissingReceipt call sites used to share one
// undifferentiated receipt-rejected counter, so a 68% rejection rate could
// not say whether the gate was catching real survivors or wasting time on a
// stale base (issue #376). Each cause's own receipt-rejected:<reason> token
// must land as its OWN row in the denies table, beside receipt-unsigned,
// rather than collapsing back into one aggregate the way the bare
// colon-less "receipt-rejected" (asserted only via s.Receipts elsewhere)
// already did before this fix.
func TestGateStats_CountsReceiptRejectionsByReasonInTheDeniesTable(t *testing.T) {
	t.Parallel()
	log := strings.Join([]string{
		stamp(time.Now().UTC(), "premergecommit", "/repo", "mutation-receipt", "receipt-rejected:base-mismatch", 0),
		stamp(time.Now().UTC(), "premergecommit", "/repo", "mutation-receipt", "receipt-rejected:base-mismatch", 0),
		stamp(time.Now().UTC(), "premergecommit", "/repo", "mutation-receipt", "receipt-rejected:unaccepted-survivor", 0),
	}, "\n") + "\n"

	s := GateStats(strings.NewReader(log), time.Time{})
	if s.Denies["receipt-rejected:base-mismatch"] != 2 || s.Denies["receipt-rejected:unaccepted-survivor"] != 1 {
		t.Fatalf("denies = %v, want two base-mismatch and one unaccepted-survivor, each its own row", s.Denies)
	}
	out := RenderGateStats(s)
	for _, want := range []string{"receipt-rejected:base-mismatch=2", "receipt-rejected:unaccepted-survivor=1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered stats never break the rejection down by reason (want %q):\n%s", want, out)
		}
	}
}

// A stand-down that only prints leaves no trace once the terminal scrolls
// past it: "skipped", "runner-missing", "lint-skipped" and the receipt
// stage's "unverifiable"/"unpinned" outcomes all reached gate.log and none
// of them had a row anywhere (issue #320). "vet-fail-open" pins that a
// verdict carrying a prefix is still caught, not just an exact "fail-open"
// token — gate.log is space-delimited, so a verdict itself can never carry a
// space (unlike the "inconclusive (fail-open)" token precommit_failfirst.go
// actually writes: that one is a pre-existing, separate parsing defect,
// tracked on its own rather than fixed here).
func TestGateStats_CountsEveryStandDownVerdict(t *testing.T) {
	t.Parallel()
	log := strings.Join([]string{
		stamp(time.Now().UTC(), "precommit", "/repo", "go test .", "skipped", 0),
		stamp(time.Now().UTC(), "precommit", "/repo", "go vet ./...", "runner-missing", 0),
		stamp(time.Now().UTC(), "precommit", "/repo", "golangci-lint run ./...", "lint-skipped", 0),
		stamp(time.Now().UTC(), "premergecommit", "/repo", "mutation-receipt", "receipt-unverifiable", 0),
		stamp(time.Now().UTC(), "premergecommit", "/repo", "mutation-receipt", "receipt-unpinned", 0),
		stamp(time.Now().UTC(), "precommit", "/repo", "go test .", "vet-fail-open", 0),
	}, "")

	s := GateStats(strings.NewReader(log), time.Time{})
	want := map[string]int{
		"skipped": 1, "runner-missing": 1, "lint-skipped": 1,
		"receipt-unverifiable": 1, "receipt-unpinned": 1, "vet-fail-open": 1,
	}
	for verdict, n := range want {
		if s.StandDowns[verdict] != n {
			t.Errorf("StandDowns[%q] = %d, want %d (StandDowns = %v)", verdict, s.StandDowns[verdict], n, s.StandDowns)
		}
	}
	out := RenderGateStats(s)
	if !strings.Contains(out, "stand-downs:") {
		t.Fatalf("rendered stats carry no stand-downs row:\n%s", out)
	}
	for _, want := range []string{"skipped=1", "runner-missing=1", "lint-skipped=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered stats never mention %s:\n%s", want, out)
		}
	}
}

// "queued-skipped" is a stand-down too (it ends in "-skipped"), so it counts
// in BOTH places: the contention line answers "is the box busy", the
// stand-downs row answers "is every stand-down counted somewhere" — the two
// questions are not mutually exclusive, and folding one into the other would
// lose whichever answer nobody asked for.
func TestGateStats_QueuedSkippedCountsInStandDownsAlongsideContention(t *testing.T) {
	t.Parallel()
	log := stamp(time.Now().UTC(), "postedit", `D:\repo\crates\pose`, "cargo nextest run -p pose", "queued-skipped", 0)
	s := GateStats(strings.NewReader(log), time.Time{})
	if s.StandDowns["queued-skipped"] != 1 {
		t.Fatalf("StandDowns[queued-skipped] = %d, want 1", s.StandDowns["queued-skipped"])
	}
	out := RenderGateStats(s)
	if !strings.Contains(out, "contention:") {
		t.Fatal("contention line missing")
	}
	if !strings.Contains(out, "stand-downs: queued-skipped=1") {
		t.Fatalf("stand-downs row missing queued-skipped:\n%s", out)
	}
}

// The queue bypass is a tolerated hole: anything can set it. What makes it
// tolerable is that every use is counted, so a bypass nobody expected shows up
// in the same table as every other waiver.
func TestGateStats_CountsAQueueBypass(t *testing.T) {
	t.Parallel()
	log := stamp(time.Now().UTC(), "precommit", "/repo", "cargo", "queue-bypass", 0) + "\n"
	s := GateStats(strings.NewReader(log), time.Time{})
	if s.Denies["queue-bypass"] != 1 {
		t.Fatalf("denies = %v, want the bypass counted", s.Denies)
	}
	if !strings.Contains(RenderGateStats(s), "queue-bypass") {
		t.Fatal("a bypass nobody can see is a bypass nobody manages")
	}
}

// A binary-behind standdown (#374) is the same shape as every other silent
// give-up here: it reaches gate.log outside the fixed stage/outcome
// vocabulary, and until it is counted as a deny, a permanently broken
// `git ls-remote` is invisible to `gate stats` the same way a hand-written
// receipt used to be.
func TestGateStats_CountsABinaryBehindStanddown(t *testing.T) {
	t.Parallel()
	log := strings.Join([]string{
		stamp(time.Now().UTC(), "binary-behind", "-", "git ls-remote", "standdown-timeout", 0),
		stamp(time.Now().UTC(), "binary-behind", "-", "git ls-remote", "standdown-failed", 0),
	}, "") + "\n"

	s := GateStats(strings.NewReader(log), time.Time{})
	if s.Denies["standdown-timeout"] != 1 || s.Denies["standdown-failed"] != 1 {
		t.Fatalf("denies = %v, want one of each standdown reason", s.Denies)
	}
	out := RenderGateStats(s)
	for _, want := range []string{"standdown-timeout", "standdown-failed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered stats never mention %s:\n%s", want, out)
		}
	}
}

// A worktree the post-commit hook could not prepare is a run that never
// happened, and until now it fell through every tally: not in the fixed
// stage/outcome vocabulary, not in Denies. `gate stats` must show it, error
// text and all, so a session watching the box sees a lane's mutation proof
// silently not-starting.
func TestGateStats_CountsAMutantsWorktreeFailure(t *testing.T) {
	t.Parallel()
	log := stamp(time.Now().UTC(), "postcommit", "/repo", "mutants",
		"mutants-worktree-failed:fatal:_could_not_create_leading_directories", 0) + "\n"
	s := GateStats(strings.NewReader(log), time.Time{})
	if s.Denies["mutants-worktree-failed:fatal:_could_not_create_leading_directories"] != 1 {
		t.Fatalf("denies = %v, want the worktree failure counted", s.Denies)
	}
	if !strings.Contains(RenderGateStats(s), "mutants-worktree-failed") {
		t.Fatal("a worktree failure nobody can see is a mutation proof nobody knows stopped running")
	}
}

// ratchet: test_removed TestGateStats_CountsMutationReceiptOutcomesSideBySide: the receipt stage is deleted; TestStats_MutantsStageRowCountsRefusalsByReason counts the mutation stage's own outcomes in its place
// The discard wall's refusals and its two overrides all reach gate.log —
// until this, none of them was counted, so a session that hit `reset --hard`
// twice and bypassed it twice left no number anywhere.
func TestStats_CountsDiscardRefusalsAndOverrides(t *testing.T) {
	t.Parallel()
	log := strings.Join([]string{
		stamp(time.Now().UTC(), "git", "/repo", "git", "git-discard-refused:reset---hard", 0),
		stamp(time.Now().UTC(), "git", "/repo", "git", "git-discard-refused:reset---hard", 0),
		stamp(time.Now().UTC(), "session", "/repo", "s1", "override-discard-used", 0),
		stamp(time.Now().UTC(), "session", "/repo", "s1", "override-discard-env", 0),
	}, "\n") + "\n"

	s := GateStats(strings.NewReader(log), time.Time{})
	if s.Denies["git-discard-refused:reset---hard"] != 2 {
		t.Fatalf("denies = %v, want the refusal counted twice", s.Denies)
	}
	if s.Denies["override-discard-used"] != 1 || s.Denies["override-discard-env"] != 1 {
		t.Fatalf("denies = %v, want both overrides counted once each", s.Denies)
	}
	out := RenderGateStats(s)
	for _, want := range []string{"git-discard-refused:reset---hard=2", "override-discard-used=1", "override-discard-env=1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered stats never mention %s:\n%s", want, out)
		}
	}
}

// TestRenderGateStats_UnmeasuredStageReadsAsDashNotZero pins issue #369: a
// stage whose only entries fall outside this table's fixed outcome
// vocabulary (mutants-started:... is not green/red/blocked/timeout/...) must
// never render as a row of confirmed zeros -- that reads as "ran clean" when
// the truth is "this table's vocabulary never applies to this stage".
func TestRenderGateStats_UnmeasuredStageReadsAsDashNotZero(t *testing.T) {
	t.Parallel()
	log := stamp(time.Now().UTC(), "mutants", "/repo", "mutants", "mutants-started:abc123", 0) + "\n"
	out := RenderGateStats(GateStats(strings.NewReader(log), time.Time{}))

	line := statsRowFor(t, out, "mutants")
	if strings.ContainsAny(line, "0123456789") {
		t.Fatalf("mutants row should carry no digit (unmeasured by this table's vocabulary), got: %q", line)
	}
	if !strings.Contains(line, "-") {
		t.Fatalf("mutants row should render dashes for an unmeasured stage, got: %q", line)
	}
}

// TestRenderGateStats_MeasuredStageStillShowsARealZero is the other half of
// #369's fix: once a stage posts even one TRACKED outcome, every cell in its
// row is a real count -- including a column that legitimately never fired,
// which must still read as 0, never as a dash meant for "never measured".
func TestRenderGateStats_MeasuredStageStillShowsARealZero(t *testing.T) {
	t.Parallel()
	log := stamp(time.Now().UTC(), "postedit", "/repo", "cargo", "green", 1.0) + "\n"
	out := RenderGateStats(GateStats(strings.NewReader(log), time.Time{}))

	line := statsRowFor(t, out, "postedit")
	fields := strings.Fields(line)
	blockedCol := 1 + indexOfString(statsOutcomes, "blocked")
	if blockedCol >= len(fields) || fields[blockedCol] != "0" {
		t.Fatalf("postedit's blocked column wants a real 0 (measured, never fired), row: %q", line)
	}
}

// statsRowFor returns the one line of a rendered table whose stage column
// names stage, so a test can inspect a specific row's cells directly.
func statsRowFor(t *testing.T, table, stage string) string {
	t.Helper()
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), stage) {
			return line
		}
	}
	t.Fatalf("no row for stage %q in:\n%s", stage, table)
	return ""
}

func indexOfString(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}
