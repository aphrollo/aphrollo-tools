package tdd

import (
	"encoding/json"
	"strings"
	"testing"
)

// preEditPayload builds a PreToolUse payload for a Write of content to path.
func preEditPayload(path, content string) []byte {
	in := map[string]any{
		"tool_name":  "Write",
		"tool_input": map[string]any{"file_path": path, "content": content},
	}
	b, _ := json.Marshal(in)
	return b
}

// TestQualityNotes_WeakPhysicsBar pins smell (a): in a Tier-1 or physics
// crate, `assert!(x > 0.0)` passes for a value 100x wrong. The bar for a
// number whose closed form is known is the closed form, not its sign — the
// borld case was a perpendicular-stiffness test asserting `> 1.0`.
func TestQualityNotes_WeakPhysicsBar(t *testing.T) {
	t.Parallel()
	src := "#[test]\nfn stiffness_is_right() {\n    assert!(k > 0.0);\n}\n"
	notes := TestQualityNotes(`D:\borld\crates\forge_solver\src\truss_tests.rs`, src)
	if len(notes) == 0 || !strings.Contains(strings.Join(notes, "\n"), "closed-form") {
		t.Fatalf("notes = %v, want a weak-bar note naming the closed-form value", notes)
	}
	if !strings.Contains(notes[0], ":3:") {
		t.Fatalf("note = %q, want file:line so the reader can go straight there", notes[0])
	}

	// The same shape in a crate whose numbers are not physical is not this
	// smell — a sign check on a counter is a fine assertion.
	if notes := TestQualityNotes(`D:\borld\crates\ui\src\widget_tests.rs`, src); len(notes) != 0 {
		t.Fatalf("notes = %v, want silence outside the physics/Tier-1 crates", notes)
	}
}

// TestQualityNotes_GenericTestName pins smell (b): a name like `test_thing` or
// `_works` describes nothing, so it cannot say which production change makes
// it red — which is the question every test must answer.
func TestQualityNotes_GenericTestName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"fn test_apply()", "fn apply_works()", "fn apply_basic()", "fn smoke_apply()"} {
		src := "#[test]\n" + name + " {}\n"
		notes := TestQualityNotes(`D:\borld\crates\ui\src\a_tests.rs`, src)
		if len(notes) == 0 || !strings.Contains(strings.Join(notes, "\n"), "red") {
			t.Fatalf("%s: notes = %v, want a naming note", name, notes)
		}
	}
	src := "#[test]\nfn rejects_a_zero_length_edge() {}\n"
	if notes := TestQualityNotes(`D:\borld\crates\ui\src\a_tests.rs`, src); len(notes) != 0 {
		t.Fatalf("notes = %v, want silence for a name that states the behaviour", notes)
	}
}

// TestQualityNotes_UnexplainedTolerance pins smell (c): a tolerance is a hole
// the size of the tolerance unless something says who needs it. The note is
// silenced by a `// tolerance:` or `// why:` line above the assertion.
func TestQualityNotes_UnexplainedTolerance(t *testing.T) {
	t.Parallel()
	bare := "#[test]\nfn t() {\n    assert!(approx_eq(a, b));\n}\n"
	if notes := TestQualityNotes(`D:\borld\crates\pose\src\a_tests.rs`, bare); len(notes) == 0 {
		t.Fatal("an unexplained tolerance must be named")
	}
	explained := "#[test]\nfn t() {\n    // tolerance: the solver integrates over 4 substeps\n    assert!(approx_eq(a, b));\n}\n"
	if notes := TestQualityNotes(`D:\borld\crates\pose\src\a_tests.rs`, explained); len(notes) != 0 {
		t.Fatalf("notes = %v, want silence when the tolerance names its consumer", notes)
	}
}

// TestQualityNotes_SkipPlatformPins pins the one exemption: a platform pin
// records what a machine DOES, so a sign bar or a tolerance there is the
// point of the file.
func TestQualityNotes_SkipPlatformPins(t *testing.T) {
	t.Parallel()
	src := "#[test]\nfn test_x() {\n    assert!(k > 0.0);\n}\n"
	if notes := TestQualityNotes(`D:\borld\crates\forge_math\src\libm_platform_pin.rs`, src); len(notes) != 0 {
		t.Fatalf("notes = %v, want a platform pin left alone", notes)
	}
}

// TestPreEdit_QualitySmellsWarnNeverDeny pins the reaction: these are
// judgement calls, not oracle defects. A false deny wedges the session, so
// they ride along as advice and the edit goes through.
func TestPreEdit_QualitySmellsWarnNeverDeny(t *testing.T) {
	t.Parallel()
	src := "#[test]\nfn test_apply() {\n    assert!(k > 0.0);\n}\n"
	got, err := DecidePreEdit(preEditPayload(`D:\borld\crates\movement\src\apply_tests.rs`, src))
	if err != nil {
		t.Fatal(err)
	}
	if got.Action == Block {
		t.Fatalf("decision = %v, want a warning: these smells are judgement calls", got)
	}
	if got.Action != Warn || !strings.Contains(got.Reason, "apply_tests.rs") {
		t.Fatalf("decision = %+v, want a warning naming the file", got)
	}
}
