package mutation

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// A real toolchain, in a temp module: package p has F1, F2 and F3 (never
// called), each test of p calls one of them, and package q has a test that
// also reaches p.F1 and one that reaches nothing of p.

const selE2ESource = "package p\n\n" +
	"func F1(a int) int {\n\tif a < 2 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n\n" +
	"func F2(a int) int {\n\tif a < 2 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n\n" +
	"func F3(a int) int {\n\tif a < 2 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n"

const (
	selE2EF1Line = 4
	selE2EF2Line = 11
	selE2EF3Line = 18
	selE2ECol    = 7
)

func selE2EMutant(line int) selMutant {
	return selMutant{File: "p/p.go", Line: line, Col: selE2ECol, Mutation: "CONDITIONALS_NEGATION"}
}

func selE2ERepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		// skip-ok: an environment probe, not a disabled assertion — the test asserts for real wherever go is installed.
		t.Skip("go is not installed; skipping the real-toolchain selection test")
	}
	if testing.Short() {
		// skip-ok: -short leaves out the tests that compile and run real test binaries.
		t.Skip("-short: this test compiles and runs real test binaries")
	}
	root := covermapRepo(t)
	mustWrite(t, filepath.Join(root, "p", "p.go"), selE2ESource)
	mustWrite(t, filepath.Join(root, "p", "p_test.go"), "package p\n\nimport \"testing\"\n\n"+
		"func TestF1(t *testing.T) {\n\tif F1(1) != 1 {\n\t\tt.Fatal(\"F1(1)\")\n\t}\n}\n\n"+
		"func TestF2(t *testing.T) {\n\tif F2(1) != 1 {\n\t\tt.Fatal(\"F2(1)\")\n\t}\n}\n")
	mustWrite(t, filepath.Join(root, "q", "q_test.go"), "package q_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/p\"\n)\n\n"+
		"func TestQReachesF1(t *testing.T) {\n\tif p.F1(1) != 1 {\n\t\tt.Fatal(\"p.F1(1)\")\n\t}\n}\n\n"+
		"func TestQUnrelated(t *testing.T) {}\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "the module")
	// This is the test that runs a real toolchain: the package's other tests
	// stand it down.
	prevExec, prevEnv := testMapExecFn, goEnvFn
	t.Cleanup(func() { testMapExecFn, goEnvFn = prevExec, prevEnv })
	testMapExecFn, goEnvFn = runMutantsTool, listGoEnv
	return root
}

func selE2ENames(stage selStage) map[string][]string {
	out := map[string][]string{}
	for _, r := range stage.Runs {
		out[r.Pkg] = r.Names
	}
	return out
}

func TestSelectE2E_AMutantInF1RunsExactlyItsTestAndTheOneInQThatReachesIt(t *testing.T) {
	root := selE2ERepo(t)
	r := newSelRunner(context.Background(), root, MutantsConfig{}, []string{"p"}, 2, io.Discard)
	defer r.close()
	if r.stats.Packages != 2 || r.stats.Extra != 1 {
		t.Errorf("coverage of %d packages, %d beyond the mutated one, want 2 and 1 (q imports p)", r.stats.Packages, r.stats.Extra)
	}

	plan := planSelection(r.sets, root, "p/p.go", selE2EF1Line)
	if plan.Full != "" || plan.NotCovered || len(plan.Stages) != 1 {
		t.Fatalf("plan for F1 = %+v", plan)
	}
	if got := selE2ENames(plan.Stages[0]); len(got) != 2 || !slices.Equal(got["p"], []string{"TestF1"}) || !slices.Equal(got["q"], []string{"TestQReachesF1"}) {
		t.Errorf("F1 runs %v, want exactly p.TestF1 and q.TestQReachesF1", got)
	}
	if plan := planSelection(r.sets, root, "p/p.go", selE2EF2Line); len(plan.Stages) != 1 || len(plan.Stages[0].Runs) != 1 || !slices.Equal(plan.Stages[0].Runs[0].Names, []string{"TestF2"}) {
		t.Errorf("F2 plan = %+v, want only p.TestF2", plan)
	}

	var mu sync.Mutex
	var mutated []string
	prev := resolveExecFn
	resolveExecFn = func(ctx context.Context, dir string, env, argv []string, log io.Writer) (int, error) {
		if slices.Contains(argv, "-overlay") {
			mu.Lock()
			mutated = append(mutated, selDescribe(argv))
			mu.Unlock()
		}
		return prev(ctx, dir, env, argv, log)
	}
	t.Cleanup(func() { resolveExecFn = prev })

	res := r.settle(context.Background(), selE2EMutant(selE2EF1Line), 5*time.Minute, t.TempDir())
	if res.Status != "caught" || res.Mode != selModeSelected {
		t.Fatalf("F1 mutant = %+v, want caught on a selection", res)
	}
	if len(mutated) == 0 || mutated[0] != "-run ^(TestF1)$ ./p" {
		t.Errorf("the mutated runs are %q, want p's TestF1 first", mutated)
	}

	if res := r.settle(context.Background(), selE2EMutant(selE2EF3Line), 5*time.Minute, t.TempDir()); res.Mode != selModeNotCovered || res.Gap.Kind != gapNotCovered || res.Status != "" {
		t.Errorf("F3 mutant = %+v, want not-covered", res)
	}
	n := len(mutated)
	if res := r.settle(context.Background(), selE2EMutant(selE2EF2Line), 5*time.Minute, t.TempDir()); res.Status != "caught" {
		t.Errorf("F2 mutant = %+v", res)
	}
	if len(mutated) <= n || mutated[n] != "-run ^(TestF2)$ ./p" {
		t.Errorf("F2 ran %q, want only p's TestF2", mutated[min(n, len(mutated)):])
	}
	if want := "1 not-covered"; !strings.Contains(r.summary(), want) {
		t.Errorf("summary %q lacks %q", r.summary(), want)
	}
}

func TestSelectE2E_AStoredIndexIsReadNotRebuiltAndAStaleOneFallsBackToTheFullSuite(t *testing.T) {
	root := selE2ERepo(t)
	first := newSelRunner(context.Background(), root, MutantsConfig{}, []string{"p"}, 2, io.Discard)
	first.close()
	if first.stats.Kept != 0 {
		t.Fatalf("a cold run read %d kept sets", first.stats.Kept)
	}
	second := newSelRunner(context.Background(), root, MutantsConfig{}, []string{"p"}, 2, io.Discard)
	defer second.close()
	if second.stats.Kept != 1 {
		t.Fatalf("the second run read %d kept sets, want 1: a fresh entry is not rebuilt", second.stats.Kept)
	}

	// The source moves after the index was read: its lines are no longer the
	// lines the index was measured on.
	moved := "package p\n\n// a new first comment line\n" + selE2ESource[len("package p\n\n"):]
	if err := os.WriteFile(filepath.Join(root, "p", "p.go"), []byte(moved), 0o600); err != nil {
		t.Fatal(err)
	}
	res, ok := second.judge(context.Background(), selE2EMutant(selE2EF1Line+1), 5*time.Minute, t.TempDir())
	if ok || res.Mode != selModeFull || res.Why != selWhyStale {
		t.Fatalf("a stale index gave %+v (selected %v), want the full suite for %s", res, ok, selWhyStale)
	}
	if want := "1 ran the full suite (1 stale-shape)"; !strings.Contains(second.summary(), want) {
		t.Errorf("summary %q lacks %q", second.summary(), want)
	}
}
