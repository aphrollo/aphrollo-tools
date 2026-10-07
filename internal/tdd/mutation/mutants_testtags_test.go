package mutation

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

// mutants-test-tags names the build tags a repo's tests need to run, so code
// tested only by a tagged suite (an integration tier) is measured against it
// and not reported as a survivor of the untagged tests alone (#1218).

func TestMutantsConfig_TestTagsAreReadFromTheKey(t *testing.T) {
	t.Parallel()
	cfg, err := readModeConfig(t, "mutants-test-tags = [\"integration\", \"e2e\"]\n")
	if err != nil {
		t.Fatalf("ReadMutantsConfig: %v", err)
	}
	if want := []string{"e2e", "integration"}; !slices.Equal(cfg.TestTags, want) { // the reader sorts lists, so the key is a set
		t.Errorf("TestTags = %v, want %v", cfg.TestTags, want)
	}
	cfg, err = readModeConfig(t, "undercover = true\n")
	if err != nil || len(cfg.TestTags) != 0 {
		t.Errorf("with no key: TestTags = %v (%v), want none", cfg.TestTags, err)
	}
}

// Every `go test` of a mutant, and the check run without it, carries the tags.
func TestRunCommitMutants_EveryRunCarriesTheDeclaredTestTags(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(c goCall) (int, string) {
		if c.Overlay {
			return 1, failedGate
		}
		return 0, "ok\tgate\n"
	})
	cfg := MutantsConfig{TestTags: []string{"integration", "e2e"}}

	runs := runCommitMutants(context.Background(), root, cfg, planFor(mapFor("Kind", "TestKind_A")), []commitMutant{kindMutant}, 1, time.Minute, io.Discard)

	if len(runs) != 1 || runs[0].Outcome.Status != "caught" {
		t.Fatalf("runs = %+v, want the mutant caught", runs)
	}
	if s.count() != 2 {
		t.Fatalf("go test ran %d times, want the mutant and the check without it", s.count())
	}
	for _, c := range s.calls {
		if !slices.Contains(c.Args, "-tags=integration,e2e") {
			t.Errorf("go test %v lacks -tags=integration,e2e", c.Args)
		}
	}
}

func TestRunCommitMutants_NoDeclaredTagsAddNoFlag(t *testing.T) {
	root := commitRoot(t)
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })

	runCommitOnce(t, root, planFor(mapFor("Kind", "TestKind_A")), kindMutant, time.Minute)

	for _, c := range s.calls {
		for _, a := range c.Args {
			if strings.HasPrefix(a, "-tags") {
				t.Errorf("go test %v carries %s with no tags declared", c.Args, a)
			}
		}
	}
}

// A tagged suite that cannot run (its service is missing) fails with and
// without the mutant: the mutant is NOT MEASURED, never a survivor.
func TestRunCommitMutants_ATaggedSuiteThatCannotRunLeavesTheMutantNotMeasured(t *testing.T) {
	root := commitRoot(t)
	scriptGo(t, func(goCall) (int, string) { return 1, failedGate })
	cfg := MutantsConfig{TestTags: []string{"integration"}}

	runs := runCommitMutants(context.Background(), root, cfg, planFor(mapFor("Kind", "TestKind_A")), []commitMutant{kindMutant}, 1, time.Minute, io.Discard)

	if runs[0].Outcome.Status == "missed" || runs[0].NotMeasured == "" || runs[0].GapKind != gapUnconfirmed {
		t.Errorf("outcome %q not measured %q kind %q, want NOT MEASURED as unconfirmed", runs[0].Outcome.Status, runs[0].NotMeasured, runs[0].GapKind)
	}
}

// The coverage run compiles the test binary with the tags, so the map holds
// what the tagged tests execute.
func TestBuildTestMap_CompilesWithTheDeclaredTestTags(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)

	_, _, err := buildTestMap(context.Background(), root, MutantsConfig{TestTags: []string{"integration"}}, "internal/p", 1, io.Discard)

	if err != nil {
		t.Fatal(err)
	}
	if compile := tc.calls[0]; !slices.Contains(compile, "-tags=integration") {
		t.Errorf("compile argv %v lacks -tags=integration", compile)
	}
}

// Tags are part of the key: a map measured without them is not the map of the
// tagged build, and the other way round.
func TestEnsureTestMap_ADifferentTagSetIsMeasuredAgain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)
	ctx := context.Background()
	if _, _, _, err := ensureTestMap(ctx, root, MutantsConfig{}, "internal/p", 1, io.Discard); err != nil {
		t.Fatal(err)
	}
	before := len(tc.calls)

	_, built, cached, err := ensureTestMap(ctx, root, MutantsConfig{TestTags: []string{"integration"}}, "internal/p", 1, io.Discard)

	if err != nil || !built || cached || len(tc.calls) == before {
		t.Errorf("tagged call = built %v cached %v err %v with %d new commands, want a fresh measurement", built, cached, err, len(tc.calls)-before)
	}
	_, _, cached, err = ensureTestMap(ctx, root, MutantsConfig{TestTags: []string{"integration"}}, "internal/p", 1, io.Discard)
	if err != nil || !cached {
		t.Errorf("the same tags again = cached %v err %v, want the kept map", cached, err)
	}
}
