package mutation

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// More edges of the commit-time run: the real `go list` calls against a real
// module, the Windows binary name, what a run logs, a confirmation that has no
// failing test to name, and a file whose last line is the edit.

// ratchet: test_removed TestListPackageInputs_NamesTheFilesTheTestBinaryIsBuiltFrom: the coverage store keys no source content, so there is no go list of the files to read

// The test binary of a Windows box is named .exe, and elsewhere is not.
func TestBuildTestMap_TheBinaryIsNamedForThePlatform(t *testing.T) {
	for goos, wantExe := range map[string]bool{"windows": true, "linux": false} {
		tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
		root := buildFixture(t, tc)
		t.Cleanup(SetMutantsGOOSForTest(goos))
		if _, _, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, bytes.NewBuffer(nil)); err != nil {
			t.Fatal(err)
		}
		binary := valueAfter(tc.calls[0], "-o")
		if strings.HasSuffix(binary, ".exe") != wantExe {
			t.Errorf("%s: binary %s, want an .exe suffix = %v", goos, binary, wantExe)
		}
	}
}

// The run's log names each mutant it could not measure, and only those.
func TestRunCommitMutants_TheLogNamesOnlyWhatWasNotMeasured(t *testing.T) {
	root := commitRoot(t)
	scriptGo(t, killsUnderTheMutant)
	missing := commitMutant{File: "gate/absent.go", Line: 4, Col: 7, Mutation: "CONDITIONALS_BOUNDARY", Func: "Kind"}
	var log bytes.Buffer
	runs := runCommitMutants(context.Background(), root, MutantsConfig{}, planFor(nil), []commitMutant{kindMutant, missing}, 1, time.Minute, &log)
	if runs[0].Outcome.Status != "caught" || runs[1].NotMeasured == "" {
		t.Fatalf("runs = %+v, want the first caught and the second not measured", runs)
	}
	lines := strings.Split(strings.TrimSpace(log.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "gate/absent.go:4:7 CONDITIONALS_BOUNDARY NOT MEASURED") {
		t.Errorf("log = %q, want one line, for the mutant that was not measured", log.String())
	}
}

// A failure with no failing test to name (a panic, a timeout) is confirmed by
// the run it was found in, not by an empty selection that runs nothing.
func TestRunCommitMutants_AFailureNamingNoTestIsConfirmedWithTheSameSelection(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(c goCall) (int, string) {
		if c.Overlay {
			return 1, "panic: boom\nFAIL\tgate\t0.1s\n"
		}
		return 0, "ok\tgate\n"
	})
	got := runCommitOnce(t, root, planFor(mapFor("Kind", "TestKind_A")), kindMutant, time.Minute)
	if got.Outcome.Status != "caught" || s.count() != 2 {
		t.Fatalf("outcome %q after %d runs (%s), want caught after two", got.Outcome.Status, s.count(), got.NotMeasured)
	}
	if s.calls[1].Overlay || s.calls[1].Run != s.calls[0].Run || s.calls[1].Run == "" {
		t.Errorf("confirmation = %+v, want the first run's own selection %q without the overlay", s.calls[1], s.calls[0].Run)
	}
}

// ratchet: test_removed TestListTestedPackages_NamesOnlyThePackagesWithTests: the gate mutants testmap verb is gone; the commit stage builds the coverage it needs
// ratchet: test_removed TestEditAddedLines_ANewFileIsMeasuredToItsLastLine: the edit-time mutation run (gate mutants edit) is gone
