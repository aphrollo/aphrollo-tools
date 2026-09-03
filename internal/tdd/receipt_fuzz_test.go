package tdd

import (
	"encoding/json"
	"testing"
)

// FuzzReceipt feeds arbitrary bytes to the two steps a mutation receipt goes
// through BEFORE anything trusts it: the caller's own decode into
// MutationReceipt, and verifyReceiptMAC's own probe of the raw bytes for a
// "mac" field. A receipt is written by another repo's own mutation run and
// read here — untrusted in exactly the sense a wire message is — so a
// malformed one must decode-error, never panic and never silently produce a
// MutationReceipt whose zero-value Verdict ("") a caller could mistake for
// having passed.
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
		var r MutationReceipt
		if err := json.Unmarshal(data, &r); err != nil {
			// A rejected receipt is exactly the safe outcome: the caller
			// (checkMutationReceipt) treats a decode error as "unreadable
			// receipt", never as a passing one.
			return
		}
		// verifyReceiptMAC only ever sees bytes that already decoded above,
		// so it must stay panic-free on the same corpus, whatever verdict it
		// reaches about the signature.
		_ = verifyReceiptMAC(data, repo, tipTree)
	})
}
