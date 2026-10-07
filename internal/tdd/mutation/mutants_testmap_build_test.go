package mutation

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeToolchain answers the commands the map build runs, so the build is
// proved without a Go toolchain: `go test -c` writes a file where -o points,
// `<binary> -test.list=.` lists the tests, and a test run writes the profile
// its name is given.
type fakeToolchain struct {
	mu       sync.Mutex
	calls    [][]string
	dirs     map[string]bool
	gitDirs  map[string]string
	envs     [][]string
	list     string
	profiles map[string]string
	compile  func(argv []string) (int, error)
	listCode int
	running  atomic.Int32
	peak     atomic.Int32
	perRun   time.Duration
}

func (f *fakeToolchain) exec(ctx context.Context, dir string, env []string, argv []string, log io.Writer) (int, error) {
	// What git answers in dir while the command would run there: the copy is
	// removed when the build ends, so this is the only time to ask.
	gitDir, _ := exec.Command("git", "-C", dir, "rev-parse", "--absolute-git-dir").Output() // stderr-ok: a dir with no repository answers nothing, which the test reads as its answer
	f.mu.Lock()
	f.calls = append(f.calls, slices.Clone(argv))
	f.envs = append(f.envs, slices.Clone(env))
	if f.dirs == nil {
		f.dirs = map[string]bool{}
		f.gitDirs = map[string]string{}
	}
	f.dirs[dir] = true
	f.gitDirs[dir] = strings.TrimSpace(string(gitDir))
	f.mu.Unlock()
	if argv[0] == "go" {
		if f.compile != nil {
			return f.compile(argv)
		}
		return 0, os.WriteFile(valueAfter(argv, "-o"), []byte("binary"), 0o700)
	}
	if slices.Contains(argv, "-test.list=.") {
		_, err := io.WriteString(log, f.list)
		return f.listCode, err
	}
	now := f.running.Add(1)
	defer f.running.Add(-1)
	for {
		peak := f.peak.Load()
		if now <= peak || f.peak.CompareAndSwap(peak, now) {
			break
		}
	}
	select {
	case <-time.After(f.perRun):
	case <-ctx.Done():
		return -1, ctx.Err()
	}
	name := strings.TrimSuffix(strings.TrimPrefix(prefixValue(argv, "-test.run="), "^"), "$")
	profile, known := f.profiles[name]
	if !known {
		return 1, nil
	}
	return 0, os.WriteFile(prefixValue(argv, "-test.coverprofile="), []byte(profile), 0o600)
}

func valueAfter(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func prefixValue(argv []string, prefix string) string {
	for _, a := range argv {
		if v, ok := strings.CutPrefix(a, prefix); ok {
			return v
		}
	}
	return ""
}

func buildFixture(t *testing.T, tc *fakeToolchain) (root string) {
	t.Helper()
	root = makeGoRepo(t)
	mustWrite(t, filepath.Join(root, "internal", "p", "p.go"),
		"package p\n\nfunc f() int {\n\treturn 1\n}\n\nfunc g() int {\n\treturn 2\n}\n")
	mustWrite(t, filepath.Join(root, "internal", "p", "p_test.go"), "package p\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "the package under test")
	prevTestMapExec := testMapExecFn
	t.Cleanup(func() { testMapExecFn = prevTestMapExec })
	testMapExecFn = tc.exec
	restoreList := setGoListForTest(func(context.Context, string, string) (string, error) {
		return filepath.Join(root, "internal", "p") + "|p.go|p_test.go||\n", nil
	})
	t.Cleanup(restoreList)
	return root
}

const (
	profileF  = "mode: set\nx/internal/p/p.go:4.9,4.10 1 1\n"
	profileFG = "mode: set\nx/internal/p/p.go:4.9,4.10 1 1\nx/internal/p/p.go:8.9,8.10 1 1\n"
)

func TestBuildTestMap_MapsEachFunctionToItsTests(t *testing.T) {
	tc := &fakeToolchain{
		list:     "Test_B\nTest_A\nBenchmarkX\n",
		profiles: map[string]string{"Test_A": profileF, "Test_B": profileFG},
	}
	root := buildFixture(t, tc)
	m, built, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 2, io.Discard)
	if err != nil || !built {
		t.Fatalf("buildTestMap = built %v, err %v", built, err)
	}
	if want := []string{"Test_A", "Test_B"}; !slices.Equal(m.Tests, want) {
		t.Errorf("Tests = %v, want %v (a benchmark is not a test)", m.Tests, want)
	}
	if got, listed := m.testsAt("p.go", 4); !listed || !slices.Equal(got, []string{"Test_A", "Test_B"}) {
		t.Errorf("testsAt(p.go, 4) = %v (listed %v), want both tests", got, listed)
	}
	if got, listed := m.testsAt("p.go", 8); !listed || !slices.Equal(got, []string{"Test_B"}) {
		t.Errorf("testsAt(p.go, 8) = %v (listed %v), want Test_B alone", got, listed)
	}
	if m.Package != "internal/p" || m.Hash == "" {
		t.Errorf("map = package %q hash %q, want internal/p and a hash", m.Package, m.Hash)
	}
}

// The compile names the package's own coverage, the binary is listed and run
// in the package's directory, and every test is run alone.
func TestBuildTestMap_CommandsRunWhereTheTestsExpectToRun(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)
	if _, _, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard); err != nil {
		t.Fatal(err)
	}
	compile := tc.calls[0]
	for _, want := range []string{"-covermode=set", "-coverpkg=./internal/p", "-c", "./internal/p"} {
		if !slices.Contains(compile, want) {
			t.Errorf("compile argv %v lacks %q", compile, want)
		}
	}
	ranInPackageDir := false
	for dir := range tc.dirs {
		ranInPackageDir = ranInPackageDir || filepath.Base(dir) == "p" && filepath.Base(filepath.Dir(dir)) == "internal"
	}
	if !ranInPackageDir {
		t.Errorf("no command ran in the package's directory; ran in %v", tc.dirs)
	}
	last := tc.calls[len(tc.calls)-1]
	if !slices.Contains(last, "-test.run=^Test_A$") || !slices.Contains(last, "-test.count=1") {
		t.Errorf("test run argv = %v, want an anchored -test.run and -test.count=1", last)
	}
}

func TestBuildTestMap_APackageWithNoTestFilesHasNoMap(t *testing.T) {
	tc := &fakeToolchain{compile: func([]string) (int, error) { return 0, nil }}
	root := buildFixture(t, tc)
	_, built, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard)
	if err != nil || built {
		t.Errorf("buildTestMap = built %v, err %v, want neither", built, err)
	}
}

func TestBuildTestMap_ACompileFailureIsAnError(t *testing.T) {
	tc := &fakeToolchain{compile: func([]string) (int, error) { return 2, nil }}
	root := buildFixture(t, tc)
	_, built, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard)
	if err == nil || built {
		t.Errorf("buildTestMap = built %v, err %v, want an error", built, err)
	}
}

// A test that fails alone still executed what it executed up to the failure.
// One that wrote no profile at all (os.Exit, log.Fatal, a panic, a kill) is not
// a test that covers nothing: it is Unknown, and the build is not failed by it.
func TestBuildTestMap_AFailingOrSilentTestDoesNotFailTheMap(t *testing.T) {
	tc := &fakeToolchain{
		list:     "Test_A\nTest_Silent\n",
		profiles: map[string]string{"Test_A": profileF},
	}
	root := buildFixture(t, tc)
	m, built, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard)
	if err != nil || !built {
		t.Fatalf("buildTestMap = built %v, err %v", built, err)
	}
	if got, _ := m.testsAt("p.go", 4); !slices.Equal(got, []string{"Test_A"}) {
		t.Errorf("testsAt(p.go, 4) = %v, want [Test_A]", got)
	}
	if !slices.Equal(m.Unknown, []string{"Test_Silent"}) || slices.Contains(m.Tests, "Test_Silent") {
		t.Errorf("Unknown = %v, Tests = %v, want the silent test unknown and not a test that covers nothing", m.Unknown, m.Tests)
	}
}

// No more tests run at once than the worker count, at both ends of it.
func TestBuildTestMap_WorkersAreBounded(t *testing.T) {
	for _, workers := range []int{1, 2, 3} {
		tc := &fakeToolchain{
			list:     "Test_A\nTest_B\nTest_C\nTest_D\nTest_E\nTest_F\n",
			profiles: map[string]string{},
			perRun:   15 * time.Millisecond,
		}
		root := buildFixture(t, tc)
		if _, _, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", workers, io.Discard); err != nil {
			t.Fatal(err)
		}
		if peak := int(tc.peak.Load()); peak > workers || peak < 1 {
			t.Errorf("workers %d: %d tests ran at once, want between 1 and %d", workers, peak, workers)
		}
	}
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{}}
	root := buildFixture(t, tc)
	if _, _, err := buildTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 0, io.Discard); err != nil {
		t.Errorf("zero workers is one worker, got error %v", err)
	}
}

func TestBuildTestMap_ACancelledContextStopsTheBuild(t *testing.T) {
	tc := &fakeToolchain{list: "Test_A\nTest_B\n", profiles: map[string]string{}, perRun: time.Minute}
	root := buildFixture(t, tc)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := buildTestMap(ctx, root, MutantsConfig{}, "internal/p", 2, io.Discard)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want the context's deadline", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the build did not stop when its context ended")
	}
}

// ratchet: test_removed TestRefreshTestMaps_RebuildsOnlyWhatIsStale: the build is asked for one package by the commit stage, and the cache in front of it is proved by TestEnsureTestMap_TheSecondCallReusesTheMapAndRunsNothing and TestEnsureTestMap_AChangedKeyIsMeasuredAgain
// ratchet: test_removed TestRefreshTestMaps_NoPackages: there is no multi-package refresh to ask for nothing
