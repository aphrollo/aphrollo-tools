package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The producer writes each survivor as an OBJECT — {"file","line","mutation"}
// — and the consumer declared a list of strings, so the first lane with one
// accepted survivor died at merge on "cannot unmarshal object into Go struct
// field MutationReceipt.survivors of type string". The zero-survivor fixture
// never exercised the shape; this one is real producer output with one
// accepted equivalent mutant.
func TestMutationReceipt_DecodesObjectSurvivorsAndMergesWhenAllAccepted(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	data, err := os.ReadFile(filepath.Join("testdata", "mutation-receipt.borld-accepted.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r MutationReceipt
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("the producer's survivor shape must decode: %v", err)
	}
	if len(r.Survivors) != 1 || len(r.Unaccepted) != 0 || r.Accepted != 1 {
		t.Fatalf("survivors = %v, unaccepted = %v, accepted = %d", r.Survivors, r.Unaccepted, r.Accepted)
	}
	writeReceipt(t, r)
	if got := checkMutationReceipt(receiptContext{Repo: r.Repo, TipTree: r.TipTree, BaseSHA: r.BaseSHA}); got != nil {
		t.Fatalf("an accepted survivor must merge: %s", got.Message)
	}
}

// An unaccepted survivor in the object shape is still refused, and the
// rejection names it as file:line so the reader can go there.
func TestMutationReceipt_RefusesAnUnacceptedObjectSurvivorByFileAndLine(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	body := `{"repo":"borld","branch":"lane/x","tip_tree":"` + laneTip + `","worktree_dirty":false,
	  "base_ref":"main","base_sha":"","mutants_total":2,"caught":1,"timeout":0,"unviable":0,
	  "survivors":[{"file":"src/a.rs","line":12,"mutation":"replace + with -"}],
	  "unaccepted":[{"file":"src/a.rs","line":12,"mutation":"replace + with -"}],
	  "verdict":"pass","accepted":0,"finished_at":"2026-09-03T00:00:00Z"}`
	var r MutationReceipt
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, r)
	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("an unaccepted survivor must not merge")
	}
	if !strings.Contains(got.Message, "src/a.rs:12: replace + with -") {
		t.Fatalf("the rejection must name file:line: mutation, got: %q", got.Message)
	}
}

// A receipt written by an older producer that named survivors as plain
// strings still decodes: the shape is the producer's, and both spellings
// count as one survivor each.
func TestMutationReceipt_StillDecodesStringSurvivors(t *testing.T) {
	var r MutationReceipt
	body := `{"survivors":["src/a.rs:12: replace + with -"],"unaccepted":["src/a.rs:12: replace + with -"]}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Unaccepted) != 1 || r.Unaccepted[0].String() != "src/a.rs:12: replace + with -" {
		t.Fatalf("unaccepted = %v", r.Unaccepted)
	}
}
