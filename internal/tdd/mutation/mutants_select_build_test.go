package mutation

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// selFake is a toolchain for the selection build: `go list`, `go test -c`, the
// test binary's -test.list and each solo run, answered from tables. A binary
// is known by the package it was compiled for.
type selFake struct {
	mu       sync.Mutex
	calls    [][]string
	listing  string
	tests    map[string][]string // package dir -> listed tests
	profiles map[string]string   // "dir/Test" -> cover profile
	exit     map[string]int      // "dir/Test" -> exit code
	noCover  map[string]bool     // "dir/Test" writes no profile
	build    map[string]bool     // package dir -> its compile fails
	noList   map[string]bool     // package dir -> its -test.list fails
	bins     map[string]string   // binary path -> package dir
	running  atomic.Int32
	// onSolo runs once at the start of each solo test run.
	onSolo func()
	peak   atomic.Int32
	// now is the fake clock; a probe build and a whole run of a test binary advance
	// it by buildCost and runCost, every command by step.
	now                time.Time
	buildCost, runCost time.Duration
	step               time.Duration
}

func (f *selFake) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *selFake) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

// probes is how many builds the cost decision made.
func (f *selFake) probes() (n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c[0] == "go" && c[1] == "test" && slices.Contains(c, "-overlay") {
			n++
		}
	}
	return n
}

func (f *selFake) exec(ctx context.Context, dir string, env []string, argv []string, log io.Writer) (int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, slices.Clone(argv))
	f.mu.Unlock()
	f.advance(f.step)
	switch {
	case argv[0] == "go" && argv[1] == "list":
		_, err := io.WriteString(log, f.listing)
		return 0, err
	case argv[0] == "go" && argv[1] == "test":
		pkg := strings.TrimPrefix(argv[len(argv)-1], "./")
		if f.build[pkg] {
			_, _ = io.WriteString(log, "p.go:1:1: syntax error")
			return 1, nil
		}
		if slices.Contains(argv, "-overlay") {
			f.advance(f.buildCost)
		}
		out := valueAfter(argv, "-o")
		f.mu.Lock()
		if f.bins == nil {
			f.bins = map[string]string{}
		}
		f.bins[out] = pkg
		f.mu.Unlock()
		return 0, os.WriteFile(out, []byte("binary"), 0o700)
	}
	f.mu.Lock()
	pkg := f.bins[argv[0]]
	f.mu.Unlock()
	if len(argv) == 1 {
		f.advance(f.runCost)
		return 0, nil
	}
	if slices.Contains(argv, "-test.list=.") {
		if f.noList[pkg] {
			return 1, nil
		}
		_, err := io.WriteString(log, strings.Join(f.tests[pkg], "\n")+"\n")
		return 0, err
	}
	if f.onSolo != nil {
		f.onSolo()
	}
	now := f.running.Add(1)
	defer f.running.Add(-1)
	for {
		peak := f.peak.Load()
		if now <= peak || f.peak.CompareAndSwap(peak, now) {
			break
		}
	}
	for range 200 {
		runtime.Gosched() // give a concurrent run the chance to overlap this one
	}
	name := strings.TrimSuffix(strings.TrimPrefix(prefixValue(argv, "-test.run="), "^"), "$")
	key := pkg + "/" + name
	if f.noCover[key] {
		return f.exit[key], nil
	}
	if err := os.WriteFile(prefixValue(argv, "-test.coverprofile="), []byte(f.profiles[key]), 0o600); err != nil {
		return 1, err
	}
	return f.exit[key], nil
}

func (f *selFake) compiled() (pkgs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c[0] == "go" && c[1] == "test" && !slices.Contains(c, "-overlay") {
			pkgs = append(pkgs, strings.TrimPrefix(c[len(c)-1], "./"))
		}
	}
	slices.Sort(pkgs)
	return pkgs
}

// selListing is the `go list` answer for module example.com/m: p is mutated;
// q's external test imports p; r's test imports q (so its binary links p); s's
// test imports nothing of the module; t imports p but has no test.
const selListing = "example.com/m/p||||1|0\n" +
	"example.com/m/q|example.com/m/p||||1\n" +
	"example.com/m/r||example.com/m/q|||1\n" +
	"example.com/m/s|||||1\n" +
	"example.com/m/t|example.com/m/p||||0\n"

func selBuildProfile(file string, hit bool) string {
	count := "0"
	if hit {
		count = "1"
	}
	return "mode: set\nexample.com/m/" + file + ":4.9,4.10 1 " + count + "\nexample.com/m/" + file + ":8.9,8.10 1 0\n"
}

// selBuildRepo is a repository holding the fixture packages on a fake toolchain.
func selBuildRepo(t *testing.T, f *selFake) string {
	t.Helper()
	root := covermapRepo(t)
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, "p", "p.go"), "package p\n\nfunc F1() int {\n\treturn 1\n}\n\nfunc F2() int {\n\treturn 2\n}\n")
	mustWrite(t, filepath.Join(root, "p", "p_test.go"), "package p\n\nimport \"testing\"\n\nfunc TestP1(t *testing.T) { _ = F1() }\n")
	mustWrite(t, filepath.Join(root, "q", "q_test.go"), "package q_test\n\nimport \"testing\"\n\nfunc TestQ(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(root, "r", "r_test.go"), "package r_test\n\nimport \"testing\"\n\nfunc TestR(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(root, "s", "s_test.go"), "package s_test\n\nimport \"testing\"\n\nfunc TestS(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(root, "t", "t.go"), "package t\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "the packages")
	f.listing = selListing
	if f.buildCost == 0 {
		f.buildCost, f.runCost = time.Second, 10*time.Second
	}
	f.now = time.Unix(1000, 0)
	prevNow := commitNowFn
	t.Cleanup(func() { commitNowFn = prevNow })
	commitNowFn = f.clock
	if f.tests == nil {
		f.tests = map[string][]string{"p": {"TestP1", "TestP2"}, "q": {"TestQ"}, "r": {"TestR"}, "s": {"TestS"}}
	}
	if f.profiles == nil {
		f.profiles = map[string]string{
			"p/TestP1": selBuildProfile("p/p.go", true), "p/TestP2": selBuildProfile("p/p.go", false),
			"q/TestQ": selBuildProfile("p/p.go", true), "r/TestR": selBuildProfile("p/p.go", false),
		}
	}
	prev := testMapExecFn
	t.Cleanup(func() { testMapExecFn = prev })
	testMapExecFn = f.exec
	return root
}

func selBuildAsk(t *testing.T, root string, set selTagSet, workers int) selBuild {
	t.Helper()
	return buildSelIndex(context.Background(), root, selIntegrationCfg, set, []string{"p"}, workers, nil, io.Discard)
}

var selUnit = selTagSet{Label: "unit"}

// selIntegrationCfg declares p an integration package, so the packages that import it are measured too.
var selIntegrationCfg = MutantsConfig{IntegrationPackages: []string{"p"}}

func TestSelTestPackages_OnlyPackagesWhoseTestBinaryLinksTheMutatedOne(t *testing.T) {
	infos := parseSelListing(selListing, "example.com/m")
	tests, deps := selTestPackages(infos, "example.com/m", []string{"p"}, []string{"p"})
	if !slices.Equal(tests, []string{"p", "q", "r"}) {
		t.Fatalf("test packages = %v, want p, q and r (s imports nothing of the module, t has no test)", tests)
	}
	for _, dir := range []string{"p", "q", "r"} {
		if !slices.Contains(deps, dir) {
			t.Errorf("deps %v lack %s", deps, dir)
		}
	}
	if slices.Contains(deps, "s") || slices.Contains(deps, "t") {
		t.Errorf("deps %v name a package the measurement does not read", deps)
	}
}

func TestSelWorkers_ATaggedSetRunsOneTestAtATime(t *testing.T) {
	if got := selWorkers(selTagSet{Label: "tags", Tags: []string{"integration"}}, 4); got != 1 {
		t.Errorf("tagged workers = %d, want 1 (-p 1: integration tests share a database or a port)", got)
	}
	if got := selWorkers(selUnit, 4); got != 4 {
		t.Errorf("unit workers = %d, want 4", got)
	}
	if got := selWorkers(selUnit, 0); got != 1 {
		t.Errorf("unit workers = %d for none asked, want 1", got)
	}
}

func TestBuildSelIndex_CompilesEachTestPackageWithCoverpkgAndRunsEveryTestAlone(t *testing.T) {
	f := &selFake{}
	root := selBuildRepo(t, f)
	b := selBuildAsk(t, root, selUnit, 2)
	if b.Idx == nil {
		t.Fatalf("no index: %s", b.Why)
	}
	if got := f.compiled(); !slices.Equal(got, []string{"p", "q", "r"}) {
		t.Errorf("compiled %v, want exactly p, q and r", got)
	}
	if b.Extra != 2 || b.Packages != 3 {
		t.Errorf("packages %d extra %d, want 3 and 2 (q and r beyond the mutated p)", b.Packages, b.Extra)
	}
	for _, c := range f.calls {
		if c[0] == "go" && c[1] == "test" && !slices.Contains(c, "-overlay") {
			if !slices.Contains(c, "-coverpkg=example.com/m/p") || !slices.Contains(c, "-covermode=set") || !slices.Contains(c, "-c") {
				t.Errorf("compile argv %v lacks -c, -covermode=set or -coverpkg of the mutated package", c)
			}
			for _, a := range c {
				if strings.HasPrefix(a, "-tags") {
					t.Errorf("the unit set compiled with %s", a)
				}
			}
		}
	}
	got, listed := b.Idx.testsAt("p/p.go", 4, 9)
	want := []selTest{{Pkg: "p", Name: "TestP1"}, {Pkg: "q", Name: "TestQ"}}
	if !listed || !slices.Equal(got, want) {
		t.Errorf("line 4 = %v, want %v: the test of q reaches p through -coverpkg", got, want)
	}
	if got, listed := b.Idx.testsAt("p/p.go", 8, 9); !listed || len(got) != 0 {
		t.Errorf("line 8 = %v listed %v, want listed and run by none", got, listed)
	}
	if b.Idx.stale(root, "p/p.go") != "" || len(b.Idx.Doubt) != 0 {
		t.Errorf("index = %+v", b.Idx)
	}
}

func TestBuildSelIndex_ATaggedSetCompilesWithTheTags(t *testing.T) {
	f := &selFake{}
	root := selBuildRepo(t, f)
	b := selBuildAsk(t, root, selTagSet{Label: "tags", Tags: []string{"integration"}}, 4)
	if b.Idx == nil {
		t.Fatal(b.Why)
	}
	if f.peak.Load() != 1 {
		t.Errorf("%d tests ran at once in the tagged set, want one at a time", f.peak.Load())
	}
	for _, c := range f.calls {
		if c[0] == "go" && c[1] == "test" && !slices.Contains(c, "-overlay") && !slices.Contains(c, "-tags=integration") {
			t.Errorf("compile argv %v lacks -tags=integration", c)
		}
	}
	if !slices.Equal(b.Idx.Tags, []string{"integration"}) {
		t.Errorf("index tags = %v", b.Idx.Tags)
	}
}

func TestBuildSelIndex_EveryFailureOfAPackageIsADoubtAboutThatPackageOnly(t *testing.T) {
	cases := map[string]struct {
		set  func(f *selFake)
		pkg  string
		want string
	}{
		"build":    {func(f *selFake) { f.build = map[string]bool{"q": true} }, "q", selWhyBuild},
		"list":     {func(f *selFake) { f.noList = map[string]bool{"q": true} }, "q", selWhyList},
		"failed":   {func(f *selFake) { f.exit = map[string]int{"q/TestQ": 1} }, "q", selWhyFailed},
		"profile":  {func(f *selFake) { f.noCover = map[string]bool{"q/TestQ": true} }, "q", selWhyNoProfile},
		"own test": {func(f *selFake) { f.exit = map[string]int{"p/TestP2": 1} }, "p", selWhyFailed},
		"own pkg":  {func(f *selFake) { f.build = map[string]bool{"p": true} }, "p", selWhyBuild},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := &selFake{}
			c.set(f)
			root := selBuildRepo(t, f)
			b := selBuildAsk(t, root, selUnit, 2)
			if b.Idx == nil {
				t.Fatalf("no index: %s", b.Why)
			}
			if b.Idx.Doubt[c.pkg] != c.want || len(b.Idx.Doubt) != 1 {
				t.Errorf("doubt = %v, want only %s: %s", b.Idx.Doubt, c.pkg, c.want)
			}
		})
	}
}

func TestBuildSelIndex_AFreshEntryIsNeverRebuiltAndAnEditedTreeIs(t *testing.T) {
	f := &selFake{}
	root := selBuildRepo(t, f)
	first := selBuildAsk(t, root, selUnit, 2)
	n := len(f.calls)
	second := selBuildAsk(t, root, selUnit, 2)
	if !second.Hit || second.Idx == nil || first.Hit {
		t.Fatalf("first hit %v, second hit %v, want a miss then a hit", first.Hit, second.Hit)
	}
	for _, c := range f.calls[n:] {
		if c[0] == "go" && c[1] == "test" && !slices.Contains(c, "-overlay") {
			t.Errorf("a fresh entry was rebuilt: %v", c)
		}
	}
	mustWrite(t, filepath.Join(root, "q", "q_test.go"), "package q_test\n\nimport \"testing\"\n\nfunc TestQ(t *testing.T) { t.Log(1) }\n")
	if third := selBuildAsk(t, root, selUnit, 2); third.Hit {
		t.Error("an edited test file still found the old entry")
	}
}

func TestBuildSelIndex_AListingThatFailsLeavesNoIndex(t *testing.T) {
	f := &selFake{}
	root := selBuildRepo(t, f)
	testMapExecFn = func(ctx context.Context, dir string, env []string, argv []string, log io.Writer) (int, error) {
		if argv[1] == "list" {
			return 1, nil
		}
		return f.exec(ctx, dir, env, argv, log)
	}
	if b := selBuildAsk(t, root, selUnit, 2); b.Idx != nil || b.Why != selWhyBuild {
		t.Errorf("build = %+v, want no index, build-failed", b)
	}
}

// ratchet: test_removed TestBuildSelIndex_ATestThatStartsTheBinaryAgainIsADoubtAboutItsPackage: a test that starts the binary again is no longer a doubt that runs its package whole; it joins every selection of it
func TestBuildSelIndex_ATestThatStartsTheBinaryAgainJoinsEverySelectionOfItsPackage(t *testing.T) {
	f := &selFake{}
	root := selBuildRepo(t, f)
	mustWrite(t, filepath.Join(root, "q", "q_test.go"),
		"package q_test\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestQ(t *testing.T) { _, _ = os.Executable() }\n")
	b := selBuildAsk(t, root, selUnit, 2)
	if b.Idx == nil || len(b.Idx.Doubt) != 0 || !slices.Equal(b.Idx.Always["q"], []string{"TestQ"}) || len(b.Idx.Always) != 1 {
		t.Fatalf("index = %+v, want no doubt and q's TestQ always run: what a child of it executes is in no profile", b.Idx)
	}
}

func TestBuildSelIndex_SaysHowLongTheBuildTook(t *testing.T) {
	f := &selFake{step: 5 * time.Second}
	root := selBuildRepo(t, f)
	b := selBuildAsk(t, root, selUnit, 2)
	// every command the build ran took 5s on the fake clock, and the probe and the whole run their own
	want := time.Duration(len(f.calls))*5*time.Second + f.buildCost + f.runCost
	if b.Took != want {
		t.Errorf("took %s, want %s", b.Took, want)
	}
}

func TestBuildSelIndex_NamesTheTestsThatMakeAPackageRunWhole(t *testing.T) {
	f := &selFake{exit: map[string]int{"q/TestQ": 1}}
	root := selBuildRepo(t, f)
	var log strings.Builder
	b := buildSelIndex(context.Background(), root, selIntegrationCfg, selUnit, []string{"p"}, 2, nil, &log)
	if b.Idx == nil {
		t.Fatal(b.Why)
	}
	if want := "mutants: 1 test(s) of q (unit) failed alone or wrote no profile, so the package runs whole (e.g. TestQ)"; !strings.Contains(log.String(), want) {
		t.Errorf("the log lacks %q:\n%s", want, log.String())
	}
}

// A doubt is often the box's own: a path too long, a port taken, a full disk.
// An index that names one is used for this run and measured again by the next,
// in whatever the box is then.
func TestBuildSelIndex_AnIndexWithADoubtOfTheBoxIsNotKept(t *testing.T) {
	f := &selFake{exit: map[string]int{"q/TestQ": 1}}
	root := selBuildRepo(t, f)
	if b := selBuildAsk(t, root, selUnit, 2); b.Idx == nil || len(b.Idx.Doubt) != 1 {
		t.Fatalf("first build = %+v", b)
	}
	f.exit = nil
	second := selBuildAsk(t, root, selUnit, 2)
	if second.Hit || second.Idx == nil || len(second.Idx.Doubt) != 0 {
		t.Errorf("second build = hit %v doubt %v, want it measured again, with the doubt gone", second.Hit, second.Idx.Doubt)
	}
}

// A package whose tests start the test binary again always does: that is a
// fact of the source, so the index is kept.
func TestBuildSelIndex_AnIndexWithASelfStartingTestIsKept(t *testing.T) {
	f := &selFake{}
	root := selBuildRepo(t, f)
	mustWrite(t, filepath.Join(root, "q", "q_test.go"),
		"package q_test\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestQ(t *testing.T) { _, _ = os.Executable() }\n")
	selBuildAsk(t, root, selUnit, 2)
	if second := selBuildAsk(t, root, selUnit, 2); !second.Hit {
		t.Error("an index whose only doubt is of the source was measured again")
	}
}

func TestSelTestPackages_AnImporterIsMeasuredOnlyForADeclaredPackage(t *testing.T) {
	infos := parseSelListing(selListing, "example.com/m")
	tests, deps := selTestPackages(infos, "example.com/m", []string{"p"}, nil)
	if !slices.Equal(tests, []string{"p"}) {
		t.Errorf("test packages = %v, want only p: nothing was declared in mutants-integration-packages", tests)
	}
	if slices.Contains(deps, "q") || slices.Contains(deps, "r") {
		t.Errorf("deps %v name an importer nobody asked for", deps)
	}
}

func TestBuildSelIndex_NoImporterIsCompiledUnlessThePackageIsDeclared(t *testing.T) {
	f := &selFake{}
	root := selBuildRepo(t, f)
	b := buildSelIndex(context.Background(), root, MutantsConfig{}, selUnit, []string{"p"}, 2, nil, io.Discard)
	if b.Idx == nil || b.Extra != 0 || !slices.Equal(f.compiled(), []string{"p"}) {
		t.Errorf("build = %+v, compiled %v, want p alone and no package beyond it", b, f.compiled())
	}
}

func TestBuildSelIndex_ASuiteThatRunsFasterThanOneBuildIsRunWholeAndGetsNoCoverage(t *testing.T) {
	f := &selFake{buildCost: 4 * time.Second, runCost: 2 * time.Second}
	root := selBuildRepo(t, f)
	var log strings.Builder
	b := buildSelIndex(context.Background(), root, selIntegrationCfg, selUnit, []string{"p"}, 2, nil, &log)
	if !b.Cheap["p"] || b.Idx != nil {
		t.Fatalf("build = %+v, want p cheap and no index", b)
	}
	if got := f.compiled(); len(got) != 0 {
		t.Errorf("coverage was compiled for %v though its tests run faster than a build", got)
	}
	want := "mutants: p (unit): the tests run in 2s and a build takes 4s, so each mutant runs the whole suite and no per-test coverage is built"
	if !strings.Contains(log.String(), want) {
		t.Errorf("the log lacks %q:\n%s", want, log.String())
	}
}

func TestBuildSelIndex_ASuiteThatDominatesItsBuildIsSelectedAndSaysSo(t *testing.T) {
	f := &selFake{buildCost: 4 * time.Second, runCost: 40 * time.Second}
	root := selBuildRepo(t, f)
	var log strings.Builder
	b := buildSelIndex(context.Background(), root, selIntegrationCfg, selUnit, []string{"p"}, 2, nil, &log)
	if b.Cheap["p"] || b.Idx == nil {
		t.Fatalf("build = %+v, want a selection for p", b)
	}
	want := "mutants: p (unit): the tests run in 40s and a build takes 4s, so each mutant runs the tests that execute its position"
	if !strings.Contains(log.String(), want) {
		t.Errorf("the log lacks %q:\n%s", want, log.String())
	}
}

func TestBuildSelIndex_TheCostDecisionIsKeptByContentAndNotMeasuredAgain(t *testing.T) {
	f := &selFake{buildCost: 4 * time.Second, runCost: 2 * time.Second}
	root := selBuildRepo(t, f)
	selBuildAsk(t, root, selUnit, 2)
	if f.probes() != 1 {
		t.Fatalf("%d probes, want 1", f.probes())
	}
	if b := selBuildAsk(t, root, selUnit, 2); !b.Cheap["p"] || f.probes() != 1 {
		t.Errorf("second build cheap %v after %d probes, want the kept decision and still 1", b.Cheap["p"], f.probes())
	}
	mustWrite(t, filepath.Join(root, "p", "p.go"), "package p\n\nfunc F1() int {\n\treturn 3\n}\n")
	selBuildAsk(t, root, selUnit, 2)
	if f.probes() != 2 {
		t.Errorf("%d probes after an edit, want the decision measured again", f.probes())
	}
}

func TestBuildSelIndex_SaysWhatEachPackagesCoverageCostInBuildsAndInTestRuns(t *testing.T) {
	f := &selFake{step: 2 * time.Second}
	root := selBuildRepo(t, f)
	var log strings.Builder
	buildSelIndex(context.Background(), root, selIntegrationCfg, selUnit, []string{"p"}, 1, nil, &log)
	// one command compiles, one lists, and each of the two tests runs alone: 2s each
	want := "mutants: coverage of p (unit): compiled in 2s, 2 test(s) run alone in 6s"
	if !strings.Contains(log.String(), want) {
		t.Errorf("the log lacks %q:\n%s", want, log.String())
	}
}

func TestMeasureSelPackage_LeavesNoProfileAndNoBinaryInTheWorkDir(t *testing.T) {
	f := &selFake{}
	root := selBuildRepo(t, f)
	work := t.TempDir()
	res, why := measureSelPackage(context.Background(), root, root, work, "0", selUnit, "p", []string{"example.com/m/p"}, 2, nil, "example.com/m", io.Discard)
	if why != "" || len(res.per) != 2 {
		t.Fatalf("measured %d tests, doubt %q", len(res.per), why)
	}
	left, _ := os.ReadDir(work)
	for _, e := range left {
		t.Errorf("the work dir still holds %s: a profile or a binary is disk until the run ends", e.Name())
	}
}

func TestBuildSelIndex_AFileEditedWhileMeasuringIsNotKeptAndTheIndexIsStaleAgainstIt(t *testing.T) {
	f := &selFake{}
	root := selBuildRepo(t, f)
	before := selFileHash(root, "p/p.go")
	var once sync.Once
	f.onSolo = func() {
		once.Do(func() {
			mustWrite(t, filepath.Join(root, "p", "p.go"), "package p\n\n\n// edited mid-run\nfunc F1() int {\n\treturn 1\n}\n")
		})
	}
	b := selBuildAsk(t, root, selUnit, 2)
	if b.Idx == nil {
		t.Fatal(b.Why)
	}
	if b.Idx.FileHash["p/p.go"] != before {
		t.Error("the file's hash is not the one taken with the key, before measuring")
	}
	if b.Idx.stale(root, "p/p.go") == "" {
		t.Error("the index is not stale against the file edited while it was measured")
	}
	if loadSelIndex(root, b.Idx.Key) != nil {
		t.Error("an index measured while the tree changed was kept for the next run")
	}
	f.onSolo = nil
	mustWrite(t, filepath.Join(root, "p", "p.go"), "package p\n\nfunc F1() int {\n\treturn 1\n}\n\nfunc F2() int {\n\treturn 2\n}\n")
	if again := selBuildAsk(t, root, selUnit, 2); again.Hit && again.Idx.FileHash["p/p.go"] != selFileHash(root, "p/p.go") {
		t.Error("an index measured on a tree that moved was kept and read back")
	}
}

func TestBuildSelIndex_TheSharedDependencyHashIsMadeOnceForAllTargets(t *testing.T) {
	f := &selFake{buildCost: 4 * time.Second, runCost: 2 * time.Second}
	root := selBuildRepo(t, f)
	mustWrite(t, filepath.Join(root, "q", "q.go"), "package q\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "q has a source")
	calls := 0
	prev := selContentHashFn
	t.Cleanup(func() { selContentHashFn = prev })
	selContentHashFn = func(root string, dirs []string) string { calls++; return prev(root, dirs) }
	b := buildSelIndex(context.Background(), root, selIntegrationCfg, selUnit, []string{"p", "q"}, 2, nil, io.Discard)
	if !b.Cheap["p"] || !b.Cheap["q"] {
		t.Fatalf("build = %+v, want both cheap", b)
	}
	if calls != 1 {
		t.Errorf("the dependency files were hashed %d times for two targets, want once", calls)
	}
}
