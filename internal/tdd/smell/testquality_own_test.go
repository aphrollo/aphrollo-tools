package smell

import (
	"strings"
	"testing"
)

// These are P's own tests of testquality.go: premiseMarked, qualityNotesOn,
// reasonAbove and enclosingAssert are exercised today only through
// internal/tdd/postedit's higher-level QualityNotes tests, so a mutant on one
// of their lines survives P's own suite and the mutation gate can only file
// it SCOPE UNKNOWN — proving nothing about the line either way.

// TestPremiseMarked_CommentTwoLinesAboveMarks pins the near edge of the
// comment-scan window: a `// premise:`-shaped comment on the line directly
// above the sign check marks it.
func TestPremiseMarked_CommentTwoLinesAboveMarks(t *testing.T) {
	t.Parallel()
	lines := []string{"// premise: fixture is compressed", "assert!(f < 0.0);"}
	if !premiseMarked(lines, 1) {
		t.Fatalf("premiseMarked(lines, 1) = false, want true: comment sits one line above")
	}
}

// TestPremiseMarked_CommentThreeLinesAboveDoesNotMark pins the far edge: the
// comment scan reads exactly the two lines above i (lines[i-2:i]), so a
// marker three lines up is out of range and the sign check is unmarked. An
// off-by-one on the `i-2` slice start would fold this line in and wrongly
// mark it.
func TestPremiseMarked_CommentThreeLinesAboveDoesNotMark(t *testing.T) {
	t.Parallel()
	lines := []string{"// premise: fixture is compressed", "", "", "assert!(f < 0.0);"}
	if premiseMarked(lines, 3) {
		t.Fatalf("premiseMarked(lines, 3) = true, want false: comment sits three lines above, outside the two-line window")
	}
}

// TestPremiseMarked_MarkerInOwnStatementWithinBound pins the statement-scan
// arm: a multi-line assert whose message argument (a few lines below the
// sign check) names the premise still marks it, so a marker sitting inside
// the statement's own argument list is honoured even though it comes after
// the comparison, not before it.
func TestPremiseMarked_MarkerInOwnStatementWithinBound(t *testing.T) {
	t.Parallel()
	lines := []string{
		"assert!(f < 0.0,",
		"    \"premise: this member is genuinely compressed\");",
	}
	if !premiseMarked(lines, 0) {
		t.Fatalf("premiseMarked(lines, 0) = false, want true: the marker sits inside the statement's own open argument list")
	}
}

// TestPremiseMarked_StatementClosesBeforeMarkerStopsTheScan pins the balance
// guard: once a statement's parentheses close (balance drops back to zero or
// below), the scan stops — a premise-shaped comment that merely FOLLOWS the
// closed statement is not read as marking it. The line that closes the
// statement has two opens and two closes, so a sign-flipped balance
// computation (`+` instead of `-`) would leave the balance positive and let
// the scan run on into the trailing comment, wrongly marking it.
func TestPremiseMarked_StatementClosesBeforeMarkerStopsTheScan(t *testing.T) {
	t.Parallel()
	lines := []string{
		"assert!(f(x));",
		"// premise: too late, the statement already closed",
	}
	if premiseMarked(lines, 0) {
		t.Fatalf("premiseMarked(lines, 0) = true, want false: the statement closes on its own line, before the trailing comment")
	}
}

// TestPremiseMarked_StatementScanIncludesTheSixthLine pins the near edge of
// the statement-scan window (premiseMaxLines = 6): a marker on the sixth
// line of an argument list that never closes is still found. An off-by-one
// that shortened the window would drop this line and miss it.
func TestPremiseMarked_StatementScanIncludesTheSixthLine(t *testing.T) {
	t.Parallel()
	lines := []string{
		"assert!(x,",
		"    a,",
		"    b,",
		"    c,",
		"    d,",
		"    // premise: e",
	}
	if !premiseMarked(lines, 0) {
		t.Fatalf("premiseMarked(lines, 0) = false, want true: the marker sits on the sixth line of the window")
	}
}

// TestPremiseMarked_StatementScanExcludesTheSeventhLine pins the far edge of
// the same window: a marker on the seventh line of an argument list that
// never closes falls outside premiseMaxLines and is not found. An off-by-one
// that widened the window would pick this line up and wrongly mark it.
func TestPremiseMarked_StatementScanExcludesTheSeventhLine(t *testing.T) {
	t.Parallel()
	lines := []string{
		"assert!(x,",
		"    a,",
		"    b,",
		"    c,",
		"    d,",
		"    e,",
		"    // premise: too far",
	}
	if premiseMarked(lines, 0) {
		t.Fatalf("premiseMarked(lines, 0) = true, want false: the marker sits on the seventh line, outside the six-line window")
	}
}

// TestQualityNotesOn_OnlyRestrictsToTheGivenLines pins qualityNotesOn's `only`
// filter: a caller judging a single edited line does not get notes for other
// smelly lines the same content carries. A negated or inverted `only[i+1]`
// check would either report every line regardless of the filter, or drop the
// one line the filter actually asks for.
func TestQualityNotesOn_OnlyRestrictsToTheGivenLines(t *testing.T) {
	t.Parallel()
	const path = `D:\borld\crates\ui\src\a_tests.rs`
	src := "#[test]\nfn test_one() {}\n#[test]\nfn test_two() {}\n"

	all := qualityNotesOn(path, src, nil)
	if len(all) != 2 {
		t.Fatalf("qualityNotesOn(nil) = %v, want a note for each generic name", all)
	}

	line2 := qualityNotesOn(path, src, map[int]bool{2: true})
	if len(line2) != 1 || !strings.Contains(line2[0], ":2:") {
		t.Fatalf("qualityNotesOn({2: true}) = %v, want exactly the line-2 note", line2)
	}

	line4 := qualityNotesOn(path, src, map[int]bool{4: true})
	if len(line4) != 1 || !strings.Contains(line4[0], ":4:") {
		t.Fatalf("qualityNotesOn({4: true}) = %v, want exactly the line-4 note", line4)
	}

	none := qualityNotesOn(path, src, map[int]bool{1: true, 3: true})
	if len(none) != 0 {
		t.Fatalf("qualityNotesOn({1,3}) = %v, want silence: neither line 1 nor 3 carries a smell", none)
	}
}

// TestQualityNotes_ExtensionMustBeRsCaseInsensitively pins the file-kind
// guard: a non-.rs path is never judged, and the comparison is
// case-insensitive (a negated or dropped ToLower would treat `.RS` as a
// mismatch).
func TestQualityNotes_ExtensionMustBeRsCaseInsensitively(t *testing.T) {
	t.Parallel()
	src := "#[test]\nfn test_x() {}\n"
	if notes := QualityNotes(`D:\borld\crates\ui\src\a_tests.go`, src); notes != nil {
		t.Fatalf("notes = %v, want nil for a non-.rs path", notes)
	}
	if notes := QualityNotes(`D:\borld\crates\ui\src\A_TESTS.RS`, src); len(notes) == 0 {
		t.Fatalf("notes = %v, want the generic-name note for an uppercase .RS extension", notes)
	}
}

// TestQualityNotes_PlatformPinSubstringSilencesEitherExtensionCase pins the
// other half of the same guard line: the "_platform_pin" exemption applies
// alongside the .rs check, not instead of it — a .rs path carrying that
// substring is silent even though the extension matches.
func TestQualityNotes_PlatformPinSubstringSilencesEitherExtensionCase(t *testing.T) {
	t.Parallel()
	src := "#[test]\nfn test_x() {}\n"
	if notes := QualityNotes(`D:\borld\crates\forge_math\src\libm_platform_pin.rs`, src); notes != nil {
		t.Fatalf("notes = %v, want nil: a platform pin is exempt even with a matching .rs extension", notes)
	}
}

// TestQualityNotes_NoteNamesTheBaseFileAndOneBasedLine pins the note's
// format: `<base file>:<1-based line>: <message>`, using the path's base
// name (not the full path) and a line number the reader can open directly
// (0-based i+1, not the raw index).
func TestQualityNotes_NoteNamesTheBaseFileAndOneBasedLine(t *testing.T) {
	t.Parallel()
	src := "#[test]\nfn test_apply() {}\n"
	// A forward-slash path so filepath.Base strips the directory the same way
	// on every platform this package must build for (Go's filepath treats '/'
	// as a separator on both Unix and Windows).
	notes := QualityNotes("crates/ui/src/apply_tests.rs", src)
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want exactly one note", notes)
	}
	if !strings.HasPrefix(notes[0], "apply_tests.rs:2: ") {
		t.Fatalf("note = %q, want it to start with the base file name and 1-based line 2", notes[0])
	}
	if strings.Contains(notes[0], "crates") {
		t.Fatalf("note = %q, want the base name only, not the full path", notes[0])
	}
}

// TestReasonAbove_TwoLinesAboveIsTheEdgeOfTheWindow pins reasonAbove's own
// two-line window (the same shape premiseMarked's comment scan uses): a
// marker two lines above is read, three lines above is not.
func TestReasonAbove_TwoLinesAboveIsTheEdgeOfTheWindow(t *testing.T) {
	t.Parallel()
	within := []string{"// tolerance: four substeps", "", "assert!(approx_eq(a, b));"}
	if !reasonAbove(within, 2) {
		t.Fatalf("reasonAbove(within, 2) = false, want true: the marker sits two lines above")
	}
	outside := []string{"// tolerance: four substeps", "", "", "assert!(approx_eq(a, b));"}
	if reasonAbove(outside, 3) {
		t.Fatalf("reasonAbove(outside, 3) = true, want false: the marker sits three lines above, outside the window")
	}
}

// TestEnclosingAssert_FindsTheOpenAssertAboveAStillOpenLine pins the walk
// backward from a comparison line that sits inside a multi-line assert whose
// parentheses are still open at that point: it reports the assert's own
// line and true.
func TestEnclosingAssert_FindsTheOpenAssertAboveAStillOpenLine(t *testing.T) {
	t.Parallel()
	lines := []string{
		"assert!(",
		"    approx_eq(a, b)",
		");",
	}
	line, ok := enclosingAssert(lines, 1)
	if !ok || line != 0 {
		t.Fatalf("enclosingAssert(lines, 1) = (%d, %v), want (0, true): line 1 sits inside the assert opened on line 0", line, ok)
	}
}

// TestEnclosingAssert_FalseOnceTheAssertHasClosed pins the negative case: a
// line after an assert's parentheses have already balanced back to zero
// sits in no assert at all, even though an assert! line still appears
// earlier in the slice.
func TestEnclosingAssert_FalseOnceTheAssertHasClosed(t *testing.T) {
	t.Parallel()
	lines := []string{
		"assert!(a.is_finite());",
		"let b = a * 2.0;",
	}
	if _, ok := enclosingAssert(lines, 1); ok {
		t.Fatalf("enclosingAssert(lines, 1) = (_, true), want false: the assert on line 0 already closed on its own line")
	}
}

// TestQualityNotesOn_WeakBarBranchFiresInAPhysicsCrate pins qualityNotesOn's
// weak-bar case directly at the package that owns it: an unmarked sign check
// in a Tier-1/physics crate path is a weak bar.
func TestQualityNotesOn_WeakBarBranchFiresInAPhysicsCrate(t *testing.T) {
	t.Parallel()
	src := "#[test]\nfn stiffness_is_right() {\n    assert!(k > 0.0);\n}\n"
	notes := qualityNotesOn(`crates/forge_solver/src/truss_tests.rs`, src, nil)
	if len(notes) != 1 || !strings.Contains(notes[0], "weak bar") {
		t.Fatalf("notes = %v, want one weak-bar note", notes)
	}
}

// TestQualityNotesOn_ToleranceBranchFiresOutsidePhysicsCrates pins the
// tolerance case directly: an unexplained tolerance comparison is named
// whether or not the path is a physics crate.
func TestQualityNotesOn_ToleranceBranchFiresOutsidePhysicsCrates(t *testing.T) {
	t.Parallel()
	src := "#[test]\nfn t() {\n    assert!(approx_eq(a, b));\n}\n"
	notes := qualityNotesOn(`crates/pose/src/a_tests.rs`, src, nil)
	if len(notes) != 1 || !strings.Contains(notes[0], "tolerance") {
		t.Fatalf("notes = %v, want one tolerance note", notes)
	}
}

// TestToleranceExplained_MarkerDirectlyAboveExplainsIt pins
// toleranceExplained's own-line arm: a `// tolerance:` two lines above the
// comparison line itself (not an enclosing assert) is enough.
func TestToleranceExplained_MarkerDirectlyAboveExplainsIt(t *testing.T) {
	t.Parallel()
	lines := []string{"// tolerance: four substeps", "assert!(approx_eq(a, b));"}
	if !toleranceExplained(lines, 1) {
		t.Fatalf("toleranceExplained(lines, 1) = false, want true: the marker sits directly above the comparison")
	}
}

// TestToleranceExplained_MarkerAboveTheEnclosingAssertExplainsAnInnerLine
// pins the fallback arm: when the comparison's own two lines above carry no
// marker, a marker above the multi-line assert that encloses it still
// counts.
func TestToleranceExplained_MarkerAboveTheEnclosingAssertExplainsAnInnerLine(t *testing.T) {
	t.Parallel()
	lines := []string{
		"// tolerance: four substeps",
		"assert!(",
		"    x,",
		"    approx_eq(a, b)",
		");",
	}
	// The comparison's own two lines above (index 1 and 2) carry no marker,
	// so this only passes through the enclosingAssert fallback, not the
	// direct reasonAbove(lines, i) arm above it.
	if reasonAbove(lines, 3) {
		t.Fatalf("reasonAbove(lines, 3) = true, want false: no marker on the comparison's own two lines above")
	}
	if !toleranceExplained(lines, 3) {
		t.Fatalf("toleranceExplained(lines, 3) = false, want true: the marker sits above the assert enclosing line 3")
	}
}

// TestToleranceExplained_NoMarkerAnywhereIsUnexplained pins the negative
// case: no marker on the comparison's own lines, and no enclosing assert to
// fall back to, leaves the tolerance unexplained.
func TestToleranceExplained_NoMarkerAnywhereIsUnexplained(t *testing.T) {
	t.Parallel()
	lines := []string{"let x = 1;", "assert!(approx_eq(a, b));"}
	if toleranceExplained(lines, 1) {
		t.Fatalf("toleranceExplained(lines, 1) = true, want false: no marker anywhere in reach")
	}
}
