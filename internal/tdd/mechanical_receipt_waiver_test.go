package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// A catch-up merge (main into a lane) and a repo whose proof is measured in
// CI are both WAIVERS of the mutation-receipt stage, not refusals — but
// Mechanical used to treat every non-nil result the stage returned as a
// short-circuit, logging it as "receipt-rejected" and returning before
// baselineStage, ratchetStage, docsCheckStage or a single suite ran. The
// merged tree — lane plus main, the tree nobody has tested — went unchecked
// for exactly the merges the waiver was supposed to still gate (issue #396).
//
// TestMechanical_CatchUpMergeStillRunsTheStagesAndSuites pins the fix for the
// first waiver shape: only a BLOCKING receipt result short-circuits: a
// waiver falls through to every other stage, its note riding along in the
// final result.
func TestMechanical_CatchUpMergeStillRunsTheStagesAndSuites(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := goReceiptRepo(t, "[aphrollo]\nmutation-receipt = true\n")
	startMerge(t, root, "lane/x", "main")
	write(t, root, "extra.go", "package m\n\nfunc Extra() int { return 3 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("a catch-up merge waiver must never block: %s", res.Message)
	}
	if len(seen) == 0 {
		t.Fatal("a catch-up merge waiver skipped the mechanical stages/suites entirely — the merged tree went unchecked")
	}
	if !strings.Contains(res.Message, "mutation receipt not judged") {
		t.Fatalf("final result must carry the waiver note, got Message=%q", res.Message)
	}
	requireLoggedVerdict(t, cfg, "catchup-merge")
	if strings.Contains(gateLogText(t, cfg), "receipt-rejected") {
		t.Fatalf("a waiver must never log as receipt-rejected, got:\n%s", gateLogText(t, cfg))
	}
}

// TestMechanical_CIJudgedRepoStillRunsTheStages pins the fix for the second
// waiver shape: `mutants-local = false` stands the receipt stage down, and
// the merge still owes every other stage the combined tree.
func TestMechanical_CIJudgedRepoStillRunsTheStages(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := goReceiptRepo(t, "[aphrollo]\nmutation-receipt = true\nmutants-local = false\n")
	startMerge(t, root, "main", "lane/x")
	write(t, root, "extra.go", "package m\n\nfunc Extra() int { return 3 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("a CI-judged repo's merge must never block on the receipt stage: %s", res.Message)
	}
	if len(seen) == 0 {
		t.Fatal("a CI-judged repo's waiver skipped the mechanical stages/suites entirely")
	}
	if !strings.Contains(res.Message, "measures it on the CI runner") {
		t.Fatalf("final result must carry the waiver note, got Message=%q", res.Message)
	}
	requireLoggedVerdict(t, cfg, "receipt-measured-in-ci")
	if strings.Contains(gateLogText(t, cfg), "receipt-rejected") {
		t.Fatalf("a waiver must never log as receipt-rejected, got:\n%s", gateLogText(t, cfg))
	}
}

// TestMechanical_ReceiptRefusalStillShortCircuits guards the direction that
// matters: a genuine refusal (a lane merging into main with no receipt at
// all) still short-circuits before any suite runs, and still logs
// receipt-rejected — the waiver fix must never turn into "nothing refuses a
// merge anymore".
func TestMechanical_ReceiptRefusalStillShortCircuits(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := receiptRepo(t)
	startMerge(t, root, "main", "lane/x")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, root))
	if !res.Blocked {
		t.Fatalf("a lane merge with no mutation receipt must still refuse, got %+v", res)
	}
	if len(seen) != 0 {
		t.Fatalf("a refused receipt must short-circuit before any suite runs, ran %+v", seen)
	}
	requireLoggedVerdict(t, cfg, "receipt-rejected")
}

// TestMechanical_CatchUpMergeStillRefusesOnARatchetHit proves the fall-through
// actually reaches ratchetStage: a waived receipt is not a waived MERGE — a
// law regression staged during a catch-up merge still refuses it.
func TestMechanical_CatchUpMergeStillRefusesOnARatchetHit(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := goReceiptRepo(t, "[aphrollo]\nmutation-receipt = true\n")
	// The law must exist on the branch the merge lands ON (lane/x, HEAD at
	// merge time) — adding it on main only would leave it absent from
	// lane/x's checked-out working tree the moment startMerge switches there.
	gitDo(t, root, "checkout", "-q", "lane/x")
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "no-badclamp.toml"), `
name = "no-badclamp"
description = "BADCLAMP is a placeholder for a real guard"
severity = "deny"
escape = "// nan-safe:"
baseline = ".ratchet/baselines/no-badclamp.txt"

[scope]
include = ["**/*.go"]

[matcher]
kind = "regex-absent"
pattern = "BADCLAMP"
`)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "no-badclamp.txt"), "")
	gitAddAll(t, root)
	gitDo(t, root, "commit", "-qm", "add law")

	startMerge(t, root, "lane/x", "main")
	write(t, root, "bad.go", "package m\n\nfunc Bad() int { return 0 } // BADCLAMP\n")
	gitDo(t, root, "add", ".")

	res := Mechanical(root, RunSuite(precommitTestTimeout))
	if !res.Blocked {
		t.Fatalf("a ratchet regression staged during a catch-up merge must still refuse, got %+v", res)
	}
	if !strings.Contains(res.Message, "no-badclamp") {
		t.Fatalf("expected the ratchet law named in the block, got %q", res.Message)
	}
}
