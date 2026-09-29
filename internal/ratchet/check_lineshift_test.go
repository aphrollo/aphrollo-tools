package ratchet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const bareNoqaLaw = `
name        = "suppression_reason_py"
description = "a noqa carries a reason"
severity    = "deny"
baseline    = ".ratchet/baselines/suppression_reason_py.txt"
mask_strings = true

[scope]
include = ["**/*.py"]

[matcher]
kind    = "regex-absent"
pattern = "#\\s*noqa(?::\\s*[A-Z0-9, ]+)?\\s*$"
key     = "file:line-content-hash"
`

// noqaBody is three identical bare-noqa lines, the shape a baseline row per
// occurrence has to count with multiplicity.
const noqaBody = `def a():
    try:
        pass
    except Exception as e:  # noqa: BLE001
        pass


def b():
    try:
        pass
    except Exception as e:  # noqa: BLE001
        pass


def c():
    try:
        pass
    except Exception as e:  # noqa: BLE001
        pass
`

// TestCheck_BaselineSurvivesLinesMovingAboveIt reproduces a baselined file
// whose bare-noqa lines were counted as new after unrelated lines above them
// were deleted or added: the apostrophes in the docstring and the comment
// above must never change what the lines below them count as, whether the
// paragraph goes, a line is added, or the apostrophe pairs flip.
func TestCheck_BaselineSurvivesLinesMovingAboveIt(t *testing.T) {
	head := "\"\"\"Service.\n\nIt's a paragraph the commit removes, and don't\nmiss it.\n\"\"\"\n\n\n# don't blank what follows\n"
	variants := map[string]string{
		"paragraph deleted": "\"\"\"Service.\"\"\"\n\n\n# don't blank what follows\n",
		"comment deleted":   "\"\"\"Service.\n\nIt's a paragraph\n\"\"\"\n\n\n",
		"lines added":       head + "x = 1\ny = \"it's\"\n# it's another\n",
		"all above deleted": "",
	}
	for name, above := range variants {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeLaw(t, root, "suppression_reason_py", bareNoqaLaw)
			file := filepath.Join(root, "backend", "svc.py")
			write(t, file, head+noqaBody)
			if _, err := Adopt(AdoptOptions{Root: root, Law: "suppression_reason_py"}); err != nil {
				t.Fatalf("Adopt: %v", err)
			}
			base := filepath.Join(root, ".ratchet", "baselines", "suppression_reason_py.txt")
			adopted, err := os.ReadFile(base)
			if err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(string(adopted), "# noqa: BLE001"); n != 3 {
				t.Fatalf("adopted %d rows, want 3 — every identical line counts:\n%s", n, adopted)
			}

			write(t, file, above+noqaBody)
			got, err := Check(Options{Root: root})
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if len(got.Findings) != 0 {
				t.Fatalf("editing lines above the noqa lines produced findings: %+v", got.Findings)
			}
			data, err := os.ReadFile(base)
			if err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(string(data), "# noqa: BLE001"); n != 3 {
				t.Errorf("baseline holds %d rows after the check, want 3:\n%s", n, data)
			}
		})
	}
}
