package smell

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These are smell's own tests of smellscope.go: editImages, addedLines,
// escapedLines, evaluateAdded, restrict, intersectLines and withoutLines are
// exercised today only through internal/tdd/postedit's edit-hook tests, so a
// mutant on one of their lines survives smell's own suite and the mutation
// gate can only file it SCOPE UNKNOWN.

// toolInput decodes a PreToolUse payload the way the hook does.
func toolInput(t *testing.T, payload string) preToolUseInput {
	t.Helper()
	var in preToolUseInput
	if err := json.Unmarshal([]byte(payload), &in); err != nil {
		t.Fatalf("payload %s: %v", payload, err)
	}
	return in
}

// onDisk writes content to a fresh file and returns its path.
func onDisk(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.go")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestEditImages_NoPathTreatsTheWholeEditAsNew pins the safe fallback: with no
// file to read there is no before-image, and everything the edit introduces
// counts as new.
func TestEditImages_NoPathTreatsTheWholeEditAsNew(t *testing.T) {
	t.Parallel()
	in := toolInput(t, `{"tool_name":"Edit","tool_input":{"old_string":"a","new_string":"b"}}`)
	pre, post := editImages(in, "")
	if pre != "" || post != "b" {
		t.Fatalf("editImages = (%q, %q), want (\"\", \"b\")", pre, post)
	}
}

// TestEditImages_UnreadableFileTreatsTheWholeEditAsNew pins the same fallback
// for a path that is not on disk: an edit whose file cannot be read has no
// before-image to diff against.
func TestEditImages_UnreadableFileTreatsTheWholeEditAsNew(t *testing.T) {
	t.Parallel()
	in := toolInput(t, `{"tool_name":"Write","tool_input":{"content":"fresh"}}`)
	pre, post := editImages(in, filepath.Join(t.TempDir(), "absent.go"))
	if pre != "" || post != "fresh" {
		t.Fatalf("editImages = (%q, %q), want (\"\", \"fresh\")", pre, post)
	}
}

// TestEditImages_WriteIsTheDiskAgainstTheContent pins that a Write's
// after-image is exactly the content it carries, not an edit applied to the
// disk.
func TestEditImages_WriteIsTheDiskAgainstTheContent(t *testing.T) {
	t.Parallel()
	path := onDisk(t, "old body\n")
	in := toolInput(t, `{"tool_name":"Write","tool_input":{"content":"new body\n"}}`)
	pre, post := editImages(in, path)
	if pre != "old body\n" || post != "new body\n" {
		t.Fatalf("editImages = (%q, %q), want the disk and the written content", pre, post)
	}
}

// TestEditImages_EditReplacesTheFirstOccurrenceOnly pins the Edit tool's own
// substitution: without replace_all only the first match changes.
func TestEditImages_EditReplacesTheFirstOccurrenceOnly(t *testing.T) {
	t.Parallel()
	path := onDisk(t, "x\nx\n")
	in := toolInput(t, `{"tool_name":"Edit","tool_input":{"old_string":"x","new_string":"y"}}`)
	pre, post := editImages(in, path)
	if pre != "x\nx\n" || post != "y\nx\n" {
		t.Fatalf("editImages = (%q, %q), want (\"x\\nx\\n\", \"y\\nx\\n\")", pre, post)
	}
}

// TestEditImages_EditReplaceAllReplacesEveryOccurrence is the other half:
// replace_all reaches every match, so the after-image carries no old text.
func TestEditImages_EditReplaceAllReplacesEveryOccurrence(t *testing.T) {
	t.Parallel()
	path := onDisk(t, "x\nx\n")
	in := toolInput(t, `{"tool_name":"Edit","tool_input":{"old_string":"x","new_string":"y","replace_all":true}}`)
	_, post := editImages(in, path)
	if post != "y\ny\n" {
		t.Fatalf("post = %q, want every x replaced", post)
	}
}

// TestEditImages_MultiEditAppliesEachEditToTheResultOfTheLast pins the order:
// a later edit whose old text only exists after an earlier edit still lands,
// and the before-image stays the disk.
func TestEditImages_MultiEditAppliesEachEditToTheResultOfTheLast(t *testing.T) {
	t.Parallel()
	path := onDisk(t, "one\n")
	in := toolInput(t, `{"tool_name":"MultiEdit","tool_input":{"edits":[`+
		`{"old_string":"one","new_string":"two"},`+
		`{"old_string":"two","new_string":"three"}]}}`)
	pre, post := editImages(in, path)
	if pre != "one\n" || post != "three\n" {
		t.Fatalf("editImages = (%q, %q), want (\"one\\n\", \"three\\n\")", pre, post)
	}
}

// TestEditImages_MultiEditHonoursReplaceAllPerEdit pins that each edit in a
// MultiEdit carries its own replace_all.
func TestEditImages_MultiEditHonoursReplaceAllPerEdit(t *testing.T) {
	t.Parallel()
	path := onDisk(t, "a a\nb b\n")
	in := toolInput(t, `{"tool_name":"MultiEdit","tool_input":{"edits":[`+
		`{"old_string":"a","new_string":"c","replace_all":true},`+
		`{"old_string":"b","new_string":"d"}]}}`)
	_, post := editImages(in, path)
	if post != "c c\nd b\n" {
		t.Fatalf("post = %q, want a replaced everywhere and b once", post)
	}
}

// TestEditImages_AnEditWhoseOldTextIsNotOnDiskFallsBack pins the guard against
// a no-op reconstruction: when the old text is not in the file the after-image
// equals the before-image, which would read as "nothing added" and let the
// edit's new content through unjudged. It falls back to "everything the edit
// introduces is new".
func TestEditImages_AnEditWhoseOldTextIsNotOnDiskFallsBack(t *testing.T) {
	t.Parallel()
	path := onDisk(t, "present\n")
	in := toolInput(t, `{"tool_name":"Edit","tool_input":{"old_string":"absent","new_string":"time.Sleep(1)"}}`)
	pre, post := editImages(in, path)
	if pre != "" || post != "time.Sleep(1)" {
		t.Fatalf("editImages = (%q, %q), want (\"\", the edit's own new text)", pre, post)
	}
}

// TestEditImages_AToolItDoesNotModelFallsBack pins the fallback for a tool
// shape editImages cannot reconstruct.
func TestEditImages_AToolItDoesNotModelFallsBack(t *testing.T) {
	t.Parallel()
	path := onDisk(t, "present\n")
	in := toolInput(t, `{"tool_name":"NotebookEdit","tool_input":{"new_string":"cell"}}`)
	pre, post := editImages(in, path)
	if pre != "" || post != "cell" {
		t.Fatalf("editImages = (%q, %q), want (\"\", \"cell\")", pre, post)
	}
}

// TestAddedLines_CountsByMultiplicityNotPosition pins the whole point of the
// function: a line present twice after and once before is ONE new line, the
// later one, and a pure move adds nothing.
func TestAddedLines_CountsByMultiplicityNotPosition(t *testing.T) {
	t.Parallel()
	got := addedLines("a\nb\n", "b\na\na\n")
	want := map[int]bool{3: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("addedLines = %v, want %v (only the second a is new)", got, want)
	}
}

// TestAddedLines_ANewLineIsNumberedFromOne pins the 1-based numbering the
// view's line filters share: the first line of post is line 1.
func TestAddedLines_ANewLineIsNumberedFromOne(t *testing.T) {
	t.Parallel()
	got := addedLines("", "fresh\n")
	if !got[1] || got[0] {
		t.Fatalf("addedLines = %v, want line 1 and not line 0", got)
	}
}

// TestAddedLines_IgnoresSurroundingWhitespace pins that a re-indented line is
// the same line: comparison trims, so an edit elsewhere never re-judges what
// only moved in the margin.
func TestAddedLines_IgnoresSurroundingWhitespace(t *testing.T) {
	t.Parallel()
	got := addedLines("\tif x {\n", "    if x {   \n")
	if len(got) != 0 {
		t.Fatalf("addedLines = %v, want none: only the indentation changed", got)
	}
}

// TestAddedLines_APurelyMovedBlockAddsNothing pins the move guarantee end to
// end: the same lines in a different order are not additions.
func TestAddedLines_APurelyMovedBlockAddsNothing(t *testing.T) {
	t.Parallel()
	if got := addedLines("a\nb\nc\n", "c\na\nb\n"); len(got) != 0 {
		t.Fatalf("addedLines = %v, want none for a pure reorder", got)
	}
}

// TestEscapedLines_AMarkerAdmitsItsOwnLineAndTheOneBelow pins the two lines a
// marker covers, 1-based: the line carrying it (so a trailing comment
// works) and the next (so a reason too long for the code's line can sit
// above it), and nothing further.
func TestEscapedLines_AMarkerAdmitsItsOwnLineAndTheOneBelow(t *testing.T) {
	t.Parallel()
	directives := "code\n// real-time: hardware settle\ntime.Sleep(1)\nother\n"
	got := escapedLines(directives, "real-time:")
	want := map[int]bool{2: true, 3: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("escapedLines = %v, want %v", got, want)
	}
}

// TestEscapedLines_EveryMarkerAdmitsItsOwnPair pins that two markers admit two
// separate pairs, not one merged span.
func TestEscapedLines_EveryMarkerAdmitsItsOwnPair(t *testing.T) {
	t.Parallel()
	directives := "skip-ok: a\nx\ny\nskip-ok: b\nz\n"
	got := escapedLines(directives, "skip-ok:")
	want := map[int]bool{1: true, 2: true, 4: true, 5: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("escapedLines = %v, want %v", got, want)
	}
}

// TestEscapedLines_NoMarkerAdmitsNothing pins the empty case.
func TestEscapedLines_NoMarkerAdmitsNothing(t *testing.T) {
	t.Parallel()
	if got := escapedLines("a\nb\n", "skip-ok:"); len(got) != 0 {
		t.Fatalf("escapedLines = %v, want none", got)
	}
}

// scopeSleep is a stand-in blocking policy with an escape: it hits when the
// code view mentions sleep(.
var scopeSleep = policy{
	name:     "sleep",
	category: smellCat,
	reason:   "sleep reason",
	escape:   "real-time:",
	hit:      func(v view) bool { return strings.Contains(v.Code, "sleep(") },
}

// scopeNolint is a stand-in suppression policy: it warns at edit time and
// blocks at commit, and admits no escape.
var scopeNolint = policy{
	name:     "nolint",
	category: suppressionCat,
	reason:   "nolint reason",
	hit:      func(v view) bool { return strings.Contains(v.directives, "nolint") },
}

// linesOf is the set of 1-based line numbers given.
func linesOf(ns ...int) map[int]bool {
	out := map[int]bool{}
	for _, n := range ns {
		out[n] = true
	}
	return out
}

// TestEvaluateAdded_JudgesOnlyTheGivenLines pins the scope: a hit on a line
// the edit did not add is not this edit's to answer for.
func TestEvaluateAdded_JudgesOnlyTheGivenLines(t *testing.T) {
	t.Parallel()
	post := "sleep(1)\nclean()\n"
	got := evaluateAdded(post, linesOf(2), defaultLang, []policy{scopeSleep}, editPhase)
	if got.Action != Allow {
		t.Fatalf("Decision = %+v, want Allow: the sleep is on line 1, the edit added line 2", got)
	}
}

// TestEvaluateAdded_ABlockingHitOnAnAddedLineBlocksAndNamesItself pins the
// verdict's payload: action, the policy's own reason and name.
func TestEvaluateAdded_ABlockingHitOnAnAddedLineBlocksAndNamesItself(t *testing.T) {
	t.Parallel()
	got := evaluateAdded("clean()\nsleep(1)\n", linesOf(2), defaultLang, []policy{scopeSleep}, editPhase)
	if got.Action != Block || got.Reason != "sleep reason" || got.Policy != "sleep" {
		t.Fatalf("Decision = %+v, want Block naming the sleep policy and its reason", got)
	}
	if len(got.Escapes) != 0 {
		t.Fatalf("Escapes = %v, want none: nothing was escaped", got.Escapes)
	}
}

// TestEvaluateAdded_NoAddedLinesAllows pins the empty scope: nothing added,
// nothing judged, even though the file holds a hit.
func TestEvaluateAdded_NoAddedLinesAllows(t *testing.T) {
	t.Parallel()
	got := evaluateAdded("sleep(1)\n", linesOf(), defaultLang, []policy{scopeSleep}, editPhase)
	if got.Action != Allow || got.Policy != "" {
		t.Fatalf("Decision = %+v, want a bare Allow", got)
	}
}

// TestEvaluateAdded_AnEscapeOnTheSameLineAdmitsTheHitAndIsRecorded pins the
// escape: the marker admits the hit, and the waiver is recorded by policy
// name so it shows up in the stats.
func TestEvaluateAdded_AnEscapeOnTheSameLineAdmitsTheHitAndIsRecorded(t *testing.T) {
	t.Parallel()
	post := "sleep(1) // real-time: the device needs it\n"
	got := evaluateAdded(post, linesOf(1), defaultLang, []policy{scopeSleep}, editPhase)
	if got.Action != Allow {
		t.Fatalf("Decision = %+v, want Allow: the line carries the escape", got)
	}
	if !reflect.DeepEqual(got.Escapes, []string{"smell-escape:sleep"}) {
		t.Fatalf("Escapes = %v, want [smell-escape:sleep]", got.Escapes)
	}
}

// TestEvaluateAdded_AnEscapeOnTheLineAboveAdmitsTheHit pins the two-line reach
// of the marker through the evaluator: a reason above the code admits it.
func TestEvaluateAdded_AnEscapeOnTheLineAboveAdmitsTheHit(t *testing.T) {
	t.Parallel()
	post := "// real-time: the device needs it\nsleep(1)\n"
	got := evaluateAdded(post, linesOf(1, 2), defaultLang, []policy{scopeSleep}, editPhase)
	if got.Action != Allow || len(got.Escapes) != 1 {
		t.Fatalf("Decision = %+v, want Allow with one recorded escape", got)
	}
}

// TestEvaluateAdded_AnEscapeDoesNotHideASecondUnescapedHit pins that the
// waiver covers its own lines only: a second hit further down still blocks,
// and the first hit's waiver is still recorded.
func TestEvaluateAdded_AnEscapeDoesNotHideASecondUnescapedHit(t *testing.T) {
	t.Parallel()
	post := "sleep(1) // real-time: needed\nfiller()\nfiller()\nsleep(2)\n"
	got := evaluateAdded(post, linesOf(1, 4), defaultLang, []policy{scopeSleep}, editPhase)
	if got.Action != Block {
		t.Fatalf("Decision = %+v, want Block for the unescaped sleep on line 4", got)
	}
	if !reflect.DeepEqual(got.Escapes, []string{"smell-escape:sleep"}) {
		t.Fatalf("Escapes = %v, want the first sleep's waiver recorded", got.Escapes)
	}
}

// TestEvaluateAdded_AMarkerOnAnUnaddedLineWaivesNothingNew pins that a marker
// admits only lines this edit added: a stale marker over a line the edit did
// not touch neither hides a new hit nor is recorded as a waiver.
func TestEvaluateAdded_AMarkerOnAnUnaddedLineWaivesNothingNew(t *testing.T) {
	t.Parallel()
	post := "// real-time: old reason\nold()\nsleep(1)\n"
	got := evaluateAdded(post, linesOf(3), defaultLang, []policy{scopeSleep}, editPhase)
	if got.Action != Block {
		t.Fatalf("Decision = %+v, want Block: the marker's reach ends at line 2", got)
	}
	if len(got.Escapes) != 0 {
		t.Fatalf("Escapes = %v, want none: the marker admitted no added hit", got.Escapes)
	}
}

// TestEvaluateAdded_AnEscapeOverACleanLineIsNotARecordedWaiver pins that a
// waiver is recorded only when it actually admitted a hit.
func TestEvaluateAdded_AnEscapeOverACleanLineIsNotARecordedWaiver(t *testing.T) {
	t.Parallel()
	post := "clean() // real-time: not needed here\n"
	got := evaluateAdded(post, linesOf(1), defaultLang, []policy{scopeSleep}, editPhase)
	if got.Action != Allow || len(got.Escapes) != 0 {
		t.Fatalf("Decision = %+v, want Allow with no escape recorded", got)
	}
}

// TestEvaluateAdded_APolicyWithoutAnEscapeAdmitsNone pins that a policy that
// declares no marker cannot be waived, even by a comment that looks like one.
func TestEvaluateAdded_APolicyWithoutAnEscapeAdmitsNone(t *testing.T) {
	t.Parallel()
	noEscape := scopeSleep
	noEscape.escape = ""
	post := "sleep(1) // real-time: pleading\n"
	got := evaluateAdded(post, linesOf(1), defaultLang, []policy{noEscape}, editPhase)
	if got.Action != Block || len(got.Escapes) != 0 {
		t.Fatalf("Decision = %+v, want an unwaivable Block", got)
	}
}

// TestEvaluateAdded_TheMoreSevereHitWinsWhateverTheOrder pins the composition
// rule: a Block outranks a Warn wherever it sits in the slice.
func TestEvaluateAdded_TheMoreSevereHitWinsWhateverTheOrder(t *testing.T) {
	t.Parallel()
	post := "sleep(1) //nolint\n"
	for name, policies := range map[string][]policy{
		"warn first":  {scopeNolint, scopeSleep},
		"block first": {scopeSleep, scopeNolint},
	} {
		got := evaluateAdded(post, linesOf(1), defaultLang, policies, editPhase)
		if got.Action != Block || got.Policy != "sleep" {
			t.Fatalf("%s: Decision = %+v, want the sleep Block", name, got)
		}
	}
}

// TestEvaluateAdded_APolicyThatWarnsAtEditBlocksAtCommit pins the phase
// matrix through the evaluator: the same hit is a Warn while editing and a
// Block at the commit wall.
func TestEvaluateAdded_APolicyThatWarnsAtEditBlocksAtCommit(t *testing.T) {
	t.Parallel()
	post := "call() //nolint\n"
	if got := evaluateAdded(post, linesOf(1), defaultLang, []policy{scopeNolint}, editPhase); got.Action != Warn || got.Policy != "nolint" {
		t.Fatalf("edit phase: Decision = %+v, want Warn naming nolint", got)
	}
	if got := evaluateAdded(post, linesOf(1), defaultLang, []policy{scopeNolint}, commitPhase); got.Action != Block {
		t.Fatalf("commit phase: Decision = %+v, want Block", got)
	}
}

// TestEvaluateAdded_TiesGoToTheFirstPolicy pins that among equally severe hits
// the first in slice order is the one named.
func TestEvaluateAdded_TiesGoToTheFirstPolicy(t *testing.T) {
	t.Parallel()
	second := scopeNolint
	second.name = "second-nolint"
	got := evaluateAdded("call() //nolint\n", linesOf(1), defaultLang, []policy{scopeNolint, second}, editPhase)
	if got.Policy != "nolint" {
		t.Fatalf("Decision = %+v, want the first policy named on a tie", got)
	}
}

// TestEvaluateAdded_TheMaskedViewHidesAQuotedHit pins that policies see the
// file's masked post-image: a token that only appears inside a string never
// trips, because evaluateAdded masks the whole file before restricting.
func TestEvaluateAdded_TheMaskedViewHidesAQuotedHit(t *testing.T) {
	t.Parallel()
	got := evaluateAdded("msg := \"sleep(1)\"\n", linesOf(1), defaultLang, []policy{scopeSleep}, editPhase)
	if got.Action != Allow {
		t.Fatalf("Decision = %+v, want Allow: the only sleep is inside a string literal", got)
	}
}

// TestRestrict_KeepsOnlyTheGivenLinesOfBothViewsAndAllOfWhole pins the
// restricted view: the code and directives views shrink to the given lines,
// in order, and the whole-file code view is left alone so a structural
// policy still sees its context.
func TestRestrict_KeepsOnlyTheGivenLinesOfBothViewsAndAllOfWhole(t *testing.T) {
	t.Parallel()
	full := newView("one\ntwo // note\nthree\n", defaultLang)
	got := restrict(full, linesOf(1, 3))
	if got.Code != "one\nthree\n" {
		t.Fatalf("Code = %q, want lines 1 and 3", got.Code)
	}
	if got.directives != "one\nthree\n" {
		t.Fatalf("directives = %q, want lines 1 and 3", got.directives)
	}
	if got.whole != full.whole {
		t.Fatalf("whole = %q, want the file-wide code view %q", got.whole, full.whole)
	}
}

// TestRestrict_DirectivesKeepTheCommentsTheCodeViewBlanks pins that the two
// restricted views differ where they should: a comment survives in the
// directives view and is blanked from the code view.
func TestRestrict_DirectivesKeepTheCommentsTheCodeViewBlanks(t *testing.T) {
	t.Parallel()
	got := restrict(newView("call() // keep-me\n", defaultLang), linesOf(1))
	if !strings.Contains(got.directives, "keep-me") {
		t.Fatalf("directives = %q, want the comment preserved", got.directives)
	}
	if strings.Contains(got.Code, "keep-me") {
		t.Fatalf("Code = %q, want the comment blanked", got.Code)
	}
}

// TestIntersectLines_KeepsOnlyWhatBothHold pins the set intersection: a line
// in one set only is dropped, in either direction.
func TestIntersectLines_KeepsOnlyWhatBothHold(t *testing.T) {
	t.Parallel()
	got := intersectLines(linesOf(1, 2, 3), linesOf(2, 3, 4))
	if want := linesOf(2, 3); !reflect.DeepEqual(got, want) {
		t.Fatalf("intersectLines = %v, want %v", got, want)
	}
	if got := intersectLines(linesOf(1), linesOf()); len(got) != 0 {
		t.Fatalf("intersectLines with an empty set = %v, want none", got)
	}
}

// TestWithoutLines_DropsExactlyTheNamedLines pins the set difference: lines
// named are removed, the rest stay, and a line named but absent adds nothing.
func TestWithoutLines_DropsExactlyTheNamedLines(t *testing.T) {
	t.Parallel()
	got := withoutLines(linesOf(1, 2, 3), linesOf(2, 9))
	if want := linesOf(1, 3); !reflect.DeepEqual(got, want) {
		t.Fatalf("withoutLines = %v, want %v", got, want)
	}
}

// TestIntroducedLines_AMovedDirectiveIsNotIntroduced pins the move rule on the
// directives view: a suppression that only moved within its file reads the same
// before and after.
func TestIntroducedLines_AMovedDirectiveIsNotIntroduced(t *testing.T) {
	t.Parallel()
	pre := "call() //nolint\nother()\n"
	post := "other()\ncall() //nolint\n"
	if got := introducedLines(pre, post, defaultLang); len(got) != 0 {
		t.Fatalf("introducedLines = %v, want none for a pure move", got)
	}
}

// TestIntroducedLines_ANewDirectiveIsIntroducedAtItsLine pins the positive
// case: the line that gained the directive is the introduced one.
func TestIntroducedLines_ANewDirectiveIsIntroducedAtItsLine(t *testing.T) {
	t.Parallel()
	pre := "call()\nother()\n"
	post := "call()\nother() //nolint\n"
	if got, want := introducedLines(pre, post, defaultLang), linesOf(2); !reflect.DeepEqual(got, want) {
		t.Fatalf("introducedLines = %v, want %v", got, want)
	}
}

// TestIntroducedLines_ADirectiveThatWasBlankInAStringIsNewWhenItGoesLive pins
// the reason the directives view is compared: text that sat inside a string
// before and is a live comment after counts as new.
func TestIntroducedLines_ADirectiveThatWasBlankInAStringIsNewWhenItGoesLive(t *testing.T) {
	t.Parallel()
	pre := "msg := \"//nolint\"\n"
	post := "msg := \"//nolint\" //nolint\n"
	if got := introducedLines(pre, post, defaultLang); !got[1] {
		t.Fatalf("introducedLines = %v, want line 1: the comment is live now", got)
	}
}

// TestRemovedTexts_CountsWhatTheChangeTookOutByMultiplicity pins the mirror of
// introducedLines: trimmed directive-view texts, with a count per text.
func TestRemovedTexts_CountsWhatTheChangeTookOutByMultiplicity(t *testing.T) {
	t.Parallel()
	pre := "keep()\n  //nolint\n//nolint\nother()\n"
	post := "keep()\nother()\n"
	got := removedTexts(pre, post, defaultLang)
	if want := map[string]int{"//nolint": 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("removedTexts = %v, want %v", got, want)
	}
}

// TestRemovedTexts_APurelyMovedLineRemovesNothing pins that a line still in
// post, wherever it now sits, was not taken out.
func TestRemovedTexts_APurelyMovedLineRemovesNothing(t *testing.T) {
	t.Parallel()
	if got := removedTexts("a()\n//nolint\n", "//nolint\na()\n", defaultLang); len(got) != 0 {
		t.Fatalf("removedTexts = %v, want none", got)
	}
}

// TestAbsorbMoved_EachRemovalPaysForAtMostOneAdditionLowestLineFirst pins the
// pool arithmetic: one removed copy absorbs the FIRST introduced copy, and the
// second copy is still introduced.
func TestAbsorbMoved_EachRemovalPaysForAtMostOneAdditionLowestLineFirst(t *testing.T) {
	t.Parallel()
	post := "call() //nolint\nfiller()\ncall() //nolint\n"
	pool := map[string]int{"call() //nolint": 1}
	got := absorbMoved(post, defaultLang, linesOf(1, 3), pool)
	if want := linesOf(3); !reflect.DeepEqual(got, want) {
		t.Fatalf("absorbMoved = %v, want %v", got, want)
	}
	if pool["call() //nolint"] != 0 {
		t.Fatalf("pool = %v, want the paid count taken", pool)
	}
}

// TestAbsorbMoved_AnEmptyPoolAbsorbsNothing pins that with nothing removed every
// introduced line stays introduced.
func TestAbsorbMoved_AnEmptyPoolAbsorbsNothing(t *testing.T) {
	t.Parallel()
	got := absorbMoved("call() //nolint\n", defaultLang, linesOf(1), map[string]int{})
	if want := linesOf(1); !reflect.DeepEqual(got, want) {
		t.Fatalf("absorbMoved = %v, want %v", got, want)
	}
}

// TestLangOf_ReadsTheCommentSyntaxFromTheExtension pins the derivation from
// the language table: a `#` comment opens no string in Python, Ruby, shell,
// TOML and YAML, the Rust lexer reads a lifetime as code, the default row is
// C-family where `#` is code, and case does not matter.
func TestLangOf_ReadsTheCommentSyntaxFromTheExtension(t *testing.T) {
	t.Parallel()
	hash := "a = 1 # it's\nb = 'q'\n"
	hashWant := "a = 1 # it's\nb = ' '\n"
	cases := map[string]struct{ src, want string }{
		"a.py":   {hash, hashWant},
		"a.RB":   {hash, hashWant},
		"a.sh":   {hash, hashWant},
		"a.toml": {hash, hashWant},
		"a.yml":  {"a: 1 # it's\nb: 'q'\n", "a: 1 # it's\nb: ' '\n"},
		"a.rs":   {"fn f<'a>(x: &'a str) { \"x\" }", "fn f<'a>(x: &'a str) { \" \" }"},
		"a.go":   {hash, "a = 1 # it' \n    'q'\n"},
		"a.ts":   {hash, "a = 1 # it' \n    'q'\n"},
		"a":      {hash, "a = 1 # it' \n    'q'\n"},
	}
	for path, c := range cases {
		if got := langOf("", path).mask(c.src, false); got != c.want {
			t.Errorf("langOf(%q).mask(%q) = %q, want %q", path, c.src, got, c.want)
		}
	}
}
