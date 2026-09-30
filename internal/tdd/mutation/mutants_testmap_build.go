package mutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Building a package's per-function test map: compile the package's test
// binary once with coverage of the package itself, run each test alone under
// -test.coverprofile, and keep which functions each one executed. The cost is
// one compile and one short run per test, in parallel and under the memory
// cap every gate-started process is held to; it is paid in the background
// after a merge and never on the commit path.

// testMapExecFn runs one command of the build: the same spawn a measurement
// uses, which holds it to the memory cap and kills its whole process tree
// when its context ends. A seam so a test proves the build without a
// toolchain.
var testMapExecFn = runMutantsTool

// goListFn answers the `go list -deps -test` listing hashPackage reads.
var goListFn = listPackageInputs

// setGoListForTest replaces the listing for one test and answers the restore.
func setGoListForTest(fn func(ctx context.Context, root, dir string) (string, error)) (restore func()) {
	prev := goListFn
	goListFn = fn
	return func() { goListFn = prev }
}

// perTestTimeout bounds one test's solo run, so one wedged test cannot hold
// the whole build.
const perTestTimeout = 10 * time.Minute

// packagePattern names a package directory the way go commands take it.
func packagePattern(dir string) string {
	if dir == "." {
		return "."
	}
	return "./" + dir
}

// listPackageInputs is the real listing: every package the test binary of dir
// is built from, standard library left out, with the files each contributes.
func listPackageInputs(ctx context.Context, root, dir string) (string, error) {
	const format = `{{if not .Standard}}{{.Dir}}|{{join .GoFiles ","}}|{{join .TestGoFiles ","}}|{{join .XTestGoFiles ","}}|{{join .EmbedFiles ","}}{{end}}`
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-test", "-f", format, packagePattern(dir))
	cmd.Dir = root
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// packageTestHash is the hash of what dir's test binary is built from.
func packageTestHash(ctx context.Context, root, dir string) (string, error) {
	listing, err := goListFn(ctx, root, dir)
	if err != nil {
		return "", fmt.Errorf("go list %s: %w", dir, err)
	}
	return hashPackage(root, listing), nil
}

// buildTestMap builds the map of dir. built is false, with no error, for a
// package that has no test files. workers bounds how many tests run at once,
// at least one. A test that fails alone, or writes no profile, still counts
// in the map for what it executed: a map is a claim about coverage, and a
// failing test covered what it ran up to the failure.
func buildTestMap(ctx context.Context, root string, cfg MutantsConfig, dir string, workers int, log io.Writer) (testMap, bool, error) {
	workers = max(workers, 1)
	start := commitNowFn()
	hash, err := packageTestHash(ctx, root, dir)
	if err != nil {
		return testMap{}, false, err
	}
	area := measureTempDir(root)
	if err := os.MkdirAll(area, 0o755); err != nil {
		return testMap{}, false, err
	}
	work, err := os.MkdirTemp(area, "testmap-")
	if err != nil {
		return testMap{}, false, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	env := measureEnv(root, cfg)
	ctx = withCapShare(ctx, workers)
	// The tests run in a copy of the checkout that has a git dir of its own,
	// never in the checkout: a test binary started in the real tree finds the
	// real repository from its working directory, and a fixture's bare git
	// call then writes to it (#1043).
	lane := RepoRoot(root)
	if lane == "" {
		return testMap{}, false, fmt.Errorf("%s is not inside a git repository to copy", root)
	}
	box, err := newProveSandbox(lane, root)
	if err != nil {
		return testMap{}, false, fmt.Errorf("a disposable copy of %s to build the test map of %s in could not be made: %v", lane, dir, err)
	}
	defer box.remove()
	defer watchProveSignals(box.remove, log)()
	boxRoot, err := box.path(lane, root)
	if err != nil {
		return testMap{}, false, err
	}
	absDir := filepath.Join(boxRoot, filepath.FromSlash(dir))

	binary := filepath.Join(work, "pkg.test")
	if mutantsGOOSFn() == "windows" {
		binary += ".exe"
	}
	pattern := packagePattern(dir)
	var out bytes.Buffer
	code, err := testMapExecFn(ctx, boxRoot, env,
		[]string{"go", "test", "-c", "-covermode=set", "-coverpkg=" + pattern, "-o", binary, pattern}, &out)
	if err != nil || code != 0 {
		return testMap{}, false, fmt.Errorf("compiling the test binary of %s exited %d (%v): %s", dir, code, err, tail(out.String()))
	}
	if _, err := os.Stat(binary); err != nil {
		return testMap{}, false, nil // go test -c wrote no binary: the package has no tests
	}

	out.Reset()
	if code, err := testMapExecFn(ctx, absDir, env, []string{binary, "-test.list=."}, &out); err != nil || code != 0 {
		return testMap{}, false, fmt.Errorf("listing the tests of %s exited %d (%v)", dir, code, err)
	}
	names := listedTests(out.String())
	spans := packageFuncSpans(absDir)

	perTest := make(map[string]map[string]bool, len(names))
	var mu sync.Mutex
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				covered := soloCoverage(ctx, absDir, env, binary, filepath.Join(work, strconv.Itoa(i)+".out"), names[i], spans)
				mu.Lock()
				perTest[names[i]] = covered
				mu.Unlock()
			}
		})
	}
feed:
	for i := range names {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return testMap{}, false, err
	}
	m := assembleTestMap(dir, hash, perTest)
	logf(log, "mutants: test map of %s: %d tests, %d functions, built in %s",
		dir, len(m.Tests), len(m.Funcs), commitNowFn().Sub(start).Round(100*time.Millisecond))
	return m, true, nil
}

// soloCoverage runs one test alone and answers the functions it executed.
func soloCoverage(ctx context.Context, absDir string, env []string, binary, profile, name string, spans map[string][]funcSpan) map[string]bool {
	runCtx, cancel := context.WithTimeout(ctx, perTestTimeout)
	defer cancel()
	_, _ = testMapExecFn(runCtx, absDir, env, []string{
		binary, "-test.run=^" + name + "$", "-test.count=1", "-test.timeout=" + perTestTimeout.String(),
		"-test.coverprofile=" + profile,
	}, io.Discard)
	data, err := os.ReadFile(profile)
	if err != nil {
		return map[string]bool{}
	}
	return coveredFuncs(string(data), spans)
}

// listedTests is the tests a `-test.list` output names: the lines the runner
// would run as tests, fuzz targets and examples, and not its benchmarks.
func listedTests(output string) []string {
	var names []string
	for line := range strings.SplitSeq(output, "\n") {
		if line = strings.TrimSpace(line); runnerTestKind(line) == kindTest {
			names = append(names, line)
		}
	}
	return names
}

// packageFuncSpans is the function spans of every non-test Go file in dir,
// keyed by file name: the profile names a block's file, this says which
// function each of its lines is in.
func packageFuncSpans(dir string) map[string][]funcSpan {
	spans := map[string][]funcSpan{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return spans
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if _, _, s := parseFuncSpans(src); s != nil {
			spans[name] = s
		}
	}
	return spans
}

// tail is the last lines of a command's output, for an error that has to say
// what the command said.
func tail(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	return strings.Join(lines[max(len(lines)-12, 0):], "\n")
}

// refreshTestMaps builds and keeps the map of each package dirs names whose
// kept map is not the tree's, and leaves the rest. built counts what it
// built, fresh what was already current. A package that fails is reported in
// the error, and the others are still done.
func refreshTestMaps(ctx context.Context, root string, cfg MutantsConfig, dirs []string, workers int, log io.Writer) (built, fresh int, err error) {
	var errs []error
	canary := watchGitWorld(root, "test-map build")
	for _, dir := range dirs {
		hash, herr := packageTestHash(ctx, root, dir)
		if herr != nil {
			errs = append(errs, herr)
			continue
		}
		if kept, ok := loadTestMap(root, dir); ok && kept.Hash == hash {
			fresh++
			continue
		}
		m, ok, berr := buildTestMap(ctx, root, cfg, dir, workers, log)
		if changes := canary.verify(log); len(changes) > 0 {
			// A build that reached the real git state is not trusted for the
			// map it made, or for any build after it.
			errs = append(errs, fmt.Errorf("the test map of %s is refused: its build changed the git state of %s (%d change(s), listed above)", dir, root, len(changes)))
			break
		}
		if berr != nil {
			errs = append(errs, berr)
			continue
		}
		if !ok {
			continue
		}
		if serr := saveTestMap(root, m); serr != nil {
			errs = append(errs, serr)
			continue
		}
		built++
	}
	return built, fresh, errors.Join(errs...)
}
