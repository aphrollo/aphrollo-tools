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
	peak     atomic.Int32
}

func (f *selFake) exec(ctx context.Context, dir string, env []string, argv []string, log io.Writer) (int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, slices.Clone(argv))
	f.mu.Unlock()
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
	if slices.Contains(argv, "-test.list=.") {
		if f.noList[pkg] {
			return 1, nil
		}
		_, err := io.WriteString(log, strings.Join(f.tests[pkg], "\n")+"\n")
		return 0, err
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
		if c[0] == "go" && c[1] == "test" {
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
	return buildSelIndex(context.Background(), root, MutantsConfig{}, set, []string{"p"}, workers, nil, io.Discard)
}

var selUnit = selTagSet{Label: "unit"}

func TestSelTestPackages_OnlyPackagesWhoseTestBinaryLinksTheMutatedOne(t *testing.T) {
	infos := parseSelListing(selListing, "example.com/m")
	tests, deps := selTestPackages(infos, "example.com/m", []string{"p"})
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
		if c[0] == "go" && c[1] == "test" {
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
	got, listed := b.Idx.testsAt("p/p.go", 4)
	want := []selTest{{Pkg: "p", Name: "TestP1"}, {Pkg: "q", Name: "TestQ"}}
	if !listed || !slices.Equal(got, want) {
		t.Errorf("line 4 = %v, want %v: the test of q reaches p through -coverpkg", got, want)
	}
	if got, listed := b.Idx.testsAt("p/p.go", 8); !listed || len(got) != 0 {
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
		if c[0] == "go" && c[1] == "test" && !slices.Contains(c, "-tags=integration") {
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
		if c[0] == "go" && c[1] == "test" {
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

func TestBuildSelIndex_ATestThatStartsTheBinaryAgainIsADoubtAboutItsPackage(t *testing.T) {
	f := &selFake{}
	root := selBuildRepo(t, f)
	mustWrite(t, filepath.Join(root, "q", "q_test.go"),
		"package q_test\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestQ(t *testing.T) { _, _ = os.Executable() }\n")
	b := selBuildAsk(t, root, selUnit, 2)
	if b.Idx == nil || b.Idx.Doubt["q"] != selWhyReexec || len(b.Idx.Doubt) != 1 {
		t.Fatalf("index = %+v, want only q in doubt for starting the test binary again", b.Idx)
	}
}
