package tdd

import (
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
	for _, tree := range []string{filepath.Join("..", "..", "internal"), filepath.Join("..", "..", "cmd")} {
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
