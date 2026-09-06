package tdd

import (
	"os"
	"path/filepath"
	"testing"
)

// shellProducerReceiptJSON is a receipt in tools/mutation_gate.sh's own
// shape: every count written directly, survivors and unaccepted written as
// "[]" (an empty, non-nil array — the tell recountReceipt's old unconditional
// zeroing destroyed, see issue #445), and NO "outcomes" key at all. A Rust
// consumer's producer never emits Outcomes; recounting from it is not merely
// unsupported, there is nothing there to recount from.
const shellProducerReceiptJSON = `{
	"repo": "borld",
	"tip_tree": "` + laneTip + `",
	"branch": "lane/recount",
	"base_ref": "origin/main",
	"base_sha": "deadbeef",
	"mutants_total": 11,
	"caught": 8,
	"timeout": 0,
	"unviable": 3,
	"accepted": 0,
	"survivors": [],
	"unaccepted": [],
	"verdict": "pass",
	"finished_at": "2026-09-05T23:07:51Z"
}`

// TestAdoptCarriedOutcomes_KeepsAShellProducersCountsWhenOutcomesIsAbsent is
// the break: recountReceipt used to zero MutantsTotal/Caught/Timeout/Unviable
// and null out Survivors/Unaccepted whenever Outcomes was empty, with no
// regard for WHY it was empty. A shell producer's receipt has no Outcomes to
// rebuild from — its counts ARE the measurement — so adoptCarriedOutcomes
// (which recounts every receipt it touches, even when nothing was carried)
// must leave them exactly as written.
func TestAdoptCarriedOutcomes_KeepsAShellProducersCountsWhenOutcomesIsAbsent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := MutationReceiptPathFor(laneTip)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("could not create the receipt dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(shellProducerReceiptJSON), 0o600); err != nil {
		t.Fatalf("could not write the fixture receipt: %v", err)
	}

	adoptCarriedOutcomes(MutantsJob{Repo: "borld", TipTree: laneTip}, nil, TreeState{})

	got, ok := readReceiptFile(path)
	if !ok {
		t.Fatal("the receipt disappeared")
	}
	if got.MutantsTotal != 11 || got.Caught != 8 || got.Timeout != 0 || got.Unviable != 3 {
		t.Fatalf("counts = total %d caught %d timeout %d unviable %d, want 11/8/0/3 — the producer's own measurement, untouched",
			got.MutantsTotal, got.Caught, got.Timeout, got.Unviable)
	}
	if got.Survivors == nil || got.Unaccepted == nil {
		t.Fatalf("Survivors = %#v, Unaccepted = %#v, want the producer's own empty (non-nil) lists — nil is what the zeroing bug produced",
			got.Survivors, got.Unaccepted)
	}
}

// TestAdoptCarriedOutcomes_SignsAShellProducersReceiptCorrectly checks the other
// half: the counts surviving is worthless if the re-signed MAC does not
// verify — a merge that cannot check the signature refuses just the same as
// one that reads zeroed counts.
func TestAdoptCarriedOutcomes_SignsAShellProducersReceiptCorrectly(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := MutationReceiptPathFor(laneTip)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("could not create the receipt dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(shellProducerReceiptJSON), 0o600); err != nil {
		t.Fatalf("could not write the fixture receipt: %v", err)
	}

	adoptCarriedOutcomes(MutantsJob{Repo: "borld", TipTree: laneTip}, nil, TreeState{})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read the signed receipt back: %v", err)
	}
	if res := verifyReceiptMAC(data, "borld", laneTip); res != nil {
		t.Fatalf("the re-signed receipt does not verify: %s", res.Message)
	}
}
