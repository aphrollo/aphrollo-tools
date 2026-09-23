package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPreEdit_ToleranceNoteNamesTheFileLineNotTheEditLine pins issue #760
// part one: an Edit's advisory named the line inside new_string ("a_tests.rs:1"
// for a comparison that sits at line 7 of the file), a line the reader cannot
// open. The note must name the line in the file as it will be after the edit.
func TestPreEdit_ToleranceNoteNamesTheFileLineNotTheEditLine(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "crates", "pose", "src", "a_tests.rs")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "use super::*;\n\n#[test]\nfn reach_is_exact() {\n    let a = reach();\n    let b = 2.0;\n    PLACEHOLDER;\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"tool_name": "Edit",
		"tool_input": map[string]any{
			"file_path":  path,
			"old_string": "    PLACEHOLDER;",
			"new_string": "    assert!(approx_eq(a, b));",
		},
	})

	got, err := DecidePreEdit(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Reason, "a_tests.rs:7: a tolerance names its consumer") {
		t.Fatalf("reason = %q, want the tolerance named at the file's line 7", got.Reason)
	}
}

// TestQualityNotes_MarkerAboveTheEnclosingAssertCoversItsInnerComparison pins
// issue #760 part two: a multi-line `assert!(` whose comparison sits several
// argument lines down was only satisfied by a marker inside the macro's
// argument list. A marker above the enclosing assert covers every comparison
// inside it; the same comparison with the marker gone is still named.
func TestQualityNotes_MarkerAboveTheEnclosingAssertCoversItsInnerComparison(t *testing.T) {
	t.Parallel()
	const path = `D:\borld\crates\forge_solver\src\ground_contact\patch\tests\substep_probe.rs`
	body := "    assert!(\n        samples.iter().all(|s| {\n            let err = (s.got - s.want).abs();\n            err < 1e-6\n        }),\n        \"a substep drifted: {samples:?}\"\n    );\n"
	explained := "#[test]\nfn substeps_agree() {\n    // tolerance: four substeps accumulate f64 rounding\n" + body + "}\n"
	if notes := TestQualityNotes(path, explained); len(notes) != 0 {
		t.Fatalf("notes = %v, want the marker above the assert to cover its inner comparison", notes)
	}
	bare := "#[test]\nfn substeps_agree() {\n" + body + "}\n"
	notes := TestQualityNotes(path, bare)
	if len(notes) != 1 || !strings.Contains(notes[0], "substep_probe.rs:6:") {
		t.Fatalf("notes = %v, want the unexplained comparison named at line 6", notes)
	}
}

// TestQualityNotes_MarkerAboveAClosedAssertDoesNotCoverTheNextOne pins the
// edge of that rule: a marker covers the statement it sits above, not a
// later assert that merely follows one already closed.
func TestQualityNotes_MarkerAboveAClosedAssertDoesNotCoverTheNextOne(t *testing.T) {
	t.Parallel()
	const path = `D:\borld\crates\pose\src\a_tests.rs`
	src := "#[test]\nfn t() {\n    // tolerance: the first one is explained\n    assert!(\n        a.is_finite()\n    );\n    assert!(\n        approx_eq(a, b)\n    );\n}\n"
	notes := TestQualityNotes(path, src)
	if len(notes) != 1 || !strings.Contains(notes[0], "a_tests.rs:8:") {
		t.Fatalf("notes = %v, want the second assert's tolerance named at line 8", notes)
	}
}

// TestQualityNotes_MarkerAboveASingleLineAssertCoversNothingBelowIt: a
// one-line assert closes on its own line, so a marker above it says nothing
// about a comparison further down that sits in no assert of its own.
func TestQualityNotes_MarkerAboveASingleLineAssertCoversNothingBelowIt(t *testing.T) {
	t.Parallel()
	const path = `D:\borld\crates\pose\src\a_tests.rs`
	src := "#[test]\nfn t() {\n    // tolerance: the first one is explained\n    assert!(a.is_finite());\n    let b = a * 2.0;\n    let c = b + 1.0;\n    assert!((c - b).abs() < 1e-6);\n}\n"
	notes := TestQualityNotes(path, src)
	if len(notes) != 1 || !strings.Contains(notes[0], "a_tests.rs:7:") {
		t.Fatalf("notes = %v, want the unexplained comparison named at line 7", notes)
	}
}

// TestPreEdit_ToleranceNoteSkipsLinesTheEditDidNotAdd: judging the whole file
// must not re-judge what was already there — an edit elsewhere in a file that
// carries an old unexplained tolerance is not this edit's to answer for.
func TestPreEdit_ToleranceNoteSkipsLinesTheEditDidNotAdd(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "crates", "pose", "src", "a_tests.rs")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "#[test]\nfn reach_is_exact() {\n    assert!(approx_eq(reach(), 2.0));\n}\n\n// PLACEHOLDER\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"tool_name": "Edit",
		"tool_input": map[string]any{
			"file_path":  path,
			"old_string": "// PLACEHOLDER",
			"new_string": "// the reach tests live above",
		},
	})

	got, err := DecidePreEdit(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Reason, "tolerance") {
		t.Fatalf("reason = %q, want no note for a tolerance the edit did not add", got.Reason)
	}
}
