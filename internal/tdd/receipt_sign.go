package tdd

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// A session hand-wrote a mutation receipt and merged on it. Everything else
// in this gate is mechanical, so the one artefact a human could type was the
// one that decided a merge — which makes it the weakest link, not the
// strongest.
//
// So a receipt carries a MAC over its own body, keyed by a per-machine secret
// the runner and the gate share, and `aphrollo gate receipt sign` is the ONLY
// thing that writes one. Nothing here is a defence against an attacker: a
// session that can run the signer can sign anything. It is a defence against
// the receipt being written by ANYTHING OTHER than a run — which is exactly
// the failure that happened.
//
// A receipt with no mac at all is refused the same as one whose MAC does not
// verify: "not measured" and "measured and then edited" are different
// claims, but neither is a run's own signature.

// receiptKeyName is the per-machine signing secret, beside the receipts it
// signs.
const receiptKeyName = "receipt.key"

// ReceiptKeyPath is where that secret lives.
func ReceiptKeyPath() string {
	dir := stateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, receiptKeyName)
}

// receiptKey reads the machine's signing secret, creating it the first time.
// Written 0600 and never logged: a key anybody can read is a key anybody can
// sign with.
//
// Absent and malformed are different facts and get different handling
// (issue #429): a file that is not there yet is the ordinary first-run case
// and gets a fresh key generated silently. A file that IS there but shorter
// than a usable key is truncation evidence — a partial write, a full disk,
// an interrupted first run, a restored backup — and is reported with its
// path and length rather than silently overwritten. Overwriting it would
// make the next receipt look normal while erasing the evidence, and every
// receipt honestly signed under the lost key would then fail its MAC check
// and read as forged, which is a different and false claim.
func receiptKey() ([]byte, error) {
	path := ReceiptKeyPath()
	if path == "" {
		return nil, errors.New("no state dir, so nowhere to keep a signing key")
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) >= 32 {
			return data, nil
		}
		return nil, fmt.Errorf("receipt signing key %s is %d bytes, want at least 32 -- truncated or corrupt, not absent; move it aside and re-run to generate a fresh one", path, len(data))
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	// A pre-existing file created with a laxer mode (an older init, a copied
	// state dir) is tightened rather than trusted.
	_ = os.Chmod(path, 0o600)
	return key, nil
}

// EnsureReceiptKey creates the signing secret if it is not there yet, and
// reports whether it made one. `gate init` calls it so the first mutation run
// on a box finds a key rather than making one mid-run.
func EnsureReceiptKey() (bool, error) {
	path := ReceiptKeyPath()
	if path == "" {
		return false, errors.New("no state dir, so nowhere to keep a signing key")
	}
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if _, err := receiptKey(); err != nil {
		return false, err
	}
	return true, nil
}

// receiptMAC is the MAC over a receipt's canonical body: the JSON object with
// the `mac` field removed and every key sorted. Canonical rather than
// literal, so a receipt that is re-indented or re-ordered on its way through
// a tool still verifies while any change to what it CLAIMS does not.
func receiptMAC(body []byte, key []byte) (string, error) {
	canon, err := canonicalReceiptBody(body)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(canon)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// canonicalReceiptBody renders a receipt's JSON with the mac field removed
// and keys sorted (Go marshals a map's keys in sorted order).
func canonicalReceiptBody(body []byte) ([]byte, error) {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	delete(obj, "mac")
	return json.Marshal(obj)
}

// SignReceiptFile stamps a receipt with the sha256 of the run's own outcome
// file (when one is named) and the MAC over the result, in place. It is the
// ONLY writer of a receipt's mac.
func SignReceiptFile(path, outcomesPath string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	if outcomesPath != "" {
		sum, err := fileSHA256(outcomesPath)
		if err != nil {
			return err
		}
		obj["outcomes_sha"] = sum
	}
	// Stamped here, not left to the producer, the same reasoning as
	// outcomes_sha above: this is the ONE choke point every producer's raw
	// bytes pass through before they are trusted (borld's tools/mutation_gate.sh
	// included), so it is where "which binary is vouching for this receipt"
	// gets recorded rather than left to a shell script that has never heard
	// of the field. Overwritten unconditionally, same as outcomes_sha: a
	// caller cannot claim an older or newer schema than the binary signing it
	// actually is.
	obj["schema"] = ReceiptSchemaVersion
	delete(obj, "mac")
	body, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	key, err := receiptKey()
	if err != nil {
		return err
	}
	mac, err := receiptMAC(body, key)
	if err != nil {
		return err
	}
	obj["mac"] = mac
	signed, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, signed)
}

// signReceipt signs a receipt this process itself is writing — the carried
// copy, whose tip tree differs from the one that was measured. The gate is a
// trusted writer: it re-stamps a proof it has just verified, and a carried
// receipt that kept the old MAC would read as forged.
func signReceipt(r *MutationReceipt) {
	r.MAC = ""
	// Overwritten unconditionally, same reasoning as SignReceiptFile's own
	// "schema" stamp: this is the binary about to vouch for the receipt, so
	// its version is what gets recorded, not whatever a caller happened to
	// set on the struct.
	r.Schema = ReceiptSchemaVersion
	body, err := json.Marshal(r)
	if err != nil {
		return
	}
	key, err := receiptKey()
	if err != nil {
		return
	}
	if mac, err := receiptMAC(body, key); err == nil {
		r.MAC = mac
	}
}

// verifyReceiptMAC judges a receipt's signature. nil allows.
func verifyReceiptMAC(data []byte, repo, tipTree string) *GateResult {
	var probe struct {
		MAC string `json:"mac"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil // the caller's own decode reports an unreadable receipt
	}
	if probe.MAC == "" {
		appendGateLog(premergeLogToken, logToken(repo), "mutation-receipt", "receipt-forged", 0)
		return &GateResult{Blocked: true, Message: "gate: receipt not written by the runner (unsigned)"}
	}
	key, err := receiptKey()
	if err != nil {
		// A signature that cannot be checked is never a valid one. This used
		// to stand down and allow — "the gate's own blind spot must not
		// reject somebody's proof" — but that reasoning let a non-empty
		// fabricated mac verify as allowed on any box where the key could
		// not be read: no HOME/USERPROFILE (a real condition on a service
		// account or stripped container), a MkdirAll or ReadFile failure, or
		// any other transient error. Every one of those is now loud and
		// blocking (issue #281); reject over substitute.
		appendGateLog(premergeLogToken, logToken(repo), "mutation-receipt", "receipt-unverifiable", 0)
		return &GateResult{Blocked: true, Message: "gate: the receipt's signing key could not be read, so its signature cannot be verified: " + err.Error()}
	}
	want, err := receiptMAC(data, key)
	if err != nil || !hmac.Equal([]byte(want), []byte(probe.MAC)) {
		appendGateLog(premergeLogToken, logToken(repo), "mutation-receipt", "receipt-forged", 0)
		return &GateResult{Blocked: true, Message: "gate: receipt not written by tools/mutation_gate.sh — the mutation receipt for tree " +
			short(tipTree) + " does not verify against this machine's signing key"}
	}
	return nil
}

// fileSHA256 hashes a file, for the outcomes_sha a receipt names its evidence
// by.
func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
