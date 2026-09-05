package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A receipt that fails its own arithmetic is not one the gate can reason
// about (issue #339). The real case: mutants_total=34, caught=28,
// survivors=4, accepted=41 — 41 accepted out of 34 measured. Nothing checked
// the receipt against itself, so the impossible number merged silently.

// TestCheckMutationReceipt_RefusesAcceptedExceedingMutantsTotal pins the exact
// shape of the real defect: a producer writing its accept-LIST size where the
// contract wants the survivors THIS RUN accepted.
func TestCheckMutationReceipt_RefusesAcceptedExceedingMutantsTotal(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.MutantsTotal, r.Caught, r.Accepted = 34, 28, 41
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("a receipt accepting more than it measured merged")
	}
	if !strings.Contains(got.Message, "41") || !strings.Contains(got.Message, "34") {
		t.Fatalf("message = %q, want both numbers named", got.Message)
	}
	if !strings.Contains(got.Message, receiptRejectionMarker) {
		t.Fatalf("message = %q, want the same remedy every receipt refusal carries", got.Message)
	}
}

// TestCheckMutationReceipt_RefusesCategorySumExceedingMutantsTotal pins the
// second invariant: caught + survivors + timeout + unviable must not exceed
// what was measured.
func TestCheckMutationReceipt_RefusesCategorySumExceedingMutantsTotal(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.MutantsTotal, r.Caught, r.Unviable = 5, 4, 4
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("a receipt whose categories overcount its own total merged")
	}
	if !strings.Contains(got.Message, "8") || !strings.Contains(got.Message, "5") {
		t.Fatalf("message = %q, want both numbers named", got.Message)
	}
}

// TestCheckMutationReceipt_RefusesMoreUnacceptedThanSurvivors pins the third
// invariant: an unaccepted mutant is a survivor, so there can never be more
// unaccepted entries than survivor entries.
func TestCheckMutationReceipt_RefusesMoreUnacceptedThanSurvivors(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.MutantsTotal, r.Caught = 2, 0
	r.Survivors = []MutantName{{File: "src/a.rs", Line: 12, Mutation: "replace + with -"}}
	r.Unaccepted = []MutantName{
		{File: "src/a.rs", Line: 12, Mutation: "replace + with -"},
		{File: "src/b.rs", Line: 3, Mutation: "replace * with +"},
	}
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("a receipt with more unaccepted than survivor entries merged")
	}
	if !strings.Contains(got.Message, "2") || !strings.Contains(got.Message, "1") {
		t.Fatalf("message = %q, want both counts named", got.Message)
	}
}

// TestCheckMutationReceipt_RefusesAnUnacceptedEntryNotInSurvivors pins the
// fourth invariant: every unaccepted entry names a mutant that is ALSO in
// survivors, never one invented independently.
func TestCheckMutationReceipt_RefusesAnUnacceptedEntryNotInSurvivors(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.MutantsTotal, r.Caught = 1, 0
	r.Survivors = []MutantName{{File: "src/a.rs", Line: 12, Mutation: "replace + with -"}}
	r.Unaccepted = []MutantName{{File: "src/z.rs", Line: 9, Mutation: "replace == with !="}}
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("an unaccepted entry that names no survivor merged")
	}
	if !strings.Contains(got.Message, "src/z.rs:9") {
		t.Fatalf("message = %q, want the orphaned entry named", got.Message)
	}
}

// TestCheckMutationReceipt_ToleratesAnOlderProducerThatOmitsMutantsTotal pins
// the escape hatch: a field an older producer never wrote at all defaults to
// Go's zero value on decode, and that zero must never be read as a violated
// invariant — only a field the wire bytes actually carried is judged. Here
// mutants_total and accepted are absent from the wire entirely, so the
// accepted>total invariant must not even ask the question.
func TestCheckMutationReceipt_ToleratesAnOlderProducerThatOmitsMutantsTotal(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	body := []byte(`{"repo":"borld","branch":"lane/x","tip_tree":"` + laneTip + `","worktree_dirty":false,
	  "base_ref":"main","base_sha":"",
	  "survivors":[],"unaccepted":[],
	  "verdict":"pass","finished_at":"2026-09-03T00:00:00Z"}`)
	writeSignedRawReceipt(t, laneTip, body)

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got != nil {
		t.Fatalf("a receipt missing mutants_total entirely must not be judged on it: %s", got.Message)
	}
}

// writeSignedRawReceipt writes body verbatim (a hand-built payload a real
// struct marshal would never omit a zero-valued int field from) with a valid
// MAC over it, so the receipt clears signature verification and only the
// coherence question under test is live.
func writeSignedRawReceipt(t *testing.T, tipTree string, body []byte) {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatal(err)
	}
	delete(obj, "mac")
	canon, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	key, err := receiptKey()
	if err != nil {
		t.Fatal(err)
	}
	mac, err := receiptMAC(canon, key)
	if err != nil {
		t.Fatal(err)
	}
	obj["mac"] = mac
	signed, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	path := MutationReceiptPathFor(tipTree)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, signed, 0o600); err != nil {
		t.Fatal(err)
	}
}
