package tdd

import (
	"encoding/json"
	"testing"
)

// FuzzReceipt feeds arbitrary bytes to the two steps a mutation receipt goes
// through BEFORE anything trusts it: verifyReceiptMAC's own probe of the raw
// bytes for a "mac" field (checkMutationReceipt calls this FIRST, on the RAW
// bytes, before ever decoding into MutationReceipt — receipt.go: "Before a
// single field is believed") and the caller's own decode into
// MutationReceipt. A receipt is written by another repo's own mutation run
// and read here — untrusted in exactly the sense a wire message is — so a
// malformed one must decode-error, never panic and never silently produce a
// MutationReceipt whose zero-value Verdict ("") a caller could mistake for
// having passed. And a well-formed receipt carrying no "mac" field at all
// must never verify: it is somebody's typing, not a measurement.
// receiptKey's "no key on this box" branch aside, most inputs here reach a
// real receiptKey() call, which creates a signing-key FILE on disk under
// ReceiptKeyPath() — this package's TestMain (main_test.go) redirects
// CLAUDE_CONFIG_DIR to a fresh os.MkdirTemp before m.Run(), and Go's native
// fuzzer runs `-fuzz` inside the SAME test binary TestMain wraps (there is
// no separate fuzz entry point), so the 60s exploration budget spends its
// time on argument variety, not on the OS/disk I/O the real config dir would
// add on every input.
func FuzzReceipt(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"verdict":"pass","mutants_total":3,"caught":3}`,
		`{"survivors":["a.rs:1: foo"]}`,
		`{"survivors":[{"file":"a.rs","line":1,"mutation":"foo"}]}`,
		`{"survivors":[{"file":"a.rs","line":1,"mutation":"foo"},"bare"]}`,
		`{"mac":"deadbeef"}`,
		`{"finished_at":"not-a-time"}`,
		`{"unaccepted":[]}`,
		`[]`,
		`null`,
		``,
		`{"survivors": [1,2,3]}`,
		`{"outcomes": "not-an-array"}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s), "repo", "tree")
	}

	f.Fuzz(func(t *testing.T, data []byte, repo, tipTree string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("receipt decode panicked on data=%q: %v", data, r)
			}
		}()
		// Called first, on the raw bytes — exactly production's order.
		macRes := verifyReceiptMAC(data, repo, tipTree)

		var probe struct {
			MAC string `json:"mac"`
		}
		if err := json.Unmarshal(data, &probe); err == nil && probe.MAC == "" {
			// The message pins the SPECIFIC reason ("unsigned"), not just
			// Blocked: a key-lookup failure or a genuine HMAC mismatch also
			// end up Blocked on this box (a signing key always exists once
			// CLAUDE_CONFIG_DIR resolves), so Blocked alone cannot tell an
			// intentionally-omitted mac from one that failed to verify for a
			// different reason — only the message says WHICH gate fired.
			const wantMsg = "gate: receipt not written by the runner (unsigned)"
			if macRes == nil || !macRes.Blocked || macRes.Message != wantMsg {
				t.Fatalf("verifyReceiptMAC(%q) = %+v, want a blocking GateResult with message %q: a receipt with no \"mac\" field must never verify", data, macRes, wantMsg)
			}
		}

		var r MutationReceipt
		if err := json.Unmarshal(data, &r); err != nil {
			// A rejected receipt is exactly the safe outcome: the caller
			// (checkMutationReceipt) treats a decode error as "unreadable
			// receipt", never as a passing one.
			return
		}
	})
}
