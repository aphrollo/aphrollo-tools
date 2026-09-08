package tdd

import (
	"strings"
	"testing"
)

// A line-keyed accept entry shifts under every edit made above it and rots
// into noise within a week (issue #578). The consuming repo whose list is
// being migrated strips the line deliberately and keys on the file plus the
// mutant's own text, which is the same reason this repo's ratchet baselines
// key on content rather than line.

// TestAcceptList_LineFreeEntryMatchesTheMutantAtAnyLine pins the shape
// itself: an entry with no ":<line>" suffix matches its mutant wherever the
// file happens to carry it, and matches nothing else on that line.
func TestAcceptList_LineFreeEntryMatchesTheMutantAtAnyLine(t *testing.T) {
	t.Parallel()
	list, bad := parseAcceptedMutants([]string{
		"crates/x/src/lib.rs replace < with <= in f # kind=equivalent: the bound is unreachable",
	})
	if len(bad) != 0 {
		t.Fatalf("bad = %v, want the line-free entry to parse cleanly", bad)
	}

	atLine41 := []MutantOutcome{
		{File: "crates/x/src/lib.rs", Line: 41, Col: 9, Mutation: "replace < with <= in f", Status: "missed"},
	}
	accepted, unaccepted, _, _ := splitAcceptedSurvivors(list, atLine41)
	if len(accepted) != 1 || len(unaccepted) != 0 {
		t.Fatalf("at line 41: accepted = %+v, unaccepted = %+v, want the survivor accepted", accepted, unaccepted)
	}

	movedTo57 := []MutantOutcome{
		{File: "crates/x/src/lib.rs", Line: 57, Col: 9, Mutation: "replace < with <= in f", Status: "missed"},
	}
	accepted, unaccepted, _, _ = splitAcceptedSurvivors(list, movedTo57)
	if len(accepted) != 1 || len(unaccepted) != 0 {
		t.Fatalf("after the edit above it moved the mutant to line 57: accepted = %+v, unaccepted = %+v, want it still accepted", accepted, unaccepted)
	}

	otherMutation := []MutantOutcome{
		{File: "crates/x/src/lib.rs", Line: 41, Col: 9, Mutation: "replace > with >= in f", Status: "missed"},
	}
	accepted, unaccepted, _, _ = splitAcceptedSurvivors(list, otherMutation)
	if len(accepted) != 0 || len(unaccepted) != 1 {
		t.Fatalf("a different mutation on the same line: accepted = %+v, unaccepted = %+v, want it unaccepted — the entry names one mutant, not one line", accepted, unaccepted)
	}
}

// TestAcceptList_LineFreeEntryAdmitsEverySiteAndReportsTheCount pins the
// trade the shape makes: dropping the line means the entry cannot tell one
// site of the same mutation from another, so it admits all of them — and the
// report says how many, so a reviewer sees what one line signed off on.
func TestAcceptList_LineFreeEntryAdmitsEverySiteAndReportsTheCount(t *testing.T) {
	t.Parallel()
	cfg := MutantsConfig{Accept: []string{
		"crates/x/src/lib.rs replace < with <= in f # kind=equivalent: every one of these bounds is unreachable",
	}}
	survivors := []MutantOutcome{
		{File: "crates/x/src/lib.rs", Line: 41, Col: 9, Mutation: "replace < with <= in f", Status: "missed"},
		{File: "crates/x/src/lib.rs", Line: 57, Col: 9, Mutation: "replace < with <= in f", Status: "missed"},
		{File: "crates/x/src/lib.rs", Line: 88, Col: 13, Mutation: "replace < with <= in f", Status: "missed"},
	}

	v := judgeMutants(cfg, survivors)

	if v.Refused {
		t.Fatalf("Refused = true, want false — a line-free entry admitting several sites is the declared trade, not a refusal:\n%s", v.Message)
	}
	if v.Accepted != 3 {
		t.Fatalf("Accepted = %d, want 3 — every site of the named mutation", v.Accepted)
	}
	// The whole rendered line, prefix included: an applied entry must not
	// read as a refused one, and a reviewer scanning the report for
	// "mutation-accept:" has to find it.
	want := "mutation-accept: line-free entry for crates/x/src/lib.rs replace < with <= in f admits 3 sites\n"
	if !strings.Contains(v.Message, want) {
		t.Fatalf("report = %q, want it to carry %q so a reviewer sees the count", v.Message, want)
	}
}

// TestJudgeMutants_ARefusedColumnLessEntryIsRenderedAsARefusal is the other
// half of that rendering: a note the split REFUSED must say so in the
// report, in its own wording, never in the wording of an entry that was
// applied.
func TestJudgeMutants_ARefusedColumnLessEntryIsRenderedAsARefusal(t *testing.T) {
	t.Parallel()
	cfg := MutantsConfig{Accept: []string{
		"crates/x/src/lib.rs:108 replace || with && # kind=equivalent: reviewed one of these",
	}}
	survivors := []MutantOutcome{
		{File: "crates/x/src/lib.rs", Line: 108, Col: 5, Mutation: "replace || with &&", Status: "missed"},
		{File: "crates/x/src/lib.rs", Line: 108, Col: 33, Mutation: "replace || with &&", Status: "missed"},
	}

	v := judgeMutants(cfg, survivors)

	if !v.Refused {
		t.Fatalf("Refused = false, want true — neither same-line mutant was accepted:\n%s", v.Message)
	}
	want := "mutation-accept entry refused (column-less entry for crates/x/src/lib.rs:108 replace || with && " +
		"admits 2 same-line mutants — add a column to disambiguate)\n"
	if !strings.Contains(v.Message, want) {
		t.Fatalf("report = %q, want it to carry %q", v.Message, want)
	}
}

// TestAcceptList_AmbiguousLineEntryFallsThroughToTheLineFreeOne pins what a
// line-free entry means when a column-less LINE entry cannot tell its own
// line's mutants apart: the line-free entry admits every site of that
// mutation by definition, so it admits both of these too, and nothing is
// refused. The refusal is for a list that offers nothing else.
func TestAcceptList_AmbiguousLineEntryFallsThroughToTheLineFreeOne(t *testing.T) {
	t.Parallel()
	survivors := []MutantOutcome{
		{File: "crates/x/src/lib.rs", Line: 108, Col: 5, Mutation: "replace || with &&", Status: "missed"},
		{File: "crates/x/src/lib.rs", Line: 108, Col: 33, Mutation: "replace || with &&", Status: "missed"},
	}

	withLineFree, bad := parseAcceptedMutants([]string{
		"crates/x/src/lib.rs:108 replace || with && # kind=equivalent: reviewed one of these",
		"crates/x/src/lib.rs replace || with && # kind=unobservable-runner test=TestSoak: only the soak tier exercises these",
	})
	if len(bad) != 0 {
		t.Fatalf("bad = %v, want both entries to parse", bad)
	}
	accepted, unaccepted, kinds, notes := splitAcceptedSurvivors(withLineFree, survivors)
	if len(accepted) != 2 || len(unaccepted) != 0 {
		t.Fatalf("accepted = %+v, unaccepted = %+v, want both admitted by the line-free entry", accepted, unaccepted)
	}
	if kinds.AcceptedUnobservableRunner != 2 || kinds.AcceptedEquivalent != 0 {
		t.Fatalf("kinds = %+v, want both counted against the line-free entry's own claim", kinds)
	}
	for _, n := range notes {
		if n.Refused {
			t.Fatalf("notes = %+v, want no refusal — the line-free entry admits every site, so nothing was left unaccepted", notes)
		}
	}

	withoutLineFree, bad := parseAcceptedMutants([]string{
		"crates/x/src/lib.rs:108 replace || with && # kind=equivalent: reviewed one of these",
	})
	if len(bad) != 0 {
		t.Fatalf("bad = %v, want the line entry to parse", bad)
	}
	accepted, unaccepted, _, notes = splitAcceptedSurvivors(withoutLineFree, survivors)
	if len(accepted) != 0 || len(unaccepted) != 2 {
		t.Fatalf("accepted = %+v, unaccepted = %+v, want both still blocking — nothing in the list can tell them apart", accepted, unaccepted)
	}
	wantNote := "column-less entry for crates/x/src/lib.rs:108 replace || with && admits 2 same-line mutants — add a column to disambiguate"
	if len(notes) != 1 || !notes[0].Refused || notes[0].Text != wantNote {
		t.Fatalf("notes = %+v, want exactly one refusal reading %q", notes, wantNote)
	}
}

// TestAcceptList_ColumnAndLineEntriesOutrankTheLineFreeOne pins the
// precedence: the more specific entry is the one that was written about THIS
// mutant, so its claim — not the blanket one — is what the kind tally counts.
func TestAcceptList_ColumnAndLineEntriesOutrankTheLineFreeOne(t *testing.T) {
	t.Parallel()
	list, bad := parseAcceptedMutants([]string{
		"crates/x/src/lib.rs replace < with <= in f # kind=equivalent: the bound is unreachable",
		"crates/x/src/lib.rs:41 replace < with <= in f # kind=unobservable-runner test=bounds_are_checked: covered in a tier this runner skips",
		"crates/x/src/lib.rs:41:9 replace < with <= in f # kind=unobservable-capability issue=578: nothing can read what this writes",
	})
	if len(bad) != 0 {
		t.Fatalf("bad = %v, want all three entries to parse", bad)
	}

	atTheNamedColumn := []MutantOutcome{
		{File: "crates/x/src/lib.rs", Line: 41, Col: 9, Mutation: "replace < with <= in f", Status: "missed"},
	}
	accepted, unaccepted, kinds, _ := splitAcceptedSurvivors(list, atTheNamedColumn)
	if len(accepted) != 1 || len(unaccepted) != 0 {
		t.Fatalf("accepted = %+v, unaccepted = %+v, want the survivor accepted", accepted, unaccepted)
	}
	if kinds.AcceptedUnobservableCapability != 1 || kinds.AcceptedUnobservableRunner != 0 || kinds.AcceptedEquivalent != 0 {
		t.Fatalf("kinds = %+v, want the column entry's own claim counted, outranking both the line and the line-free entry", kinds)
	}

	elsewhereOnTheSameLine := []MutantOutcome{
		{File: "crates/x/src/lib.rs", Line: 41, Col: 20, Mutation: "replace < with <= in f", Status: "missed"},
	}
	accepted, unaccepted, kinds, _ = splitAcceptedSurvivors(list, elsewhereOnTheSameLine)
	if len(accepted) != 1 || len(unaccepted) != 0 {
		t.Fatalf("accepted = %+v, unaccepted = %+v, want the survivor accepted", accepted, unaccepted)
	}
	if kinds.AcceptedUnobservableRunner != 1 || kinds.AcceptedEquivalent != 0 {
		t.Fatalf("kinds = %+v, want the line entry's own claim counted, outranking the line-free one", kinds)
	}
}

// TestAcceptList_LineFreeKeyWithNoReasonIsDropped keeps the rule the shape
// does not change: an accept-list nobody had to justify is a list of
// survivors somebody silenced, whatever the key looks like. Its reasoned
// sibling in the same list is the control — the entry is dropped for the
// missing reason, not for being line-free.
func TestAcceptList_LineFreeKeyWithNoReasonIsDropped(t *testing.T) {
	t.Parallel()
	list, bad := parseAcceptedMutants([]string{
		"crates/x/src/lib.rs replace < with <= in f",
		"crates/x/src/lib.rs replace > with >= in g # kind=equivalent: this bound is unreachable too",
	})
	if len(bad) != 0 {
		t.Fatalf("bad = %v, want the reasonless entry dropped silently, exactly as a line-keyed one is", bad)
	}
	survivors := []MutantOutcome{
		{File: "crates/x/src/lib.rs", Line: 41, Col: 9, Mutation: "replace < with <= in f", Status: "missed"},
		{File: "crates/x/src/lib.rs", Line: 52, Col: 9, Mutation: "replace > with >= in g", Status: "missed"},
	}

	accepted, unaccepted, _, _ := splitAcceptedSurvivors(list, survivors)

	if len(accepted) != 1 || accepted[0].Mutation != "replace > with >= in g" {
		t.Fatalf("accepted = %+v, want only the line-free entry that states a reason", accepted)
	}
	if len(unaccepted) != 1 || unaccepted[0].Mutation != "replace < with <= in f" {
		t.Fatalf("unaccepted = %+v, want the survivor whose entry states no reason still blocking", unaccepted)
	}
}

// TestAcceptList_MalformedLineIsStillRefusedNotReadAsLineFree keeps the
// third shape from swallowing the failures the first two used to report. An
// entry that was WRITTEN line-keyed and whose line did not parse must stay
// in bad and be quoted back: read as a line-free key instead, it is stored
// as an entry that can never match a survivor and never says why.
func TestAcceptList_MalformedLineIsStillRefusedNotReadAsLineFree(t *testing.T) {
	t.Parallel()
	entries := []string{
		"src/lib.rs:4x replace + with - # r",
		"src/lib.rs:12:x replace + with - # r",
		// A path with a space in it: the key is space-separated, so the
		// location half is "my" and the line is left in the mutation.
		"my dir/lib.rs:4 X # r",
	}

	list, bad := parseAcceptedMutants(entries)

	if len(bad) != len(entries) {
		t.Fatalf("bad = %v, want all %d malformed entries refused and quoted", bad, len(entries))
	}
	if len(list.LineFree) != 0 || len(list.ByLine) != 0 {
		t.Fatalf("list = %+v, want nothing stored — none of these entries can ever match a survivor", list)
	}
}
