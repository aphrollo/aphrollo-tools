package tdd

import (
	"strings"
	"testing"
)

// The real case (issue #282): three distinct survivors on
// crates/editor_client/src/creator.rs:108 at columns 5, 33 and 71 — different
// mutants, one line. The accept-list keyed on file:line and mutator alone, so
// accepting one silently accepted every same-line sibling too, including two
// nobody had ever looked at.

// TestSplitAcceptedSurvivors_ExactColumnEntryAcceptsOnlyItsOwnMutant pins the
// fix's positive case: an accept-list entry that NAMES a column matches only
// the survivor at that column, leaving its same-line siblings exactly as
// unaccepted as if no entry existed for the line at all.
func TestSplitAcceptedSurvivors_ExactColumnEntryAcceptsOnlyItsOwnMutant(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", strings.Join([]string{
		"[aphrollo]",
		`mutation-accept = [`,
		`  "crates/editor_client/src/creator.rs:108:33 replace || with && # the branches are provably equivalent here",`,
		"]",
	}, "\n"))
	survivors := []MutantOutcome{
		{File: "crates/editor_client/src/creator.rs", Line: 108, Col: 5, Mutation: "replace || with &&", Status: "missed"},
		{File: "crates/editor_client/src/creator.rs", Line: 108, Col: 33, Mutation: "replace || with &&", Status: "missed"},
		{File: "crates/editor_client/src/creator.rs", Line: 108, Col: 71, Mutation: "replace || with &&", Status: "missed"},
	}

	list, bad := acceptedMutants(root)
	if len(bad) != 0 {
		t.Fatalf("bad = %v, want the column-bearing entry to parse cleanly", bad)
	}
	accepted, unaccepted, _, _ := splitAcceptedSurvivors(list, survivors)

	if len(accepted) != 1 || accepted[0].Col != 33 {
		t.Fatalf("accepted = %+v, want only the column-33 mutant the entry names", accepted)
	}
	if len(unaccepted) != 2 {
		t.Fatalf("unaccepted = %+v, want the two same-line siblings nobody reviewed to still block", unaccepted)
	}
}

// TestSplitAcceptedSurvivors_RefusesAColumnLessEntryWhenSameLineSiblingsExist
// pins the fix's negative case: a column-LESS entry cannot tell which of
// several same-line mutants it was written for, so it must not silently
// admit any of them.
func TestSplitAcceptedSurvivors_RefusesAColumnLessEntryWhenSameLineSiblingsExist(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", strings.Join([]string{
		"[aphrollo]",
		`mutation-accept = [`,
		`  "crates/editor_client/src/creator.rs:108 replace || with && # reviewed one of these",`,
		"]",
	}, "\n"))
	survivors := []MutantOutcome{
		{File: "crates/editor_client/src/creator.rs", Line: 108, Col: 5, Mutation: "replace || with &&", Status: "missed"},
		{File: "crates/editor_client/src/creator.rs", Line: 108, Col: 33, Mutation: "replace || with &&", Status: "missed"},
		{File: "crates/editor_client/src/creator.rs", Line: 108, Col: 71, Mutation: "replace || with &&", Status: "missed"},
	}

	list, _ := acceptedMutants(root)
	accepted, unaccepted, _, ambiguous := splitAcceptedSurvivors(list, survivors)

	if len(accepted) != 0 {
		t.Fatalf("accepted = %+v, want none — a column-less entry cannot tell these three apart", accepted)
	}
	if len(unaccepted) != 3 {
		t.Fatalf("unaccepted = %+v, want all three still blocking until the entry names a column", unaccepted)
	}
	if len(ambiguous) == 0 {
		t.Fatal("an ambiguous column-less entry must be named, not silently dropped")
	}
}

// TestSplitAcceptedSurvivors_ColumnLessEntryStillAcceptsTheOnlyMutantOnItsLine
// is the compatibility guard: the overwhelmingly common case is one mutant
// per line, and an accept-list entry written before #282 has no column at
// all. It must keep matching exactly as before.
func TestSplitAcceptedSurvivors_ColumnLessEntryStillAcceptsTheOnlyMutantOnItsLine(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", strings.Join([]string{
		"[aphrollo]",
		`mutation-accept = [`,
		`  "calc.go:4 CONDITIONALS_BOUNDARY # the bound is the sweep's own age bar",`,
		"]",
	}, "\n"))
	survivors := []MutantOutcome{
		{File: "calc.go", Line: 4, Col: 9, Mutation: "CONDITIONALS_BOUNDARY", Status: "missed"},
	}

	list, _ := acceptedMutants(root)
	accepted, unaccepted, _, ambiguous := splitAcceptedSurvivors(list, survivors)

	if len(accepted) != 1 {
		t.Fatalf("accepted = %+v, want the lone mutant on the line still accepted", accepted)
	}
	if len(unaccepted) != 0 {
		t.Fatalf("unaccepted = %+v, want none", unaccepted)
	}
	if len(ambiguous) != 0 {
		t.Fatalf("ambiguous = %v, want none — there is only one mutant on this line", ambiguous)
	}
}

// TestAcceptedMutants_ParsesDistinctColumnEntriesOnOneLine pins the grammar
// extension itself: three entries naming the same file, line and mutator but
// different columns must all parse, none refused as a duplicate key.
func TestAcceptedMutants_ParsesDistinctColumnEntriesOnOneLine(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", strings.Join([]string{
		"[aphrollo]",
		`mutation-accept = [`,
		`  "creator.rs:108:5 replace || with && # first reviewed",`,
		`  "creator.rs:108:33 replace || with && # second reviewed",`,
		`  "creator.rs:108:71 replace || with && # third reviewed",`,
		"]",
	}, "\n"))
	survivors := []MutantOutcome{
		{File: "creator.rs", Line: 108, Col: 5, Mutation: "replace || with &&", Status: "missed"},
		{File: "creator.rs", Line: 108, Col: 33, Mutation: "replace || with &&", Status: "missed"},
		{File: "creator.rs", Line: 108, Col: 71, Mutation: "replace || with &&", Status: "missed"},
	}

	list, bad := acceptedMutants(root)
	if len(bad) != 0 {
		t.Fatalf("bad = %v, want all three column-bearing entries to parse", bad)
	}
	accepted, unaccepted, _, ambiguous := splitAcceptedSurvivors(list, survivors)
	if len(accepted) != 3 {
		t.Fatalf("accepted = %+v, want all three distinct columns matched to their own entry", accepted)
	}
	if len(unaccepted) != 0 {
		t.Fatalf("unaccepted = %+v, want none", unaccepted)
	}
	if len(ambiguous) != 0 {
		t.Fatalf("ambiguous = %v, want none — every survivor matched an exact column", ambiguous)
	}
}
