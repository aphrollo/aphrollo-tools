package mutation

import (
	"context"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// What a cold review found: a test with no profile is unknown and not "covers
// nothing", a package that starts its own test binary again cannot be mapped,
// each package has its own share of the budget, and the cache key reads every
// input the test binary has.

// ratchet: test_removed TestEnsureTestMap_AMapWithAnUnknownTestIsUsedOnceAndNeverKept: TestEnsureCoverage_ATestThatWritesNoProfileIsUnknownAndNotKept

// What an unknown test executes is not known: it joins every selection, and no
// line is called not covered while one exists.
func TestSelectTests_UnknownTestsJoinEverySelectionAndNothingIsUncovered(t *testing.T) {
	t.Parallel()
	m := mapOf([]string{"TestA"},
		mapBlock{coverBlock{File: "p.go", From: 4, To: 5}, []int{0}},
		mapBlock{coverBlock{File: "p.go", From: 12, To: 12}, nil})
	m.Unknown = []string{"TestExits"}
	current := []string{"TestA", "TestExits"}

	got := selectTests(m, current, nil, "p.go", 4)
	if !slices.Equal(got.Names, []string{"TestA", "TestExits"}) || got.Exact || got.Uncovered {
		t.Errorf("a covered line: %+v, want both tests, inexact", got)
	}
	got = selectTests(m, current, nil, "p.go", 12)
	if got.Uncovered || !slices.Equal(got.Names, []string{"TestExits"}) || got.Exact {
		t.Errorf("a line no known test ran: %+v, want only the unknown test, inexact and not uncovered", got)
	}
}

// A test that starts its own binary again (the helper-process pattern) runs the
// code under test in a child whose coverage the parent's profile does not hold.
// Such a test is found in the source, by what its function and the helpers it
// reaches do, and a string that only says "os.Args[0]" is not one.
func TestScanPackage_TestsThatStartTheirOwnBinaryAreFoundThroughHelpers(t *testing.T) {
	t.Parallel()
	tests := "package p\n\nimport (\n\t\"os\"\n\t\"os/exec\"\n\t\"testing\"\n)\n\n" +
		"func child() *exec.Cmd { return exec.Command(os.Args[0], \"-test.run=Test_Child\") }\n" +
		"func exe() string { s, _ := os.Executable(); return s }\n" +
		"func Test_Direct(t *testing.T) { _ = exec.Command(os.Args[0]) }\n" +
		"func Test_ViaHelper(t *testing.T) { _ = child() }\n" +
		"func Test_ViaExecutable(t *testing.T) { _ = exe() }\n" +
		"func Test_Plain(t *testing.T) { _ = \"os.Args[0]\" }\n"
	scan, err := scanPackage(covscanWrite(t, map[string]string{"p.go": "package p\n", "p_test.go": tests}), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Test_Direct", "Test_ViaExecutable", "Test_ViaHelper"}
	if got := scan.reexecTests(); !slices.Equal(got, want) {
		t.Fatalf("reexec tests = %v, want %v", got, want)
	}
}

func TestScanPackage_NonTestCodeThatReadsItsArgsIsNoReexecTest(t *testing.T) {
	t.Parallel()
	src := "package p\n\nimport \"os\"\n\nfunc name() string { return os.Args[0] }\n"
	tests := "package p\n\nimport \"testing\"\n\nfunc Test_Plain(t *testing.T) { _ = 1 }\n"
	scan, _ := scanPackage(covscanWrite(t, map[string]string{"p.go": src, "p_test.go": tests}), nil)
	if got := scan.reexecTests(); len(got) != 0 {
		t.Fatalf("reexec tests = %v, want none: no test reaches the code that reads its args", got)
	}
}

// Such tests are never measured: what they execute is not in the parent's
// profile. They join every selection, as a test with no profile does, and the
// rest of the package is still mapped, so one helper-process test no longer
// sends a package of hundreds of tests to the whole-package fallback.
func TestEnsureCoverage_TestsThatStartTheirOwnBinaryJoinEverySelectionAndAreNotMeasured(t *testing.T) {
	tc := &fakeToolchain{}
	root := covbuildRepo(t, tc)
	mustWrite(t, filepath.Join(root, "internal", "p", "p_test.go"), covbuildTests+"\nfunc Test_Child(t *testing.T) { _ = os.Args[0]; _ = f() }\n")
	tc.list = "Test_A\nTest_B\nTest_C\nTest_Child\n"
	var log strings.Builder

	res, err := ensureCoverage(context.Background(), root, MutantsConfig{}, covRequest{Dir: "internal/p", Mutants: []commitMutant{covbuildMutant(4)}, Workers: 1}, &log)

	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Map.Unknown, []string{"Test_Child"}) {
		t.Fatalf("unknown = %v, want the re-executing test", res.Map.Unknown)
	}
	for _, argv := range tc.calls {
		if prefixValue(argv, "-test.run=") == "^Test_Child$" {
			t.Fatalf("the re-executing test was run for coverage: %v", argv)
		}
	}
	if res.Unmeasured != 2 {
		t.Fatalf("unmeasured = %d, want Test_B and Test_C only", res.Unmeasured)
	}
	if !strings.Contains(log.String(), "start the test binary again") {
		t.Errorf("log = %q, want the re-executing tests named", log.String())
	}
}

// Such a package is mapped like any other: the plan is not whole.
func TestCommitPlans_APackageThatStartsItsOwnTestBinaryIsStillMapped(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	write(t, root, "gate/gate.go", commitGateSource)
	write(t, root, "gate/gate_test.go",
		"package gate\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestKind_A(t *testing.T) { _ = os.Args[0] }\n")

	plans := commitPlans(root, []commitMutant{kindMutant}, map[string]map[int]bool{})

	if plans["gate"].Whole {
		t.Error("the plan of a package that starts its own test binary is whole: its other tests can still be mapped")
	}
}

// ratchet: test_removed TestReexecsTestBinary_APackageThatStartsItsOwnBinaryIsRecognised: a substring search of the test files named a string in a test as a re-exec; the scan reads the functions (TestScanPackage_TestsThatStartTheirOwnBinaryAreFoundThroughHelpers)
// ratchet: test_removed TestCommitPlans_APackageThatStartsItsOwnTestBinaryRunsWhole: TestCommitPlans_APackageThatStartsItsOwnTestBinaryIsStillMapped

// The first package cannot spend the budget and leave the next none: each gets
// an equal share of what is left.
func TestMeasureTestMaps_EachPackageGetsItsOwnShareOfTheBudget(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}, perRun: time.Minute}
	root := buildFixture(t, tc)
	mustWrite(t, filepath.Join(root, "internal", "q", "q.go"), "package q\n\nfunc f() int {\n\treturn 1\n}\n")
	mustWrite(t, filepath.Join(root, "internal", "q", "q_test.go"), "package q\n\nimport \"testing\"\n\nfunc Test_A(t *testing.T) { _ = f() }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "a second package")
	var compiled []string
	prev := testMapExecFn
	testMapExecFn = func(ctx context.Context, dir string, env, argv []string, log io.Writer) (int, error) {
		if argv[0] == "go" {
			if ctx.Err() != nil {
				return -1, ctx.Err() // a compile started with no time left
			}
			compiled = append(compiled, argv[len(argv)-1])
		}
		return prev(ctx, dir, env, argv, log)
	}
	t.Cleanup(func() { testMapExecFn = prev })
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	plans := map[string]*commitPlan{"internal/p": {Dir: "internal/p"}, "internal/q": {Dir: "internal/q"}}

	mutants := []commitMutant{{File: "internal/p/p.go", Line: 4}, {File: "internal/q/q.go", Line: 4}}
	measureTestMaps(ctx, root, MutantsConfig{}, plans, mutants, 1, newCommitBoxes(root, 1, 2), io.Discard)

	if !slices.Equal(compiled, []string{"./internal/p", "./internal/q"}) {
		t.Errorf("compiled %v, want both packages: the first one's solo runs wait for the whole budget, and its share ends before the second starts", compiled)
	}
}

// The build key reads the module files and the declared environment.
func TestCoverEnvKey_ReadsGoModAndTheDeclaredEnv(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(setGoEnvForTest(func(context.Context, string) (string, error) { return "go1\n", nil }))
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.26\n")
	key := func(cfg MutantsConfig) string {
		t.Helper()
		k, err := coverEnvKey(context.Background(), root, "a", cfg)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	base := key(MutantsConfig{})

	if key(MutantsConfig{Env: []string{"DB=1"}}) == base {
		t.Error("a declared mutants-env did not change the key")
	}
	mustWrite(t, filepath.Join(root, "go.sum"), "example.com/x v1.0.0 h1:abc\n")
	withSum := key(MutantsConfig{})
	if withSum == base {
		t.Error("a go.sum did not change the key")
	}
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.26\n\nrequire example.com/x v1.0.0\n")
	if key(MutantsConfig{}) == withSum {
		t.Error("an edited go.mod did not change the key")
	}
	if key(MutantsConfig{TestTags: nil}) != key(MutantsConfig{}) {
		t.Error("the key is not stable")
	}
}

func TestCoverEnvKey_NamesNoSourceContent(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(setGoEnvForTest(func(context.Context, string) (string, error) { return "go1\n", nil }))
	mustWrite(t, filepath.Join(root, "a", "a.go"), "package a\n")
	before, _ := coverEnvKey(context.Background(), root, "a", MutantsConfig{})
	mustWrite(t, filepath.Join(root, "a", "a.go"), "package a\n\nfunc F() {}\n")
	after, _ := coverEnvKey(context.Background(), root, "a", MutantsConfig{})
	if before != after {
		t.Error("a source edit changed the build key: edits are the function hashes' to say, not the key's")
	}
}

// ratchet: test_removed TestHashPackage_ReadsFixturesCgoAssemblyAndReplacedDependencies: the store keys no package content, so there is no package hash to read it into
// ratchet: test_removed TestPackageTestHash_ReadsGoModAndTheDeclaredEnv: TestCoverEnvKey_ReadsGoModAndTheDeclaredEnv
