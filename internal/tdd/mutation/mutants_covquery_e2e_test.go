package mutation

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	covqueryE2ESource = "package p\n\nvar limit = 3\n\nfunc F1() int {\n\treturn 1\n}\n\nfunc F2() int {\n\treturn limit\n}\n"
	covqueryE2ETests  = "package p\n\nimport \"testing\"\n\nfunc TestT1(t *testing.T) {\n\tif F1() != 1 {\n\t\tt.Fatal(\"F1\")\n\t}\n}\n\nfunc TestT2(t *testing.T) {\n\tif F2() != 3 {\n\t\tt.Fatal(\"F2\")\n\t}\n}\n"
)

// covqueryRealGo runs go test in the module with the arguments and answers its
// verbose output, so a test can read which tests ran.
func covqueryRealGo(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", slices.Concat([]string{"test", "-count=1", "-v"}, args)...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// The selection end to end on a real toolchain: a store built by the real
// coverage path, an edit to F1 selects T1 alone and the argv the edit stage
// would run executes only T1; a var edit cannot be mapped and runs both.
func TestCoveringTests_RealModuleEditToOneFunctionRunsOnlyItsTest(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		// skip-ok: an environment probe, not a disabled assertion — the test asserts for real wherever go is installed.
		t.Skip("go is not installed")
	}
	root := makeGoRepo(t)
	pkg := filepath.Join(root, "internal", "p")
	mustWrite(t, filepath.Join(pkg, "p.go"), covqueryE2ESource)
	mustWrite(t, filepath.Join(pkg, "p_test.go"), covqueryE2ETests)
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "the package under test")

	prevExec, prevEnv, prevList := testMapExecFn, goEnvFn, goListFn
	t.Cleanup(func() { testMapExecFn, goEnvFn, goListFn = prevExec, prevEnv, prevList })
	testMapExecFn, goEnvFn, goListFn = runMutantsTool, listGoEnv, listPackageInputs

	mutants := []commitMutant{
		{File: "internal/p/p.go", Line: 6, Col: 9, Mutation: "ARITHMETIC_BASE"},
		{File: "internal/p/p.go", Line: 10, Col: 9, Mutation: "ARITHMETIC_BASE"},
	}
	res, err := ensureCoverage(context.Background(), root, MutantsConfig{}, covRequest{Dir: "internal/p", Mutants: mutants, Workers: 2}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.Measured != 2 {
		t.Fatalf("the real build measured %d tests, want 2: %+v", res.Measured, res)
	}

	edited := strings.Replace(covqueryE2ESource, "return 1", "return 1 + 0", 1)
	mustWrite(t, filepath.Join(pkg, "p.go"), edited)
	diff := DiffFuncs([]byte(covqueryE2ESource), []byte(edited), false)
	if diff.Unmappable != "" || !slices.Equal(diff.Funcs, []string{"F1"}) {
		t.Fatalf("diff = %+v, want F1", diff)
	}
	got := CoveringTests(root, "internal/p", diff.Funcs)
	if !got.Fresh || !slices.Equal(got.Tests, []string{"TestT1"}) || got.Total != 2 {
		t.Fatalf("query = %+v, want fresh with TestT1 of 2", got)
	}
	selected := withSelectedTests(Runner{Cmd: "go", Args: []string{"test", "./internal/p"}}, got.Tests, got.Total, diff.Funcs)
	out := covqueryRealGo(t, root, selected.Args[1:]...)
	if !strings.Contains(out, "--- PASS: TestT1") || strings.Contains(out, "TestT2") {
		t.Fatalf("the selected run (%v) ran:\n%s", selected.Args, out)
	}

	// A var edit changes what every test sees: it cannot be mapped, so the
	// package runs whole.
	varEdit := strings.Replace(covqueryE2ESource, "limit = 3", "limit = 3 + 0", 1)
	mustWrite(t, filepath.Join(pkg, "p.go"), varEdit)
	if diff := DiffFuncs([]byte(covqueryE2ESource), []byte(varEdit), false); diff.Unmappable == "" {
		t.Fatalf("a var edit mapped to %+v", diff)
	}
	whole := covqueryRealGo(t, root, "./internal/p")
	if !strings.Contains(whole, "--- PASS: TestT1") || !strings.Contains(whole, "--- PASS: TestT2") {
		t.Fatalf("the whole package ran:\n%s", whole)
	}
}
