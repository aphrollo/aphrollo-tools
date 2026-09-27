package mutation

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #910's traps (a) and (b), settled the way `gate mutants prove`
// settles a mutant: apply that one mutation and run the tests. A case
// condition coverage cannot count, or a survivor gremlins judged with the
// wrong package's tests, is run against every package with tests that reaches
// its line. It is refused only when it survives there — or, when the run
// cannot finish inside the gate's budget, refused as UNRESOLVED, never
// called a survivor. These run the real `go test` over a small module.

// gateKindSource classifies with a bare case condition — line 5, column 9 is
// the `>` of `n > 10`, a position Go coverage never counts.
const gateKindSource = "package gate\n\nfunc Kind(n int) string {\n\tswitch {\n\tcase n > 10:\n" +
	"\t\treturn \"big\"\n\t}\n\treturn \"small\"\n}\n\n" +
	"func Label(s string) string {\n\treturn \"#\" + s\n}\n"

// gateKindModule is that package in a module, with the given test body.
func gateKindModule(t *testing.T, testBody string) string {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, filepath.FromSlash("gate/gate.go"), gateKindSource)
	write(t, root, filepath.FromSlash("gate/gate_test.go"),
		"package gate\n\nimport \"testing\"\n\nfunc TestKind(t *testing.T) {\n"+testBody+"}\n")
	return root
}

// caseBoundary is gremlins' NOT COVERED mutant on that case condition.
func caseBoundary() MutantOutcome {
	return MutantOutcome{File: "gate/gate.go", Line: 5, Col: 9, Mutation: "CONDITIONALS_BOUNDARY",
		Name: "gate/gate.go:5:9: CONDITIONALS_BOUNDARY", Status: gremlinsNotCovered, NewLine: true}
}

func resolveOneForTest(t *testing.T, root string, m MutantOutcome) MutantOutcome {
	t.Helper()
	out := resolveGapMutants(context.Background(), root, MutantsConfig{AtMerge: true}, goReachOnce(root),
		[]MutantOutcome{m}, []int{0}, io.Discard)
	return out[0]
}

func TestResolveGapMutants_ACaseConditionATestKillsIsCaught(t *testing.T) {
	root := gateKindModule(t, "\tif Kind(10) != \"small\" || Kind(11) != \"big\" {\n\t\tt.Fatal(\"wrong kind\")\n\t}\n")

	got := resolveOneForTest(t, root, caseBoundary())

	if got.Status != "caught" {
		t.Fatalf("status = %q (%s), want caught: TestKind fails under `n >= 10`", got.Status, got.Note)
	}
	if !strings.Contains(got.Note, "gate") {
		t.Errorf("note = %q, want the package whose tests killed it named", got.Note)
	}
}

func TestResolveGapMutants_ACaseConditionNoTestObservesSurvives(t *testing.T) {
	root := gateKindModule(t, "\tif Kind(100) != \"big\" {\n\t\tt.Fatal(\"wrong kind\")\n\t}\n")

	got := resolveOneForTest(t, root, caseBoundary())

	if got.Status != "missed" {
		t.Fatalf("status = %q (%s), want missed: nothing tests the boundary at 10", got.Status, got.Note)
	}
	if !strings.Contains(got.Note, "survived") {
		t.Errorf("note = %q, want it to say the mutant survived the tests it was run against", got.Note)
	}
}

// A mutation that does not compile is not a survivor and not a kill.
func TestResolveGapMutants_AMutantThatDoesNotCompileIsUnviable(t *testing.T) {
	root := gateKindModule(t, "\t_ = Label(\"x\")\n")
	m := MutantOutcome{File: "gate/gate.go", Line: 12, Col: 13, Mutation: "ARITHMETIC_BASE",
		Name: "gate/gate.go:12:13: ARITHMETIC_BASE", Status: gremlinsNotCovered, NewLine: true}

	got := resolveOneForTest(t, root, m)

	if got.Status != "unviable" {
		t.Fatalf("status = %q (%s), want unviable: strings have no `-`", got.Status, got.Note)
	}
}

// A run that cannot finish inside the budget leaves the mutant as it was,
// saying it is unresolved — never a survivor claim nobody observed.
func TestResolveGapMutants_ARunPastTheBudgetIsUnresolvedNotASurvivor(t *testing.T) {
	root := gateKindModule(t, "\tif Kind(10) != \"small\" || Kind(11) != \"big\" {\n\t\tt.Fatal(\"wrong kind\")\n\t}\n")
	t.Cleanup(setResolveBudgetForTest(time.Nanosecond))

	got := resolveOneForTest(t, root, caseBoundary())

	if got.Status != gremlinsNotCovered {
		t.Fatalf("status = %q, want it left %q", got.Status, gremlinsNotCovered)
	}
	if !strings.HasPrefix(got.Note, "UNRESOLVED:") {
		t.Errorf("note = %q, want it marked UNRESOLVED with the reason", got.Note)
	}
}

// The mutation is applied beside the tree, never in it: a run of it leaves
// the checkout byte-identical.
func TestResolveGapMutants_LeavesTheSourceFileUntouched(t *testing.T) {
	root := gateKindModule(t, "\tif Kind(100) != \"big\" {\n\t\tt.Fatal(\"wrong kind\")\n\t}\n")

	_ = resolveOneForTest(t, root, caseBoundary())

	data, err := os.ReadFile(filepath.Join(root, "gate", "gate.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != gateKindSource {
		t.Errorf("gate.go changed under resolution:\n%s", data)
	}
}

// A mutant whose operator is not where the report says cannot be applied,
// and is unresolved rather than guessed at.
func TestResolveGapMutants_AMutantNotFoundAtItsPositionIsUnresolved(t *testing.T) {
	root := gateKindModule(t, "\t_ = Kind(1)\n")
	m := caseBoundary()
	m.Col = 10

	got := resolveOneForTest(t, root, m)

	if got.Status != gremlinsNotCovered || !strings.HasPrefix(got.Note, "UNRESOLVED:") {
		t.Fatalf("got %q / %q, want it left not covered and UNRESOLVED", got.Status, got.Note)
	}
}

func TestMutateAt_SwapsExactlyTheOperatorTheMutatorNames(t *testing.T) {
	t.Parallel()
	src := []byte("package p\n\nfunc f(a, b int) bool {\n\ta += b\n\treturn a >= b && b != 0\n}\n")
	for _, c := range []struct {
		line, col int
		mutator   string
		want      string
	}{
		{5, 11, "CONDITIONALS_BOUNDARY", "\treturn a > b && b != 0"},
		{5, 11, "CONDITIONALS_NEGATION", "\treturn a < b && b != 0"},
		{5, 16, "INVERT_LOGICAL", "\treturn a >= b || b != 0"},
		{4, 4, "INVERT_ASSIGNMENTS", "\ta -= b"},
		{4, 4, "REMOVE_SELF_ASSIGNMENTS", "\ta = b"},
	} {
		got, err := mutateAt(src, c.line, c.col, c.mutator)
		if err != nil {
			t.Errorf("%d:%d %s: %v", c.line, c.col, c.mutator, err)
			continue
		}
		if line := strings.Split(string(got), "\n")[c.line-1]; line != c.want {
			t.Errorf("%d:%d %s: line = %q, want %q", c.line, c.col, c.mutator, line, c.want)
		}
	}
	if _, err := mutateAt(src, 5, 11, "INVERT_LOGICAL"); err == nil {
		t.Error("INVERT_LOGICAL applied to `>=`, which it does not mutate")
	}
}

// gremlins already ran the mutated package's own tests over an inconclusive
// survivor, and they did not kill it: settling it runs only the packages
// outside, which are the new evidence.
func TestResolveGapMutants_AnInconclusiveMutantRunsOnlyThePackagesGremlinsDidNot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := torqueAndItsImporter(t)
	var ran []string
	prev := resolveExecFn
	resolveExecFn = func(ctx context.Context, dir string, env, argv []string, log io.Writer) (int, error) {
		for _, a := range argv {
			if strings.HasPrefix(a, "./") {
				ran = append(ran, a)
			}
		}
		return prev(ctx, dir, env, argv, log)
	}
	t.Cleanup(func() { resolveExecFn = prev })
	m := MutantOutcome{File: "torque/torque.go", Line: 5, Col: 16, Mutation: "ARITHMETIC_BASE",
		Name: "torque/torque.go:5:16: ARITHMETIC_BASE", Status: gremlinsScopeUnknown, NewLine: true}

	got := resolveOneForTest(t, root, m)

	if got.Status != "caught" {
		t.Fatalf("status = %q (%s), want caught by driveline's test", got.Status, got.Note)
	}
	if strings.Join(ran, " ") != "./driveline" {
		t.Errorf("ran the tests of %v, want ./driveline alone", ran)
	}
}

// A case condition in a package no test anywhere reaches is plain not
// covered: nothing is run, and the note says why.
func TestResolveGapMutants_ALineNoTestedPackageReachesStaysNotCovered(t *testing.T) {
	root := gateKindModule(t, "\t_ = Kind(1)\n")
	if err := os.Remove(filepath.Join(root, "gate", "gate_test.go")); err != nil {
		t.Fatal(err)
	}
	prev := resolveExecFn
	resolveExecFn = func(context.Context, string, []string, []string, io.Writer) (int, error) {
		t.Error("a test run was started for a line no package with tests reaches")
		return 0, nil
	}
	t.Cleanup(func() { resolveExecFn = prev })

	got := resolveOneForTest(t, root, caseBoundary())

	if got.Status != gremlinsNotCovered || !strings.Contains(got.Note, "no package with tests reaches gate") {
		t.Fatalf("got %q / %q, want it left not covered, saying nothing reaches gate", got.Status, got.Note)
	}
}

// Each way the settling cannot even start leaves the mutant UNRESOLVED with
// the reason, never a verdict.
func TestResolveGapMutants_ASettlingThatCannotStartIsUnresolved(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, root string) MutantOutcome
		want  string
	}{
		{"missing source", func(t *testing.T, root string) MutantOutcome {
			m := caseBoundary()
			m.File = "gate/absent.go"
			return m
		}, "the source could not be read"},
		{"unwritable overlay", func(t *testing.T, root string) MutantOutcome {
			area := measureTempDir(root)
			if err := os.MkdirAll(area, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(area, "resolve"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return caseBoundary()
		}, "the overlay could not be written"},
		{"test run that cannot spawn", func(t *testing.T, root string) MutantOutcome {
			prev := resolveExecFn
			resolveExecFn = func(context.Context, string, []string, []string, io.Writer) (int, error) {
				return 0, errors.New("exec: \"go\": executable file not found in $PATH")
			}
			t.Cleanup(func() { resolveExecFn = prev })
			return caseBoundary()
		}, "the tests of gate could not start"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := gateKindModule(t, "\t_ = Kind(1)\n")
			got := resolveOneForTest(t, root, c.setup(t, root))
			if got.Status != gremlinsNotCovered || !strings.HasPrefix(got.Note, "UNRESOLVED: "+c.want) {
				t.Errorf("got %q / %q, want it left not covered and UNRESOLVED: %s…", got.Status, got.Note, c.want)
			}
		})
	}
}

// A package with no tests of its own is settled by the packages that reach
// it, and a survivor's note names only packages that have tests to run.
func TestResolveGapMutants_APackageWithoutTestsIsSettledByItsImportersAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := torqueAndItsImporter(t)
	if err := os.Remove(filepath.Join(root, "torque", "torque_test.go")); err != nil {
		t.Fatal(err)
	}
	write(t, root, filepath.FromSlash("driveline/driveline_test.go"),
		"package driveline\n\nimport \"testing\"\n\nfunc TestDeliveredRuns(t *testing.T) {\n\t_ = Delivered(1)\n}\n")
	m := MutantOutcome{File: "torque/torque.go", Line: 5, Col: 16, Mutation: "ARITHMETIC_BASE",
		Name: "torque/torque.go:5:16: ARITHMETIC_BASE", Status: gremlinsNotCovered, NewLine: true}

	got := resolveOneForTest(t, root, m)

	want := "survived the tests of driveline, every package with tests that reaches this line"
	if got.Status != "missed" || got.Note != want {
		t.Fatalf("got %q / %q, want missed / %q", got.Status, got.Note, want)
	}
}
