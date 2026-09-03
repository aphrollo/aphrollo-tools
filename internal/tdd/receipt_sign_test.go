package tdd

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
)

// A session hand-wrote a mutation receipt and merged on it. Fail-first and
// the receipt are the two halves of the proof a merge needs, and a proof
// anyone can type is not one — so a receipt carries a MAC over its own body,
// keyed by a secret only this box's runner and gate share.
func TestReceiptSigning_VerifiesWhatTheCommandWroteAndRejectsAnEditedByte(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	writeReceipt(t, r)
	path := MutationReceiptPathFor(r.TipTree)

	if err := SignReceiptFile(path, ""); err != nil {
		t.Fatalf("signing the receipt: %v", err)
	}
	signed := readReceipt(t, r.TipTree)
	if signed.MAC == "" {
		t.Fatal("the signer left no MAC on the receipt")
	}
	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: r.TipTree}); got != nil {
		t.Fatalf("a receipt the signer wrote must merge: %s", got.Message)
	}

	// One byte of the body changed: the numbers no longer describe the run
	// the MAC was taken over.
	signed.Caught = 99
	writeReceiptFile(path, signed)
	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: r.TipTree})
	if got == nil || !got.Blocked {
		t.Fatal("an edited receipt must not merge")
	}
	if !strings.Contains(got.Message, "not written by tools/mutation_gate.sh") {
		t.Fatalf("message = %q, want it to say the receipt was not written by the runner", got.Message)
	}
}

// A forged receipt is a fact about a session, not a typo: it is counted.
func TestReceiptSigning_LogsAForgedReceipt(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	r := passingReceipt()
	r.MAC = "0123456789abcdef"
	writeReceipt(t, r)

	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: r.TipTree}); got == nil || !got.Blocked {
		t.Fatal("a receipt with a MAC that does not verify must not merge")
	}
	requireLoggedVerdict(t, cfg, "receipt-forged")
}

// The producers have not all caught up: a receipt with NO mac at all is still
// accepted, and counted, exactly like the older receipts that carry no
// base_sha. Refusing them would break every lane on the day this ships.
func TestReceiptSigning_AcceptsButCountsAnUnsignedReceipt(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	writeReceipt(t, passingReceipt())

	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip}); got != nil {
		t.Fatalf("an unsigned receipt must still merge for now: %s", got.Message)
	}
	requireLoggedVerdict(t, cfg, "receipt-unsigned")
}

// The key is per-machine and private: a key anybody can read is a key
// anybody can sign with.
func TestReceiptKey_IsCreatedOncePrivateToThisUser(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	key, err := receiptKey()
	if err != nil {
		t.Fatalf("creating the key: %v", err)
	}
	if len(key) < 32 {
		t.Fatalf("key is %d bytes, want at least 32", len(key))
	}
	again, err := receiptKey()
	if err != nil || string(again) != string(key) {
		t.Fatalf("the key must be created once and kept: %v", err)
	}
	info, err := os.Stat(ReceiptKeyPath())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// Windows maps a Go file mode onto the read-only bit alone, so the
		// group/other bits below say nothing there. The mode is asserted where
		// it means something, which is also where CI runs.
		return
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("key mode = %v, want it unreadable by anyone else", perm)
	}
}

// The MAC covers the BODY, not the file's whitespace: a receipt reformatted
// on its way through a tool still verifies, and any change to what it CLAIMS
// does not.
func TestReceiptSigning_CoversTheBodyRatherThanTheFormatting(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	writeReceipt(t, r)
	path := MutationReceiptPathFor(r.TipTree)
	if err := SignReceiptFile(path, ""); err != nil {
		t.Fatal(err)
	}
	signed := readReceipt(t, r.TipTree)

	pretty, err := json.MarshalIndent(signed, "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pretty, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: r.TipTree}); got != nil {
		t.Fatalf("re-indenting a receipt must not break its proof: %s", got.Message)
	}
}

// The signer also records what the run's own outcome file hashed to, so the
// receipt names the evidence it was taken from.
func TestSignReceiptFile_RecordsTheOutcomesHash(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	writeReceipt(t, r)
	outcomes := t.TempDir() + "/outcomes.json"
	if err := os.WriteFile(outcomes, []byte(`{"mutants":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SignReceiptFile(MutationReceiptPathFor(r.TipTree), outcomes); err != nil {
		t.Fatal(err)
	}
	signed := readReceipt(t, r.TipTree)
	if len(signed.OutcomesSHA) != 64 {
		t.Fatalf("outcomes_sha = %q, want a sha256 of the run's outcome file", signed.OutcomesSHA)
	}
	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: r.TipTree}); got != nil {
		t.Fatalf("the signed receipt must merge: %s", got.Message)
	}
}
