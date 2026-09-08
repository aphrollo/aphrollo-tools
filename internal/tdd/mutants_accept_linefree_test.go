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
	want := "line-free entry for crates/x/src/lib.rs replace < with <= in f admits 3 sites"
	if !strings.Contains(v.Message, want) {
		t.Fatalf("report = %q, want it to carry %q so a reviewer sees the count", v.Message, want)
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
	})
	if len(bad) != 0 {
		t.Fatalf("bad = %v, want both entries to parse", bad)
	}
	survivors := []MutantOutcome{
		{File: "crates/x/src/lib.rs", Line: 41, Col: 9, Mutation: "replace < with <= in f", Status: "missed"},
	}

	accepted, unaccepted, kinds, _ := splitAcceptedSurvivors(list, survivors)

	if len(accepted) != 1 || len(unaccepted) != 0 {
		t.Fatalf("accepted = %+v, unaccepted = %+v, want the survivor accepted", accepted, unaccepted)
	}
	if kinds.AcceptedUnobservableRunner != 1 || kinds.AcceptedEquivalent != 0 {
		t.Fatalf("kinds = %+v, want the line entry's own claim counted, not the line-free one's", kinds)
	}
}

// TestAcceptList_LineFreeKeyWithNoReasonIsDropped keeps the rule the shape
// does not change: an accept-list nobody had to justify is a list of
// survivors somebody silenced, whatever the key looks like.
func TestAcceptList_LineFreeKeyWithNoReasonIsDropped(t *testing.T) {
	t.Parallel()
	list, bad := parseAcceptedMutants([]string{
		"crates/x/src/lib.rs replace < with <= in f",
	})
	if len(bad) != 0 {
		t.Fatalf("bad = %v, want the reasonless entry dropped silently, exactly as a line-keyed one is", bad)
	}
	survivors := []MutantOutcome{
		{File: "crates/x/src/lib.rs", Line: 41, Col: 9, Mutation: "replace < with <= in f", Status: "missed"},
	}

	accepted, unaccepted, _, _ := splitAcceptedSurvivors(list, survivors)

	if len(accepted) != 0 || len(unaccepted) != 1 {
		t.Fatalf("accepted = %+v, unaccepted = %+v, want the survivor still blocking — the entry states no reason", accepted, unaccepted)
	}
}
