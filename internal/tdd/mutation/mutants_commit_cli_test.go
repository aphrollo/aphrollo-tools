package mutation

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseTestedDirs_RelativeSortedAndInsideTheRepo(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "repo")
	listing := strings.Join([]string{
		filepath.Join(root, "internal", "b"),
		"",
		filepath.Join(root, "internal", "a"),
		root,
		filepath.Join(filepath.Dir(root), "elsewhere"),
		filepath.Join(root, "internal", "a"),
	}, "\n")
	got := parseTestedDirs(root, listing)
	want := []string{".", "internal/a", "internal/b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("dirs = %v, want %v", got, want)
	}
	if got := parseTestedDirs(root, ""); len(got) != 0 {
		t.Errorf("dirs of an empty listing = %v, want none", got)
	}
	if got := parseTestedDirs(root, filepath.Join(root, "only")); len(got) != 1 || got[0] != "only" {
		t.Errorf("dirs of one package = %v, want [only]", got)
	}
}

// A directory of test data or of a law's fixtures is never a package whose
// tests the map is built for, whatever the listing says.
func TestParseTestedDirs_LeavesOutTestDataAndRatchetFixtures(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "repo")
	listing := strings.Join([]string{
		filepath.Join(root, "internal", "a"),
		filepath.Join(root, "internal", "a", "testdata", "p"),
		filepath.Join(root, ".ratchet", "fixtures", "law", "hit"),
		filepath.Join(root, ".ratchet", "fixturesx"),
		filepath.Join(root, "testdatax"),
	}, "\n")

	got := parseTestedDirs(root, listing)

	want := []string{".ratchet/fixturesx", "internal/a", "testdatax"}
	if !slices.Equal(got, want) {
		t.Errorf("dirs = %v, want %v", got, want)
	}
}

func testMapVerbFixture(t *testing.T, declare bool, tc *fakeToolchain) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(setMutantsJobsForTest(2, "pinned"))
	t.Cleanup(SetCommitHeadroomForTest(func(string, time.Duration) string { return "" }))
	root := buildFixture(t, tc)
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	if declare {
		write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = true\n")
	}
	return root
}

func TestRunMutantsTestMap_BuildsTheNamedPackages(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := testMapVerbFixture(t, true, tc)
	var out, errOut bytes.Buffer

	code := RunMutantsTestMap(root, []string{"internal/p"}, &out, &errOut)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "1 built, 0 current") {
		t.Errorf("stdout = %q, want the counts", out.String())
	}
	if _, ok := loadTestMap(root, "internal/p"); !ok {
		t.Error("the map was not kept")
	}
	out.Reset()
	if code := RunMutantsTestMap(root, []string{"internal/p"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "0 built, 1 current") {
		t.Errorf("second run: exit %d stdout %q, want 0 built, 1 current", code, out.String())
	}
}

// The verb is run by a hook in every repo of the box after every merge, so a
// repo that never declared the key is left alone without a word.
func TestRunMutantsTestMap_UndeclaredIsInert(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\n"}
	root := testMapVerbFixture(t, false, tc)
	var out, errOut bytes.Buffer
	if code := RunMutantsTestMap(root, []string{"internal/p"}, &out, &errOut); code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if out.Len() != 0 || errOut.Len() != 0 || len(tc.calls) != 0 {
		t.Errorf("an undeclared repo got output %q %q and %d commands", out.String(), errOut.String(), len(tc.calls))
	}
}

func TestRunMutantsTestMap_NoHeadroomBuildsNothing(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\n"}
	root := testMapVerbFixture(t, true, tc)
	t.Cleanup(SetCommitHeadroomForTest(func(string, time.Duration) string { return "memory headroom: 0.5 GB available" }))
	var out, errOut bytes.Buffer
	if code := RunMutantsTestMap(root, []string{"internal/p"}, &out, &errOut); code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if len(tc.calls) != 0 || !strings.Contains(out.String(), "memory headroom") {
		t.Errorf("stdout %q after %d commands, want the reason and no build", out.String(), len(tc.calls))
	}
}

func TestRunMutantsTestMap_AFailedBuildIsAnError(t *testing.T) {
	tc := &fakeToolchain{compile: func([]string) (int, error) { return 2, nil }}
	root := testMapVerbFixture(t, true, tc)
	var out, errOut bytes.Buffer
	if code := RunMutantsTestMap(root, []string{"internal/p"}, &out, &errOut); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "compiling the test binary") {
		t.Errorf("stderr = %q, want the failure", errOut.String())
	}
}

func TestRunMutantsTestMap_WithNoPackagesNamedItTakesEveryTestedOne(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := testMapVerbFixture(t, true, tc)
	prev := goTestedPackagesFn
	goTestedPackagesFn = func(context.Context, string) (string, error) {
		return filepath.Join(root, "internal", "p") + "\n", nil
	}
	t.Cleanup(func() { goTestedPackagesFn = prev })
	var out, errOut bytes.Buffer
	if code := RunMutantsTestMap(root, nil, &out, &errOut); code != 0 || !strings.Contains(out.String(), "1 built") {
		t.Errorf("exit %d stdout %q stderr %q, want 1 built", code, out.String(), errOut.String())
	}
}

func TestRunMutantsCommit_UndeclaredSaysSo(t *testing.T) {
	_, root := commitStage(t, "")
	write(t, root, "aphrollo.toml", "[aphrollo]\nundercover = true\n")
	var out, errOut bytes.Buffer
	if code := RunMutantsCommit(root, &out, &errOut); code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if !strings.Contains(errOut.String(), "mutants-at-commit") {
		t.Errorf("stderr = %q, want it to say the repo declares no mutants-at-commit", errOut.String())
	}
}

func TestRunMutantsCommit_ASurvivorIsExitOne(t *testing.T) {
	_, root := commitStage(t, "")
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = \"block\"\n")
	scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })
	var out, errOut bytes.Buffer
	var code int
	stderr := captureStderr(t, func() { code = RunMutantsCommit(root, &out, &errOut) })
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "gate/gate.go:4:7: CONDITIONALS_BOUNDARY") {
		t.Errorf("stderr = %q, want the survivor named", stderr)
	}
}

func TestRunMutantsCommit_CaughtMutantsAreExitZero(t *testing.T) {
	_, root := commitStage(t, "")
	scriptGo(t, killsUnderTheMutant)
	var out, errOut bytes.Buffer
	if code := RunMutantsCommit(root, &out, &errOut); code != 0 {
		t.Errorf("exit %d, want 0: %s", code, out.String())
	}
}
