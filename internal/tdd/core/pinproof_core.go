package core

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// A pin proof is the record `aphrollo gate mutants prove` leaves when a
// mutation KILLED a test: this test fails when this file is broken. It is the
// evidence the fail-first stage accepts for a test that is already green on
// the old code (a pin or regression test), where no RED can exist.
//
// The proof names the file it broke by the git blob id of the content that
// was mutated, so it speaks for that content only: once the file changes, the
// proof no longer matches and does not count.

// PinProof is one killed mutant: Test failed when File, holding the content
// whose git blob id is Blob, was broken.
type PinProof struct {
	Test string `json:"test"`
	File string `json:"file"`
	Blob string `json:"blob"`
}

func pinProofPath(repoRoot string) string {
	dir := StateDir()
	if dir == "" || repoRoot == "" {
		return ""
	}
	return filepath.Join(dir, "pin-proofs", repoStateKey(repoRoot)+".jsonl")
}

// RecordPinProof appends p to repoRoot's proofs. Best effort, like gate.log:
// a proof that cannot be written costs the commit gate its evidence, never a
// decision.
func RecordPinProof(repoRoot string, p PinProof) {
	path := pinProofPath(repoRoot)
	if path == "" {
		return
	}
	line, err := json.Marshal(p)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// PinProofs is every proof recorded for repoRoot, oldest first; an unreadable
// store or a damaged line contributes nothing.
func PinProofs(repoRoot string) []PinProof {
	path := pinProofPath(repoRoot)
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []PinProof
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var p PinProof
		if json.Unmarshal(sc.Bytes(), &p) == nil && p.Test != "" && p.Blob != "" {
			out = append(out, p)
		}
	}
	return out
}
