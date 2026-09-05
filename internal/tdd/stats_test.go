package tdd

import (
	"strings"
	"testing"
	"time"
)

// TestGateStats_TalliesTheLogByStageAndOutcome pins the point of the command:
// pipeline health should be a number, not a feeling. Before this the only way
// to know how often the gate timed out, queued or deferred was to read
// thousands of gate.log lines by eye.
func TestGateStats_TalliesTheLogByStageAndOutcome(t *testing.T) {
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

// stamp writes one gate.log line in the format appendGateLog produces.
func stamp(at time.Time, stage, root, cmd, verdict string, secs float64) string {
	return at.UTC().Format(time.RFC3339) + " " + stage + " " + root + " " + cmd + " " + verdict + " " +
		formatFloat(secs) + "s\n"
}

// A receipt that was hand-written or unsigned is a policy event: it reaches
// gate.log, and until now nothing in `gate stats` counted it, so the one
// number that would have shown a session hand-writing a receipt was invisible.
func TestGateStats_CountsTheReceiptVerdicts(t *testing.T) {
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

// The queue bypass is a tolerated hole: anything can set it. What makes it
// tolerable is that every use is counted, so a bypass nobody expected shows up
// in the same table as every other waiver.
func TestGateStats_CountsAQueueBypass(t *testing.T) {
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

// The receipt stage's accepted, carried and rejected outcomes are counted
// side by side, so an audit can tell an accepted receipt (once it left a
// line at all, issue #136) from a stage that never ran.
func TestGateStats_CountsMutationReceiptOutcomesSideBySide(t *testing.T) {
	log := strings.Join([]string{
		stamp(time.Now().UTC(), "premergecommit", "/repo", "mutation-receipt",
			"receipt-accepted:abc123_caught=5_missed=0_accepted=0", 0),
		stamp(time.Now().UTC(), "premergecommit", "/repo", "mutation-receipt",
			"receipt-accepted:def456_caught=3_missed=0_accepted=0", 0),
		stamp(time.Now().UTC(), "premergecommit", "/repo", "mutation-receipt",
			"receipt-carried:aaa->bbb", 0),
		stamp(time.Now().UTC(), "premergecommit", "/repo", "mutation-receipt", "receipt-rejected", 0),
	}, "\n") + "\n"

	s := GateStats(strings.NewReader(log), time.Time{})
	if s.Receipts["accepted"] != 2 || s.Receipts["carried"] != 1 || s.Receipts["rejected"] != 1 {
		t.Fatalf("receipts = %v, want 2 accepted, 1 carried, 1 rejected", s.Receipts)
	}
	out := RenderGateStats(s)
	for _, want := range []string{"accepted=2", "carried=1", "rejected=1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered stats never mention %s:\n%s", want, out)
		}
	}
}

// The discard wall's refusals and its two overrides all reach gate.log —
// until this, none of them was counted, so a session that hit `reset --hard`
// twice and bypassed it twice left no number anywhere.
func TestStats_CountsDiscardRefusalsAndOverrides(t *testing.T) {
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
