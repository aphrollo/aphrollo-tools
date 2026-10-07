package mutation

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// ensureCoverage is the store in front of the coverage build. What the
// source says is proved in mutants_covbuild_test.go; here is what is outside
// the source: the toolchain, the module, and a store that cannot be kept.

// The Go version, the module path and the module files are the build key: a
// store of another key is not used, and its tests are measured again.
func TestEnsureCoverage_AChangedBuildKeyIsMeasuredAgain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	covbuildAsk(t, root, 1, 4)
	for _, tc2 := range []struct {
		name  string
		apply func(t *testing.T)
	}{
		{"another Go version", func(t *testing.T) {
			t.Cleanup(setGoEnvForTest(func(context.Context, string) (string, error) { return "go9.99\n", nil }))
		}},
		{"another module path", func(t *testing.T) {
			mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/renamed\n\ngo 1.26\n")
		}},
		{"another mutation environment", func(t *testing.T) {}},
	} {
		before := len(tc.calls)
		tc2.apply(t)
		cfg := MutantsConfig{}
		if tc2.name == "another mutation environment" {
			cfg.Env = []string{"X=1"}
		}
		res := covbuildAskCtx(t, context.Background(), root, cfg, 1, nil, 4)
		if res.Kept != 0 || len(tc.calls) == before {
			t.Errorf("after %s: kept %d with %d new commands, want a fresh measurement", tc2.name, res.Kept, len(tc.calls)-before)
		}
	}
}

func TestEnsureCoverage_AToolchainFailureNamesThePackageAndRunsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	t.Cleanup(setGoEnvForTest(func(context.Context, string) (string, error) { return "", errors.New("go env broke") }))
	_, err := ensureCoverage(context.Background(), root, MutantsConfig{}, covRequest{Dir: "internal/p", Mutants: []commitMutant{covbuildMutant(4)}, Workers: 1}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "go env") || !strings.Contains(err.Error(), "internal/p") {
		t.Fatalf("err = %v, want go env and the package named", err)
	}
	if len(tc.calls) != 0 {
		t.Fatalf("%d commands ran after the toolchain failed", len(tc.calls))
	}
}

// A store that cannot be kept is still the answer for this commit, with the
// failure said, and the next commit measures again.
func TestEnsureCoverage_AStoreThatCannotBeKeptIsStillUsedAndTheFailureSaid(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	// A file where the cache directory belongs keeps the directory from being made.
	mustWrite(t, CoverCacheDir(root), "in the way")
	var log strings.Builder

	res, err := ensureCoverage(context.Background(), root, MutantsConfig{}, covRequest{Dir: "internal/p", Mutants: []commitMutant{covbuildMutant(4)}, Workers: 1}, &log)

	if err != nil || !res.Built || res.Measured != 1 {
		t.Fatalf("result = %+v err %v, want the measured map", res, err)
	}
	if !strings.Contains(log.String(), "not kept for the next commit") {
		t.Errorf("log = %q, want the failure to keep it said", log.String())
	}
}

// ratchet: test_removed TestEnsureTestMap_TheSecondCallReusesTheMapAndRunsNothing: the store is per test now; TestEnsureCoverage_ASecondCommitOfTheSameTreeRunsNothing proves the reuse
// ratchet: test_removed TestEnsureTestMap_AChangedKeyIsMeasuredAgain: the build key is proved by TestEnsureCoverage_AChangedBuildKeyIsMeasuredAgain, and an edited source by TestPlanCoverage_EditedFunctionInvalidatesOnlyTheTestsThatCoveredIt
// ratchet: test_removed TestEnsureTestMap_APackageWithNoTestsHasNoMapAndKeepsNone: proved by TestEnsureCoverage_APackageWithNoTestFunctionsRunsNothing
// ratchet: test_removed TestEnsureTestMap_AListingOrToolchainFailureNamesThePackage: there is no listing of the package's files; the toolchain failure is TestEnsureCoverage_AToolchainFailureNamesThePackageAndRunsNothing
// ratchet: test_removed TestEnsureTestMap_AMapThatCannotBeKeptIsStillUsedAndTheFailureSaid: TestEnsureCoverage_AStoreThatCannotBeKeptIsStillUsedAndTheFailureSaid
