package precommit

import (
	"reflect"
	"slices"
	"testing"
)

// ratchet: test_removed TestWithGoCIParity_InsertsRaceCountShuffleAndTimeout: withGoCIParity gained an atMerge parameter (cold review on #421 moved -race to the merge-only path), splitting the one "always all four flags" case into TestWithGoCIParity_AtPrecommitInsertsCountShuffleAndTimeoutOnly and TestWithGoCIParity_AtMergeAlsoInsertsRace below.
// ratchet: test_removed TestWithGoCIParity_AtPrecommitInsertsCountShuffleAndTimeoutOnly: -timeout=180s came off both stages entirely (second cold review on #421/#434 — CI's 180s bound does not fit this box's load and never will), so the "count, shuffle, timeout" case became TestWithGoCIParity_AtPrecommitInsertsCountAndShuffleOnly below, minus timeout.
// ratchet: test_removed TestPrecommitGoSuite_NeverPaysForRace: it asserted the commit-time suite's argv carried no -race, and the commit gate runs no suite at all now, so there is no argv left to make the claim about. What it protected -- that -race is paid at the merge and nowhere else -- is still pinned by TestWithGoCIParity_AtPrecommitInsertsCountAndShuffleOnly and TestWithGoCIParity_AtMergeAlsoInsertsRace below.
// ratchet: test_removed TestWithGoCIParity_AtMergeAlsoInsertsRace: same -timeout removal — its want no longer carries -timeout=180s, and the case is restated below under the same name with a corrected argv.

// TestWithGoCIParity_AtPrecommitInsertsCountAndShuffleOnly is the cold-review
// correction to #421: -race is several times slower and the box this gate
// runs on is already contended, so precommit — paid on every commit — gets
// only the two flags that cost nothing extra to run: -count=1, -shuffle=on.
func TestWithGoCIParity_AtPrecommitInsertsCountAndShuffleOnly(t *testing.T) {
	got := withGoCIParity(Runner{Cmd: "go", Args: []string{"test", "./internal/x"}}, false)
	want := Runner{Cmd: "go", Args: []string{"test", "-count=1", "-shuffle=on", "./internal/x"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withGoCIParity(atMerge=false) = %+v, want %+v", got, want)
	}
}

// TestWithGoCIParity_AtMergeAlsoInsertsRace: the merge gate is where CI's
// verdict is about to be trusted and where this repo's own commit frequency
// is lowest, so it is the affordable place to pay for the one genuinely
// expensive flag too.
func TestWithGoCIParity_AtMergeAlsoInsertsRace(t *testing.T) {
	got := withGoCIParity(Runner{Cmd: "go", Args: []string{"test", "./internal/x"}}, true)
	want := Runner{Cmd: "go", Args: []string{"test", "-race", "-count=1", "-shuffle=on", "./internal/x"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withGoCIParity(atMerge=true) = %+v, want %+v", got, want)
	}
}

// TestWithGoCIParity_NeverCarriesCIsTimeout is the second cold-review
// finding on #421/#434: CI's -timeout=180s was copied onto this box
// verbatim, but internal/tdd's own suite under real load (seven concurrent
// lanes) does not finish in 180s and never will — every measured local
// mechanical run since #432 landed hit that internal deadline and panicked
// mid-test (28 of 29 runs measured, 188-200s each), never a genuine
// assertion failure. The single stage-level ceiling (DefaultPrecommitTimeout,
// enforced by RunSuite's own context timeout) is the one bound that belongs
// locally; -timeout=180s must never ride along in EITHER mode.
func TestWithGoCIParity_NeverCarriesCIsTimeout(t *testing.T) {
	for _, atMerge := range []bool{false, true} {
		got := withGoCIParity(Runner{Cmd: "go", Args: []string{"test", "./internal/x"}}, atMerge)
		if slices.Contains(got.Args, "-timeout=180s") {
			t.Fatalf("withGoCIParity(atMerge=%v).Args = %v, must never carry CI's -timeout=180s locally", atMerge, got.Args)
		}
	}
}

// TestWithGoCIParity_NeverDoublesAFlagAlreadyPresent guards the idempotency
// the doc comment claims: calling it twice (or handing it a Runner that
// already names one of the flags) must not repeat a flag, in either mode.
func TestWithGoCIParity_NeverDoublesAFlagAlreadyPresent(t *testing.T) {
	for _, atMerge := range []bool{false, true} {
		once := withGoCIParity(Runner{Cmd: "go", Args: []string{"test", "./..."}}, atMerge)
		twice := withGoCIParity(once, atMerge)
		if !reflect.DeepEqual(once, twice) {
			t.Fatalf("withGoCIParity(atMerge=%v) applied twice = %+v, want unchanged %+v", atMerge, twice, once)
		}
	}
}

// TestWithGoCIParity_LeavesNonGoTestRunnersUntouched: cargo, vet, and the
// linter must never see these flags — go vet and golangci-lint do not
// understand them, and cargo has its own command shape entirely.
func TestWithGoCIParity_LeavesNonGoTestRunnersUntouched(t *testing.T) {
	for _, r := range []Runner{
		{Cmd: "cargo", Args: []string{"test", "-p", "alpha"}},
		{Cmd: "go", Args: []string{"vet", "./..."}},
		{Cmd: "golangci-lint", Args: []string{"run", "."}},
	} {
		for _, atMerge := range []bool{false, true} {
			if got := withGoCIParity(r, atMerge); !reflect.DeepEqual(got, r) {
				t.Fatalf("withGoCIParity(%+v, atMerge=%v) = %+v, want unchanged", r, atMerge, got)
			}
		}
	}
}

// The commit gate's "never pays for -race" test is gone with the stage it
// tested: the commit gate runs no suite at all now, so there is no runner
// left to check for the flag. TestPrecommit_RunsNoSuiteAtCommitTime pins
// the stronger claim, and the premerge half below still pins -race.

// TestMechanicalGoSuite_CarriesCIParityFlags proves the premergecommit path
// pays for -race on top of the two cheap flags: the merge is where CI's
// verdict is about to be trusted, and where this repo's own commit
// frequency is lowest, so it is the affordable place for the one genuinely
// expensive flag (cold review on #421). -timeout=180s never rides along,
// at either stage — see TestWithGoCIParity_NeverCarriesCIsTimeout.
func TestMechanicalGoSuite_CarriesCIParityFlags(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := Runner{Cmd: "go", Args: []string{"test", "-race", "-count=1", "-shuffle=on", "./internal/x"}}
	if len(seen) != 1 || !reflect.DeepEqual(seen[0], want) {
		t.Fatalf("mechanical (premerge) go runner = %+v, want one %+v", seen, want)
	}
}
