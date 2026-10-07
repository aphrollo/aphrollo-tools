package mutation

import (
	"context"
	"io"
	"path/filepath"
	"slices"
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

func TestReexecsTestBinary_APackageThatStartsItsOwnBinaryIsRecognised(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"os.Args[0]":    "package p\n\nimport (\n\t\"os\"\n\t\"os/exec\"\n\t\"testing\"\n)\n\nfunc TestHelper(t *testing.T) { _ = exec.Command(os.Args[0], \"-test.run=TestChild\") }\n",
		"os.Executable": "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestHelper(t *testing.T) { exe, _ := os.Executable(); _ = exe }\n",
	} {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "p_test.go"), src)
		if !reexecsTestBinary(dir) {
			t.Errorf("%s: a test that starts its own binary was not recognised", name)
		}
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "p_test.go"), "package p\n\nimport \"testing\"\n\nfunc TestPlain(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(dir, "p.go"), "package p\n\nimport \"os\"\n\nvar _ = os.Args[0]\n")
	if reexecsTestBinary(dir) {
		t.Error("a package whose only mention of os.Args[0] is in non-test code was treated as re-executing")
	}
	if reexecsTestBinary(filepath.Join(dir, "absent")) {
		t.Error("a directory that is not there was treated as re-executing")
	}
}

// Such a package runs whole for every mutant and costs no coverage build.
func TestCommitPlans_APackageThatStartsItsOwnTestBinaryRunsWhole(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	write(t, root, "gate/gate.go", commitGateSource)
	write(t, root, "gate/gate_test.go",
		"package gate\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestKind_A(t *testing.T) { _ = os.Args[0] }\n")

	plans := commitPlans(root, []commitMutant{kindMutant}, map[string]map[int]bool{})

	if !plans["gate"].Whole {
		t.Error("the plan of a package that starts its own test binary is not whole")
	}
}

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
