package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// borldReceipt is REAL producer output, checked in unchanged: a schema whose
// only reader is its own writer is untested by construction, and this pair
// disagreed in production — the producer writes survivors as an ARRAY of
// names, the consumer declared an int, so every merge died on
// "cannot unmarshal array into Go struct field MutationReceipt.survivors".
//
// It is ALSO, independently, real evidence of the defect issue #339 closes:
// mutants_total=0, caught=0, timeout=0, unviable=0, survivors=0, accepted=29
// — a run that measured nothing cannot have accepted twenty-nine of it. The
// producer bug that wrote this (borld's tools/mutation_gate.sh, writing its
// accept-list's own size rather than a count of mutants THIS run measured
// and accepted) predates this fixture and is fixed on that side; the bytes
// are kept exactly as captured, as the reproduction, and
// TestMutationReceipt_RefusesKnownWrongProducerOutputWhereAcceptedExceedsTotal
// below is what now reads it correctly.
func borldReceipt(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "mutation-receipt.borld.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// borldAcceptedReceipt is real producer output too, and internally coherent:
// mutants_total=131, caught=130, one survivor, accepted=1 — 130+1+0+0=131 and
// 1<=131, so it is the fixture the "a real receipt merges" guard belongs on.
func borldAcceptedReceipt(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "mutation-receipt.borld-accepted.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestMutationReceipt_DecodesRealProducerOutput(t *testing.T) {
	var r MutationReceipt
	if err := json.Unmarshal(borldReceipt(t), &r); err != nil {
		t.Fatalf("the producer's own receipt must decode: %v", err)
	}
	if r.TipTree != "c32524cd06d5d882efefcdc5ceff75b50c2e6d82" {
		t.Errorf("tip_tree = %q", r.TipTree)
	}
	if r.BaseSHA != "10bc2934c617a2f61d6dad156cd6e238f3dfab91" {
		t.Errorf("base_sha = %q", r.BaseSHA)
	}
	if r.Verdict != "pass" || r.Accepted != 29 {
		t.Errorf("verdict = %q, accepted = %d", r.Verdict, r.Accepted)
	}
	if len(r.Survivors) != 0 || len(r.Unaccepted) != 0 {
		t.Errorf("survivors = %v, unaccepted = %v", r.Survivors, r.Unaccepted)
	}
}

// The whole file, judged: a real passing receipt clears the merge it was
// written for, when it runs in the SAME checkout the receipt names (this
// test's own repo-identity question is TestMutationReceipt_
// MatchesARepoNamedByItsGitDir below; here `repo` is just r.Repo itself, so
// this asks the same verdict/base_sha question the merge gate asks without
// re-testing the identity comparison). Pointed at borldAcceptedReceipt,
// which is coherent, so this guard proves the gate does not over-tighten —
// see TestMutationReceipt_RefusesKnownWrongProducerOutputWhereAcceptedExceedsTotal
// for the sibling fixture this guard used to be pointed at, which is not.
func TestMutationReceipt_AcceptsRealProducerOutput(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var r MutationReceipt
	if err := json.Unmarshal(borldAcceptedReceipt(t), &r); err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, r)

	if got := checkMutationReceipt(receiptContext{Repo: r.Repo, TipTree: r.TipTree, BaseSHA: r.BaseSHA}); got != nil {
		t.Fatalf("a real passing receipt must merge: %s", got.Message)
	}
}

// TestMutationReceipt_RefusesKnownWrongProducerOutputWhereAcceptedExceedsTotal
// is borldReceipt (mutation-receipt.borld.json) read correctly: mutants_total
// is 0 and accepted is 29, so no run could have produced this pair honestly.
// The receipt is KNOWN-WRONG real output (issue #339) and is kept checked in
// deliberately, unedited, as the reproduction — this test's claim is that the
// gate now refuses it, never that the numbers are valid.
func TestMutationReceipt_RefusesKnownWrongProducerOutputWhereAcceptedExceedsTotal(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var r MutationReceipt
	if err := json.Unmarshal(borldReceipt(t), &r); err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{Repo: r.Repo, TipTree: r.TipTree, BaseSHA: r.BaseSHA})
	if got == nil || !got.Blocked {
		t.Fatal("a receipt accepting more mutants than it measured merged")
	}
	if !strings.Contains(got.Message, "29") || !strings.Contains(got.Message, "accepted") {
		t.Fatalf("message = %q, want the impossible accepted count named", got.Message)
	}
}

// The producer names the repo by its git COMMON dir (`D:/Projects/borld/.git`);
// the gate names the checkout doing the merge by the same thing (commonGitDir,
// resolved from a real repoRoot). The two spellings that must still converge
// are "already a .git dir" (the producer's) and "a bare repo root" (what a
// bug-report reproduction or an older caller might pass) — refusing that pair
// as "this receipt is for another repo" would be the most misleading
// rejection the gate can produce, since it is the SAME repo.
func TestMutationReceipt_MatchesARepoNamedByItsGitDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.Repo = "D:/Projects/borld/.git"
	writeReceipt(t, r)

	if got := checkMutationReceipt(receiptContext{Repo: "D:/Projects/borld", TipTree: laneTip}); got != nil {
		t.Fatalf("a bare repo root must match the producer's own git-dir spelling: %s", got.Message)
	}
}

// A survivor nobody signed off on is still the rule, and the name the
// producer wrote is what the rejection quotes.
func TestMutationReceipt_RefusesUnacceptedSurvivorsByName(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.Survivors = []MutantName{{Raw: "src/a.rs:12: replace + with -"}}
	r.Unaccepted = []MutantName{{Raw: "src/a.rs:12: replace + with -"}}
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("an unaccepted survivor must not merge")
	}
	if !strings.Contains(got.Message, "src/a.rs:12") {
		t.Fatalf("the rejection must quote the survivor, got: %q", got.Message)
	}
}
