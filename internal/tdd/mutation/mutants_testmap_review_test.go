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

func TestEnsureTestMap_AMapWithAnUnknownTestIsUsedOnceAndNeverKept(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tc := &fakeToolchain{list: "Test_A\nTest_Exits\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)
	var log strings.Builder

	m, built, cached, err := ensureTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, &log)

	if err != nil || !built || cached || !slices.Equal(m.Unknown, []string{"Test_Exits"}) {
		t.Fatalf("ensure = unknown %v built %v cached %v err %v, want the map with one unknown test", m.Unknown, built, cached, err)
	}
	if !strings.Contains(log.String(), "not kept") {
		t.Errorf("log = %q, want it to say the map was not kept", log.String())
	}
	before := len(tc.calls)
	if _, _, cached, _ := ensureTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard); cached || len(tc.calls) == before {
		t.Errorf("a map with an unknown test was reused (cached %v, %d new commands)", cached, len(tc.calls)-before)
	}
}

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
	mustWrite(t, filepath.Join(root, "internal", "q", "q.go"), "package q\n")
	mustWrite(t, filepath.Join(root, "internal", "q", "q_test.go"), "package q\n")
	t.Cleanup(setGoListForTest(func(_ context.Context, root, dir string) (string, error) {
		return filepath.Join(root, filepath.FromSlash(dir)) + "|x.go|x_test.go||\n", nil
	}))
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

	measureTestMaps(ctx, root, MutantsConfig{}, plans, 1, io.Discard)

	if !slices.Equal(compiled, []string{"./internal/p", "./internal/q"}) {
		t.Errorf("compiled %v, want both packages: the first one's solo runs wait for the whole budget, and its share ends before the second starts", compiled)
	}
}

// The cache key reads every input of the test binary.
func TestHashPackage_ReadsFixturesCgoAssemblyAndReplacedDependencies(t *testing.T) {
	t.Parallel()
	root, listing := hashFixture(t)
	base := hashPackage(root, listing)
	mustWrite(t, filepath.Join(root, "a", "testdata", "golden.txt"), "one")
	withFixture := hashPackage(root, listing)
	if withFixture == base {
		t.Error("a testdata file did not change the hash")
	}
	mustWrite(t, filepath.Join(root, "a", "testdata", "golden.txt"), "two")
	if hashPackage(root, listing) == withFixture {
		t.Error("an edited testdata file did not change the hash")
	}

	replaced := t.TempDir()
	mustWrite(t, filepath.Join(replaced, "r.go"), "package r\n")
	line := replaced + "|r.go|||\n"
	first := hashPackage(root, listing+line)
	mustWrite(t, filepath.Join(replaced, "r.go"), "package r // edited\n")
	if hashPackage(root, listing+line) == first {
		t.Error("an edit to a replaced dependency outside the module did not change the hash: it was named, not read")
	}

	mustWrite(t, filepath.Join(root, "a", "a.s"), "TEXT x(SB)")
	mustWrite(t, filepath.Join(root, "a", "c.go"), "package a\n")
	sListing := filepath.Join(root, "a") + "|a.go|a_test.go|||c.go|a.s\n"
	cgoFirst := hashPackage(root, sListing)
	mustWrite(t, filepath.Join(root, "a", "a.s"), "TEXT y(SB)")
	if hashPackage(root, sListing) == cgoFirst {
		t.Error("an assembly file listed in the package did not change the hash when edited")
	}
}

func TestPackageTestHash_ReadsGoModAndTheDeclaredEnv(t *testing.T) {
	root, listing := hashFixture(t)
	t.Cleanup(setGoListForTest(func(context.Context, string, string) (string, error) { return listing, nil }))
	t.Cleanup(setGoEnvForTest(func(context.Context, string) (string, error) { return "go1\n", nil }))
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.26\n")
	hash := func(cfg MutantsConfig) string {
		t.Helper()
		h, err := packageTestHash(context.Background(), root, "a", cfg)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	base := hash(MutantsConfig{})

	if hash(MutantsConfig{Env: []string{"DB=1"}}) == base {
		t.Error("a declared mutants-env did not change the key")
	}
	mustWrite(t, filepath.Join(root, "go.sum"), "example.com/x v1.0.0 h1:abc\n")
	withSum := hash(MutantsConfig{})
	if withSum == base {
		t.Error("a go.sum did not change the key")
	}
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.26\n\nrequire example.com/x v1.0.0\n")
	if hash(MutantsConfig{}) == withSum {
		t.Error("an edited go.mod did not change the key")
	}
}
