package ratchet

import (
	"path/filepath"
	"strings"
	"testing"
)

// twoLawRepo is fixtureRepo plus a SECOND law whose hit fixture no longer
// hits — the shape a selection has to be able to leave out of a run. Both
// laws are well-formed; only the second one's fixture is broken, so a run
// that judges it reports exactly one failure and a run that does not judge
// it reports none.
func twoLawRepo(t *testing.T) string {
	t.Helper()
	root := fixtureRepo(t)
	writeLaw(t, root, "broken-guard", strings.Replace(nanGuardLaw, `"nan-guard"`, `"broken-guard"`, 1))
	write(t, filepath.Join(root, ".ratchet", "fixtures", "broken-guard", "hit", "crates", "a", "src", "bare.rs"),
		"let a = 1;\n")
	write(t, filepath.Join(root, ".ratchet", "fixtures", "broken-guard", "expected.txt"),
		"crates/a/src/bare.rs:1\n")
	write(t, filepath.Join(root, ".ratchet", "fixtures", "broken-guard", "clean", "crates", "a", "src", "guarded.rs"),
		"let a = 1;\n")
	return root
}

// A fixtures run has one judge for the whole tree, which is exactly what a
// lane correcting a MATCHER cannot use: its new rows are by construction rows
// the installed binary must reject (issues #659, #673). Splitting the run
// needs the engine to be able to judge a named subset, so the two halves
// together cover every law exactly once.
func TestRunFixturesWith_OnlyJudgesTheNamedLawsAndLeavesTheRestUnrun(t *testing.T) {
	results, err := RunFixturesWith(twoLawRepo(t), FixtureOptions{Only: []string{"nan-guard"}})
	if err != nil {
		t.Fatalf("RunFixturesWith: %v", err)
	}
	if len(results) != 1 || results[0].Law != "nan-guard" {
		t.Fatalf("only nan-guard was asked for; results = %+v", results)
	}
	if len(results[0].Failures) != 0 {
		t.Fatalf("nan-guard's own fixtures are sound; failures = %v", results[0].Failures)
	}
}

// The other half of the same split: everything the other judge is NOT taking.
// A law left out here must leave no result at all rather than an empty one —
// an empty Failures slice is how "proved clean" is spelled, so a law nobody
// ran must never be able to spell it.
func TestRunFixturesWith_ExceptLeavesTheNamedLawToAnotherJudge(t *testing.T) {
	results, err := RunFixturesWith(twoLawRepo(t), FixtureOptions{Except: []string{"broken-guard"}})
	if err != nil {
		t.Fatalf("RunFixturesWith: %v", err)
	}
	if len(results) != 1 || results[0].Law != "nan-guard" {
		t.Fatalf("broken-guard was left to another judge; results = %+v", results)
	}
}

// Only naming a law the tree does not have is a caller that believes it
// asked for a verdict it will never get. Answering it with an empty, green
// run is the silent hole the whole split exists to avoid, so it is an error.
func TestRunFixturesWith_ErrorsWhenOnlyNamesALawTheTreeDoesNotHave(t *testing.T) {
	_, err := RunFixturesWith(fixtureRepo(t), FixtureOptions{Only: []string{"nan-guard", "no-such-law"}})
	if err == nil {
		t.Fatal("a law the tree does not have cannot be judged; RunFixturesWith returned no error")
	}
	if !strings.Contains(err.Error(), "no-such-law") {
		t.Fatalf("the error must name the law nobody can judge; got %v", err)
	}
}

// RunFixtures is RunFixturesWith with an empty selection, so the unselected
// call cannot drift away from the selected one.
func TestRunFixtures_JudgesEveryLawWhenNothingIsSelected(t *testing.T) {
	results, err := RunFixturesWith(twoLawRepo(t), FixtureOptions{})
	if err != nil {
		t.Fatalf("RunFixturesWith: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("an empty selection judges every law; results = %+v", results)
	}
}
