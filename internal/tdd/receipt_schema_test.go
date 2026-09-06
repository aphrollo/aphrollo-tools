package tdd

// Producer-identity coverage for issue #505.

import (
	"encoding/json"
	"strings"
	"testing"
)

// A receipt written before ZeroReason existed and one written after it, by a
// producer that simply had nothing to say, were the identical bytes (issue
// #505) — every field on MutationReceipt describes the measurement, none
// described the measurer. Schema is the fix: stamped by whichever binary
// signs the receipt (signReceipt, SignReceiptFile), so a reader can finally
// tell "older producer" apart from "current producer, nothing to report".

// TestSignReceipt_StampsCurrentSchema pins the round trip a NEW receipt gets:
// whatever the caller set (or left zero), the binary that signs it stamps its
// own version, and that value survives a marshal/unmarshal unchanged.
func TestSignReceipt_StampsCurrentSchema(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	signReceipt(&r)
	if r.Schema != ReceiptSchemaVersion {
		t.Fatalf("schema = %d, want %d (signReceipt must stamp its own version)", r.Schema, ReceiptSchemaVersion)
	}

	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back MutationReceipt
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatal(err)
	}
	if back.Schema != ReceiptSchemaVersion {
		t.Fatalf("round-tripped schema = %d, want %d", back.Schema, ReceiptSchemaVersion)
	}
}

// TestSignReceipt_OverwritesACallerSuppliedSchema pins that the stamp is the
// SIGNER's claim, never the caller's: a receipt cannot claim to be older or
// newer than the binary actually vouching for it.
func TestSignReceipt_OverwritesACallerSuppliedSchema(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.Schema = 99
	signReceipt(&r)
	if r.Schema != ReceiptSchemaVersion {
		t.Fatalf("schema = %d, want signReceipt to overwrite it to %d", r.Schema, ReceiptSchemaVersion)
	}
}

// TestCheckMutationReceipt_RefusesANewerSchemaProducer is the real, reachable
// case today: a receipt naming a schema this binary has not caught up to is
// refused outright rather than read as if nothing changed, naming the fix
// (upgrade the binary) instead of guessing at fields it does not understand.
func TestCheckMutationReceipt_RefusesANewerSchemaProducer(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	body := []byte(`{"repo":"borld","branch":"lane/x","tip_tree":"` + laneTip + `","worktree_dirty":false,
	  "base_ref":"main","base_sha":"","schema":2,
	  "mutants_total":12,"caught":12,
	  "survivors":[],"unaccepted":[],
	  "verdict":"pass","finished_at":"2026-09-03T00:00:00Z"}`)
	writeSignedRawReceipt(t, laneTip, body)

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("a receipt naming a schema newer than this binary's must be refused, not read as if nothing changed")
	}
	want := "this receipt was written by a newer producer (schema 2) than this binary understands (schema 1) — upgrade aphrollo before judging it"
	if !strings.Contains(got.Message, want) {
		t.Fatalf("message = %q, want it to contain %q", got.Message, want)
	}
}

// TestOlderProducerZeroReasonMessage_NamesTheSchemaGap pins the exact wording
// checkReceiptNotVacuous names once a receipt's own schema places it behind
// this binary's. It is tested at the function level with fabricated schema
// numbers rather than through checkMutationReceipt end to end, because the
// real gap does not exist yet: ReceiptSchemaVersion is 1 today, and the only
// value below 1 a receipt can carry is 0 — which reads as "no schema field at
// all" (every receipt before this one, and unchanged by this change), not
// "older". This wording starts firing on a real receipt only once
// ReceiptSchemaVersion moves to 2; it is pinned now so the wording is right
// on that day rather than written that day under time pressure.
func TestOlderProducerZeroReasonMessage_NamesTheSchemaGap(t *testing.T) {
	got := olderProducerZeroReasonMessage(1, 2)
	want := "this receipt was written by an older producer (schema 1, this binary writes 2) — re-run `aphrollo gate mutants run` rather than checking the base"
	if got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

// TestCheckMutationReceipt_SchemaZeroStaysAmbiguous pins the honest limit
// stated in MutationReceipt.Schema's own comment: a receipt with no schema at
// all (every receipt written before this field existed) still gets the old,
// hedged message, because it is genuinely indistinguishable from a current
// producer that had nothing to report. Schema does not retroactively help
// receipts that already exist — this test is the proof it does not pretend
// to.
func TestCheckMutationReceipt_SchemaZeroStaysAmbiguous(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.MutantsTotal, r.Caught = 0, 0
	writeReceipt(t, r) // signReceipt stamps Schema = ReceiptSchemaVersion (1), same as an unset caller

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip, RepoRoot: repoWithMutationScript(t)})
	if got == nil || !got.Blocked {
		t.Fatal("an unexplained zero-mutant receipt must still be refused")
	}
	if strings.Contains(got.Message, "older producer") {
		t.Fatalf("message = %q, a schema equal to this binary's must not claim an older producer", got.Message)
	}
	if !strings.Contains(got.Message, "mutants_total is 0 and moved_lines is 0") {
		t.Fatalf("message = %q, want the original hedged vacuous message", got.Message)
	}
}
