package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// A reason with no "kind=" prefix at all is every entry that predates issue
// #268. It must keep working exactly as before: read as equivalent, never
// refused (backward compatibility).
func TestParseAcceptKind_UnkindedReasonReadsAsEquivalent(t *testing.T) {
	t.Parallel()
	kind, evidence, ok := parseAcceptKind("the two forms compute the same thing")
	if !ok {
		t.Fatal("an unkinded reason must parse, not be refused")
	}
	if kind != acceptKindEquivalent {
		t.Fatalf("kind = %v, want acceptKindEquivalent", kind)
	}
	if evidence != "" {
		t.Fatalf("evidence = %q, want none for an unkinded entry", evidence)
	}
}

// The three literal kinds each parse to their own value, with the evidence
// field the CLAIM they make requires: none for equivalent, the test name for
// runner-scoped, the tracking issue for capability-parked.
func TestParseAcceptKind_EachOfTheThreeKindsParsesSeparately(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		reason       string
		wantKind     acceptKind
		wantEvidence string
	}{
		{"equivalent", "kind=equivalent: the two forms compute the same thing", acceptKindEquivalent, ""},
		{"unobservable-runner", "kind=unobservable-runner test=TestSoakBudget_CoversTheChurnHelper: only the soak tier exercises this", acceptKindUnobservableRunner, "TestSoakBudget_CoversTheChurnHelper"},
		{"unobservable-capability", "kind=unobservable-capability issue=310: bevy exposes no reader for this gizmo output yet", acceptKindUnobservableCapability, "310"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, evidence, ok := parseAcceptKind(c.reason)
			if !ok {
				t.Fatalf("parseAcceptKind(%q) refused, want it to parse", c.reason)
			}
			if kind != c.wantKind {
				t.Fatalf("kind = %v, want %v", kind, c.wantKind)
			}
			if evidence != c.wantEvidence {
				t.Fatalf("evidence = %q, want %q", evidence, c.wantEvidence)
			}
		})
	}
}

// unobservable-runner is a claim about SCOPE, cheap to check because it names
// a real test — so the name is the one thing this parse enforces the
// presence of. Missing it, or naming the wrong evidence key, is refused
// rather than silently downgraded to equivalent.
func TestParseAcceptKind_UnobservableRunnerWithoutTestEvidenceIsRefused(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{
		"kind=unobservable-runner: only the soak tier exercises this",
		"kind=unobservable-runner issue=310: wrong evidence key for this kind",
	} {
		if _, _, ok := parseAcceptKind(reason); ok {
			t.Errorf("parseAcceptKind(%q) parsed, want refused — a runner-scoped claim without a named test is not verifiable today", reason)
		}
	}
}

// unobservable-capability is a claim about an upstream API that does not
// exist yet, so the tracking reference is what makes it revisitable — missing
// it, or naming the wrong evidence key, is refused.
func TestParseAcceptKind_UnobservableCapabilityWithoutIssueEvidenceIsRefused(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{
		"kind=unobservable-capability: bevy exposes no reader for this yet",
		"kind=unobservable-capability test=TestFoo: wrong evidence key for this kind",
	} {
		if _, _, ok := parseAcceptKind(reason); ok {
			t.Errorf("parseAcceptKind(%q) parsed, want refused — a capability claim with no tracking reference is not revisitable", reason)
		}
	}
}

// A misspelled kind must be refused loudly and NAMED, never silently read as
// equivalent — that would be the exact silent-degradation failure issue #268
// exists to close.
func TestParseAcceptKind_MisspelledKindIsRefusedNotReadAsEquivalent(t *testing.T) {
	t.Parallel()
	kind, _, ok := parseAcceptKind("kind=unobservable: which of the two new kinds is this?")
	if ok {
		t.Fatal("a misspelled kind parsed as if it were valid")
	}
	if kind == acceptKindUnobservableRunner || kind == acceptKindUnobservableCapability {
		t.Fatalf("a refused kind must not report a specific kind: got %v", kind)
	}
}

// acceptedMutants keeps an unkinded entry (equivalent, matching the
// accept-list's own historical header claim) but names a misspelled-kind
// entry in bad rather than silently dropping or accepting it.
func TestAcceptedMutants_NamesAMisspelledKindEntryInBad(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "aphrollo.toml", strings.Join([]string{
		"[aphrollo]",
		`mutation-accept = [`,
		`  "calc.go:1 CONDITIONALS_BOUNDARY # legacy entry, no kind directive",`,
		`  "calc.go:2 ARITHMETIC_BASE # kind=unobservable: which of the two new kinds is this?",`,
		"]",
	}, "\n"))

	list, bad := acceptedMutants(root)
	if _, ok := list.ByLine["calc.go:1 CONDITIONALS_BOUNDARY"]; !ok {
		t.Fatal("the unkinded legacy entry must still be in the accepted list")
	}
	if _, ok := list.ByLine["calc.go:2 ARITHMETIC_BASE"]; ok {
		t.Fatal("the misspelled-kind entry must not be accepted")
	}
	if len(bad) != 1 || !strings.Contains(bad[0], "calc.go:2 ARITHMETIC_BASE") {
		t.Fatalf("bad = %q, want the misspelled entry named verbatim", bad)
	}
}

// The split reports how much of the accepted set is closed (equivalent)
// versus parked (the two unobservable kinds) SEPARATELY, so a reader — and a
// merge gate — never reads parked work as merely out of scope.
func TestSplitAcceptedSurvivors_CountsAcceptedSurvivorsByKindSeparately(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "aphrollo.toml", strings.Join([]string{
		"[aphrollo]",
		`mutation-accept = [`,
		`  "calc.go:1 CONDITIONALS_BOUNDARY # kind=equivalent: the two forms compute the same thing",`,
		`  "calc.go:2 ARITHMETIC_BASE # kind=unobservable-runner test=TestSoak: only the soak tier exercises this",`,
		`  "calc.go:3 CONDITIONALS_NEGATION # kind=unobservable-capability issue=310: no reader exists yet",`,
		"]",
	}, "\n"))

	list, bad := acceptedMutants(root)
	if len(bad) != 0 {
		t.Fatalf("bad = %q, want none — all three entries carry a valid kind", bad)
	}
	accepted, unaccepted, kinds, _ := splitAcceptedSurvivors(list, []MutantOutcome{
		{File: filepath.FromSlash("calc.go"), Line: 1, Mutation: "CONDITIONALS_BOUNDARY", Status: "missed"},
		{File: filepath.FromSlash("calc.go"), Line: 2, Mutation: "ARITHMETIC_BASE", Status: "missed"},
		{File: filepath.FromSlash("calc.go"), Line: 3, Mutation: "CONDITIONALS_NEGATION", Status: "missed"},
	})

	if len(accepted) != 3 {
		t.Fatalf("accepted = %d, want 3", len(accepted))
	}
	if kinds.AcceptedEquivalent != 1 {
		t.Errorf("AcceptedEquivalent = %d, want 1", kinds.AcceptedEquivalent)
	}
	if kinds.AcceptedUnobservableRunner != 1 {
		t.Errorf("AcceptedUnobservableRunner = %d, want 1", kinds.AcceptedUnobservableRunner)
	}
	if kinds.AcceptedUnobservableCapability != 1 {
		t.Errorf("AcceptedUnobservableCapability = %d, want 1", kinds.AcceptedUnobservableCapability)
	}
	if len(unaccepted) != 0 {
		t.Fatalf("unaccepted = %d, want none — all three entries carry a valid kind", len(unaccepted))
	}
}
