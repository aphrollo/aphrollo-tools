package precommit

import (
	"slices"
	"strings"
	"testing"
)

// hubMergeRepo is a Go module where two packages import hub, and a change to
// hub is staged: the shape of a lane that regenerates the package everything
// else imports.
func hubMergeRepo(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	write(t, root, "hub/hub.go", "package hub\n\nfunc H() int { return 1 }\n")
	write(t, root, "leaf1/l.go", "package leaf1\n\nimport \"example.com/m/hub\"\n\nfunc L() int { return hub.H() }\n")
	write(t, root, "leaf2/l.go", "package leaf2\n\nimport \"example.com/m/hub\"\n\nfunc L() int { return hub.H() }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "packages")
	write(t, root, "hub/hub.go", "package hub\n\nfunc H() int { return 2 }\n")
	gitDo(t, root, "add", ".")
	return root
}

func ranLines(seen []Runner) []string {
	var out []string
	for _, r := range seen {
		out = append(out, cmdLine(r))
	}
	return out
}

// #1172: the merge of a hub package change ran -race over the hub and every
// importer. It now runs -race over the hub, then the importers without it.
func TestMechanical_RacesTheChangedPackageAndRunsItsImportersPlain(t *testing.T) {
	t.Parallel()
	root := hubMergeRepo(t)

	var seen []Runner
	if res := Mechanical(root, recordRunner(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}

	want := []string{
		"go test -race -count=1 -shuffle=on ./hub",
		"go test -count=1 -shuffle=on ./leaf1 ./leaf2",
	}
	if got := ranLines(seen); !slices.Equal(got, want) {
		t.Fatalf("the merge ran %q, want %q", got, want)
	}
}

// A repo that asks for -race over everything keeps the one run it had.
func TestMechanical_RaceScopeAllRacesTheImportersToo(t *testing.T) {
	t.Parallel()
	root := hubMergeRepo(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\nrace-scope = \"all\"\n")

	var seen []Runner
	if res := Mechanical(root, recordRunner(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}

	want := []string{"go test -race -count=1 -shuffle=on ./hub ./leaf1 ./leaf2"}
	if got := ranLines(seen); !slices.Equal(got, want) {
		t.Fatalf("race-scope = all: the merge ran %q, want %q", got, want)
	}
}

// Both runs must pass, and a refusal names both: which one failed and what
// the other did.
func TestMechanical_AFailingImportersRunRefusesTheMergeAndNamesBothRuns(t *testing.T) {
	t.Parallel()
	root := hubMergeRepo(t)

	res := Mechanical(root, func(r Runner, _ string) SuiteResult {
		if isQualityRunner(r) || strings.Contains(cmdLine(r), "-race") {
			return SuiteResult{Passed: true, Output: "ok  \texample.com/m/hub\t0.01s\n"}
		}
		return SuiteResult{Passed: false, Output: "--- FAIL: TestLeaf (0.00s)\nFAIL\n"}
	})

	if !res.Blocked {
		t.Fatal("a red importers run let the merge through")
	}
	for _, want := range []string{
		"go test -race -count=1 -shuffle=on ./hub",
		"go test -count=1 -shuffle=on ./leaf1 ./leaf2",
		"1 of 2",
		"passed",
		"2 of 2",
		"refused",
	} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, res.Message)
		}
	}
}

func TestMechanical_AFailingRaceRunRefusesTheMergeAndSaysTheImportersNeverStarted(t *testing.T) {
	t.Parallel()
	root := hubMergeRepo(t)

	var seen []Runner
	res := Mechanical(root, func(r Runner, dir string) SuiteResult {
		if isQualityRunner(r) {
			return SuiteResult{Passed: true}
		}
		seen = append(seen, r)
		return SuiteResult{Passed: false, Output: "WARNING: DATA RACE\n--- FAIL: TestHub (0.00s)\nFAIL\n"}
	})

	if !res.Blocked {
		t.Fatal("a red race run let the merge through")
	}
	if len(seen) != 1 {
		t.Fatalf("%d suite runs after a red race run, want the importers run never started: %q", len(seen), ranLines(seen))
	}
	if !strings.Contains(res.Message, "not started") || !strings.Contains(res.Message, "2 of 2") {
		t.Errorf("the refusal does not say the importers run never started:\n%s", res.Message)
	}
}
