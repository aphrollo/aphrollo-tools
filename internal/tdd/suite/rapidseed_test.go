package suite

import (
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

const rapidTestSrc = "package a\n\nimport (\n\t\"testing\"\n\n\t\"pgregory.net/rapid\"\n)\n\nfunc TestP(t *testing.T) { rapid.Check(t, func(*rapid.T) {}) }\n"
const plainTestSrc = "package b\n\nimport \"testing\"\n\nfunc TestQ(t *testing.T) {}\n"

func rapidSeedTree(t *testing.T, rapidBody string) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "go.mod", "module m\n\ngo 1.26\n")
	write(t, root, "a/a_test.go", rapidBody)
	write(t, root, "b/b_test.go", plainTestSrc)
	return root
}

func seedArg(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "-rapid.seed=") {
			return a
		}
	}
	return ""
}

// -rapid.seed is a flag only rapid registers: handed to a test binary that
// does not import rapid it is "flag provided but not defined" and the package
// fails. The seed rides on the rapid packages' run alone.
func TestSplitRapidRuns_SeedsOnlyThePackagesImportingRapid(t *testing.T) {
	root := rapidSeedTree(t, rapidTestSrc)
	r := Runner{Cmd: "go", Args: []string{"test", "-count=1", "./a", "./b"}}
	runs := splitRapidRuns(r, root)
	if len(runs) != 2 {
		t.Fatalf("runs = %v, want a seeded run over ./a and a plain run over ./b", runs)
	}
	if seedArg(runs[0].Args) == "" || !strings.Contains(strings.Join(runs[0].Args, " "), "./a") || strings.Contains(strings.Join(runs[0].Args, " "), "./b") {
		t.Errorf("first run = %v, want ./a with a seed", runs[0].Args)
	}
	if seedArg(runs[1].Args) != "" || !strings.Contains(strings.Join(runs[1].Args, " "), "./b") {
		t.Errorf("second run = %v, want ./b with no seed", runs[1].Args)
	}
}

func TestSplitRapidRuns_LeavesAPlainRunAlone(t *testing.T) {
	root := rapidSeedTree(t, rapidTestSrc)
	r := Runner{Cmd: "go", Args: []string{"test", "-count=1", "./b"}}
	runs := splitRapidRuns(r, root)
	if len(runs) != 1 || strings.Join(runs[0].Args, " ") != "test -count=1 ./b" {
		t.Errorf("runs = %v, want the runner unchanged", runs)
	}
}

// A package list it cannot read back (the whole module) gets no seed: a guess
// would hand the flag to a package that cannot take it.
func TestSplitRapidRuns_WholeModuleRunGetsNoSeed(t *testing.T) {
	root := rapidSeedTree(t, rapidTestSrc)
	r := Runner{Cmd: "go", Args: []string{"test", "-count=1", "./..."}}
	runs := splitRapidRuns(r, root)
	if len(runs) != 1 || seedArg(runs[0].Args) != "" {
		t.Errorf("runs = %v, want ./... unchanged", runs)
	}
}

func TestSplitRapidRuns_OnlyGoTestIsSeeded(t *testing.T) {
	root := rapidSeedTree(t, rapidTestSrc)
	r := Runner{Cmd: "cargo", Args: []string{"test", "-p", "a"}}
	runs := splitRapidRuns(r, root)
	if len(runs) != 1 || strings.Join(runs[0].Args, " ") != "test -p a" {
		t.Errorf("runs = %v, want cargo unchanged", runs)
	}
}

// Equal trees give an equal seed (the same verdict twice); a changed tree
// gives another (a new commit tries new inputs).
func TestSplitRapidRuns_SeedFollowsTheTreeContent(t *testing.T) {
	r := Runner{Cmd: "go", Args: []string{"test", "-count=1", "./a"}}
	one := splitRapidRuns(r, rapidSeedTree(t, rapidTestSrc))[0].Args
	two := splitRapidRuns(r, rapidSeedTree(t, rapidTestSrc))[0].Args
	if seedArg(one) == "" || seedArg(one) != seedArg(two) {
		t.Errorf("equal trees: %q vs %q, want one equal non-empty seed", seedArg(one), seedArg(two))
	}
	other := splitRapidRuns(r, rapidSeedTree(t, rapidTestSrc+"// edited\n"))[0].Args
	if seedArg(other) == seedArg(one) {
		t.Errorf("an edited tree kept seed %q", seedArg(one))
	}
}

// A rapid failure names its seed in its own output; the gate must hand that
// line back, joined across the two runs, so the failure can be replayed.
func TestRunRapidSplit_KeepsTheRapidFailureLine(t *testing.T) {
	root := rapidSeedTree(t, rapidTestSrc)
	r := Runner{Cmd: "go", Args: []string{"test", "-count=1", "./a", "./b"}}
	var seen [][]string
	one := func(b Runner, _ string) SuiteResult {
		seen = append(seen, b.Args)
		if seedArg(b.Args) != "" {
			return SuiteResult{Output: "    rapid_test.go:9: [rapid] failed: x\n    To reproduce, specify -run=\"TestP\" -rapid.seed=777\n"}
		}
		return SuiteResult{Passed: true, Output: "ok b\n"}
	}
	res := runRapidSplit(r, root, time.Hour, one)
	if res.Passed {
		t.Error("a failed seeded run was reported as passed")
	}
	if !strings.Contains(res.Output, "-rapid.seed=777") {
		t.Errorf("output lost the rapid seed line: %q", res.Output)
	}
	if len(seen) != 1 {
		t.Errorf("ran %d commands, want the failed one to end the run", len(seen))
	}
}

// The gate's one executor carries the seed, so post-edit, the commit and the
// merge all run a rapid package under it; a plain `go test` never does.
func TestRunSuite_SeedsARapidPackageAndLeavesOthersPlain(t *testing.T) {
	prevWait, prevChild := waitForHeadroomFn, suiteChildFn
	t.Cleanup(func() { waitForHeadroomFn, suiteChildFn = prevWait, prevChild })
	waitForHeadroomFn = func(string, time.Duration) string { return "" }
	var argvs [][]string
	suiteChildFn = func(s run.Spec, _ MemCap) suiteChildEnd {
		argvs = append(argvs, s.Args)
		return suiteChildEnd{}
	}
	root := rapidSeedTree(t, rapidTestSrc)

	RunSuite(30*time.Second)(Runner{Cmd: "go", Args: []string{"test", "./a", "./b"}}, root)

	if len(argvs) != 2 || seedArg(argvs[0]) == "" || seedArg(argvs[1]) != "" {
		t.Errorf("child argvs = %v, want a seeded ./a run then a plain ./b run", argvs)
	}
}
