package tdd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The mutation receipt, the outcome store and everything that carried
// outcomes between a background run and a later judgement are gone: the
// measurement and the judgement are one event now, so there is nothing left
// for a cache to carry and no document for a merge to read.
//
// This is a test rather than a one-off sweep because the deletion is the
// point. A helper that comes back — a struct field, a stray writer, a "just
// for the CI path" store — quietly re-creates the thing the run was supposed
// to replace, and nothing else in the suite would notice.
func TestDeletedSurface_NoSourceMentionsReceiptOrStore(t *testing.T) {
	t.Parallel()
	// This file names the identifiers in order to look for them.
	const self = "mutants_deleted_surface_test.go"
	gone := []string{"MutationReceipt", "MutantStore", "carryReceipt", "recountReceipt", "InvocationVersion"}

	var offenders []string
	// tree-read-ok: the claim IS about this repo's own source tree, so the
	// tree is the input; there is nothing else to read it from.
	root := repoRootForTest(t)
	for _, tree := range []string{filepath.Join(root, "internal"), filepath.Join(root, "cmd")} {
		err := filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || d.Name() == self {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for line := range strings.Lines(string(data)) {
				// A test_removed tombstone has to name the test it buries,
				// verbatim, or the ratchet law does not accept it. Naming a
				// deleted test is the opposite of keeping the thing alive.
				if strings.Contains(line, "ratchet: test_removed") {
					continue
				}
				for _, needle := range gone {
					if strings.Contains(line, needle) {
						offenders = append(offenders, path+": "+needle)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", tree, err)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("the receipt and the outcome store are deleted, but %d mention(s) remain:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// The identifiers above are the mechanism; the WORD is the idea. A comment
// that still tells a reader the merge waits for a receipt teaches the same
// wrong thing the code used to do, and prose is what the next session reads
// first — the block this gate writes into every repo's CLAUDE.md is generated
// from this package, and four of these comments outlived the code they
// described.
//
// Scope and its two admitted limits, both deliberate:
//
//   - internal/tdd's own production text, plus cmd/. The workspace verbs
//     print a stateful receipt of their own — a live, README-documented
//     artifact of a different feature — so internal/workspace and the
//     workspace surface in internal/cli are not this rule's business, and
//     renaming them would be a vocabulary change to code this measurement
//     never touched.
//   - production text only. A test may legitimately name the retired
//     vocabulary it feeds a parser: stats' own suite replays historical
//     gate.log lines carrying tokens nothing can write again, and asserting
//     they are still counted is the point of those tests.
//
// A line that genuinely has to carry the word says why with
// `receipt-word-ok: <reason>`, on the line itself or the one above it — the
// same escape shape the edit-time smell policies use.
func TestDeletedSurface_NoProductionCommentPromisesAReceipt(t *testing.T) {
	t.Parallel()
	const self = "mutants_deleted_surface_test.go"
	const escape = "receipt-word-ok:"

	var offenders []string
	// tree-read-ok: the claim IS about this repo's own source tree, so the
	// tree is the input; there is nothing else to read it from.
	root := repoRootForTest(t)
	for _, tree := range []string{filepath.Join(root, "internal", "tdd"), filepath.Join(root, "cmd")} {
		err := filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir(), d.Name() == self:
				return nil
			case !strings.HasSuffix(path, ".go"), strings.HasSuffix(path, "_test.go"):
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			lines := strings.Split(string(data), "\n")
			for i, line := range lines {
				if !mentionsReceipt(line) || strings.Contains(line, escape) {
					continue
				}
				if i > 0 && strings.Contains(lines[i-1], escape) {
					continue
				}
				offenders = append(offenders, fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimSpace(line)))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", tree, err)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("nothing produces a mutation receipt, but %d production line(s) still say one exists:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// mentionsReceipt matches the whole word, in any case and in any form the
// prose uses it — "Receipts", "mutation-receipt", "receipt's" all count. A
// substring match would not: `receipted` inside a longer word is the same
// claim, and nothing in this tree needs the word for anything else.
func mentionsReceipt(line string) bool {
	return strings.Contains(strings.ToLower(line), "receipt")
}
