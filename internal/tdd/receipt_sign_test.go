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

// The signing runner has merged, so nothing legitimate is unsigned any more
// (issue #115): a receipt with no mac at all — tip and base matching the
// merge exactly — is refused the same as a forged one, not waved through.
func TestReceiptSigning_RefusesAnUnsignedReceiptEvenWithAMatchingTipAndBase(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	r := passingReceipt()
	r.BaseSHA = "abc123"
	writeUnsignedReceipt(t, r)

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: r.TipTree, BaseSHA: "abc123"})
	if got == nil || !got.Blocked {
		t.Fatal("an unsigned receipt must not merge, matching tip and base or not")
	}
	if got.Message != "gate: receipt not written by the runner (unsigned)" {
		t.Fatalf("message = %q, want the exact unsigned refusal", got.Message)
	}
	requireLoggedVerdict(t, cfg, "receipt-forged")
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
func TestSignReceiptFile_OutcomesSHAIsTheHashOfTheOutcomesFileBytes(t *testing.T) {
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
	// sha256(`{"mutants":[]}`), computed independently of fileSHA256 —
	// literal, not a value the production hasher could also produce for
	// the wrong input. A mutation that hashes a different field (the
	// receipt path, an empty slice, r.TipTree) still yields 64 hex chars
	// but not THIS value.
	const wantOutcomesSHA = "d047b4feb927b06e9dcee9fca1677d72296bdf11225f3c57e33df42bd4b668a4"
	if signed.OutcomesSHA != wantOutcomesSHA {
		t.Fatalf("outcomes_sha = %q, want %q (sha256 of the outcomes file's bytes)", signed.OutcomesSHA, wantOutcomesSHA)
	}
	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: r.TipTree}); got != nil {
		t.Fatalf("the signed receipt must merge: %s", got.Message)
	}
}
