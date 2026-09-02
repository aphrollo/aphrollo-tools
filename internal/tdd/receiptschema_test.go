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
func borldReceipt(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "mutation-receipt.borld.json"))
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
// written for. Its base_sha is judged only against a base the gate can name,
// so this asks the same question the merge gate asks.
func TestMutationReceipt_AcceptsRealProducerOutput(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var r MutationReceipt
	if err := json.Unmarshal(borldReceipt(t), &r); err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, r)

	if got := checkMutationReceipt("borld", r.TipTree, r.BaseSHA); got != nil {
		t.Fatalf("a real passing receipt must merge: %s", got.Message)
	}
}

// The producer names the repo by its git dir. Refusing that reads as "this
// receipt is for another repo", which is the most misleading rejection the
// gate can produce — it is the SAME repo.
func TestMutationReceipt_MatchesARepoNamedByItsGitDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.Repo = "D:/Projects/borld/.git"
	writeReceipt(t, r)

	if got := checkMutationReceipt("borld", laneTip, ""); got != nil {
		t.Fatalf("the producer's own repo spelling must be accepted: %s", got.Message)
	}
}

// A survivor nobody signed off on is still the rule, and the name the
// producer wrote is what the rejection quotes.
func TestMutationReceipt_RefusesUnacceptedSurvivorsByName(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.Survivors = []string{"src/a.rs:12: replace + with -"}
	r.Unaccepted = []string{"src/a.rs:12: replace + with -"}
	writeReceipt(t, r)

	got := checkMutationReceipt("borld", laneTip, "")
	if got == nil || !got.Blocked {
		t.Fatal("an unaccepted survivor must not merge")
	}
	if !strings.Contains(got.Message, "src/a.rs:12") {
		t.Fatalf("the rejection must quote the survivor, got: %q", got.Message)
	}
}
