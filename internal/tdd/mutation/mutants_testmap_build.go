package mutation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// Building a package's test map: compile the package's test binary once with
// coverage of the package itself, run each test alone under
// -test.coverprofile, and keep which blocks each one executed. The cost is one
// compile and one short run per test, in parallel and under the memory cap
// every gate-started process is held to. A solo run per test is what gives
// line-to-tests: one run of the whole suite writes one profile that cannot say
// which test ran a block, and the compile, the expensive part, is paid once.
// It is paid in the foreground by the commit that needs the map, never ahead of it.

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
	var out, errOut bytes.Buffer
	if err := run.LightRunCtx(ctx, run.Spec{Name: "go", Args: []string{"list", "-deps", "-test", "-f", format, packagePattern(dir)}, Dir: root, Stdout: &out, Stderr: &errOut}); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// goEnvFn answers the Go version and the build settings the test binary of the
// module at root is built under: `go env` of the ones that change what is
// compiled or run. A seam so a test needs no toolchain.
var goEnvFn = listGoEnv

// setGoEnvForTest replaces the answer for one test and returns the restore.
func setGoEnvForTest(fn func(ctx context.Context, root string) (string, error)) (restore func()) {
	prev := goEnvFn
	goEnvFn = fn
	return func() { goEnvFn = prev }
}

// listGoEnv is the real answer.
func listGoEnv(ctx context.Context, root string) (string, error) {
	var out, errOut bytes.Buffer
	args := []string{"env", "GOVERSION", "GOFLAGS", "GOOS", "GOARCH", "CGO_ENABLED", "GOEXPERIMENT"}
	if err := run.LightRunCtx(ctx, run.Spec{Name: "go", Args: args, Dir: root, Stdout: &out, Stderr: &errOut}); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// modulePath is the import path of the module whose go.mod is in root, "" when
// there is none to read.
func modulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		// absence-ok: no go.mod names no module path, and the hash then rests on the files and the toolchain
		return ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// packageTestHash is the key of dir's map: the hash of what its test binary is
// built from (the files of the package and of every package it imports inside
// the module, by content), the package's import path, and the Go version and
// build settings it is compiled under. A map of any other key is not used.
func packageTestHash(ctx context.Context, root, dir string) (string, error) {
	listing, err := goListFn(ctx, root, dir)
	if err != nil {
		return "", fmt.Errorf("go list %s: %w", dir, err)
	}
	env, err := goEnvFn(ctx, root)
	if err != nil {
		return "", fmt.Errorf("go env for %s: %w", dir, err)
	}
	h := sha256.New()
	fmt.Fprintf(h, "files %s\nimport %s/%s\nenv %s\n", hashPackage(root, listing), modulePath(root), dir, strings.TrimSpace(env))
	return hex.EncodeToString(h.Sum(nil)), nil
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

	perTest := make(map[string]map[coverBlock]bool, len(names))
	var mu sync.Mutex
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				covered := soloCoverage(ctx, absDir, env, binary, filepath.Join(work, strconv.Itoa(i)+".out"), names[i])
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
	logf(log, "mutants: coverage of %s: %d tests, %d blocks, measured in %s",
		dir, len(m.Tests), len(m.Blocks), commitNowFn().Sub(start).Round(100*time.Millisecond))
	return m, true, nil
}

// soloCoverage runs one test alone and answers the blocks its profile names,
// true for the ones it executed. A test that wrote no profile names none.
func soloCoverage(ctx context.Context, absDir string, env []string, binary, profile, name string) map[coverBlock]bool {
	runCtx, cancel := context.WithTimeout(ctx, perTestTimeout)
	defer cancel()
	_, _ = testMapExecFn(runCtx, absDir, env, []string{
		binary, "-test.run=^" + name + "$", "-test.count=1", "-test.timeout=" + perTestTimeout.String(),
		"-test.coverprofile=" + profile,
	}, io.Discard)
	data, err := os.ReadFile(profile)
	if err != nil {
		return map[coverBlock]bool{}
	}
	return coveredBlocks(string(data))
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

// tail is the last lines of a command's output, for an error that has to say
// what the command said.
func tail(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	return strings.Join(lines[max(len(lines)-12, 0):], "\n")
}

// ensureTestMap answers the map of dir for the tree as it is: the kept one
// when its key matches, else one measured now and kept for the next commit to
// the same content. cached says it was kept. built is false, with no error,
// for a package that has no test files. A map that cannot be kept is still
// the answer; saving it is best effort, and what it could not do is said on
// log.
func ensureTestMap(ctx context.Context, root string, cfg MutantsConfig, dir string, workers int, log io.Writer) (m testMap, built, cached bool, err error) {
	hash, err := packageTestHash(ctx, root, dir)
	if err != nil {
		return testMap{}, false, false, err
	}
	if kept, ok := loadTestMap(root, dir, hash); ok {
		logf(log, "mutants: coverage of %s: reused, %d tests, %d blocks", dir, len(kept.Tests), len(kept.Blocks))
		return *kept, true, true, nil
	}
	m, built, err = buildTestMap(ctx, root, cfg, dir, workers, log)
	if err != nil || !built {
		return testMap{}, false, false, err
	}
	if serr := saveTestMap(root, m); serr != nil {
		logf(log, "mutants: coverage of %s not kept for the next commit: %v", dir, serr)
	}
	return m, true, false, nil
}
