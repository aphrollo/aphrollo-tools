package mutation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Measuring what the plan names, in the foreground, within the context's
// deadline: one compile of the package's test binary with coverage of the
// package, then each test alone under -test.coverprofile, in the tests' own
// order of worth. Every finished test goes into the store at once, so a
// deadline that ends the run loses only the tests it cut off, never the ones
// done: the next commit starts from them.

// covRequest is what one package's coverage is asked for.
type covRequest struct {
	Dir string
	// Mutants are the commit's mutants in this package; the functions they sit
	// in decide which tests are measured first.
	Mutants []commitMutant
	// Workers bounds how many tests run at once, at least one.
	Workers int
	// Box is the disposable copy the build runs in, shared with the mutant
	// runs so the lane is copied once. Nil makes the build its own.
	Box *commitBox
	// FillBy is when the tests beyond the targeted ones stop being started: a
	// run that has to compile and measure anyway uses the time to spare to
	// measure more of the package, so the store converges. The zero time is no
	// fill.
	FillBy time.Time
}

// covResult is the answer: the map of the tests measured, and what it cost.
type covResult struct {
	Map testMap
	// Built is false for a package with no tests to measure.
	Built bool
	// Kept is how many tests had an entry that still held, Measured how many
	// were measured now, Unmeasured how many have none.
	Kept, Measured, Unmeasured int
	// Cut says the deadline ended the run before every test it chose was done.
	Cut bool
}

// mutantDecls is the keys of the functions and variables the mutants sit in.
func mutantDecls(scan pkgScan, mutants []commitMutant) []string {
	var keys []string
	for _, m := range mutants {
		if d, ok := scan.declAt(path.Base(m.File), m.Line); ok && !slices.Contains(keys, d.Key) {
			keys = append(keys, d.Key)
		}
	}
	slices.Sort(keys)
	return keys
}

// ensureCoverage answers the map of the request's package for the tree as it
// is: what the store holds and still holds, plus the tests measured now for
// the functions the commit changes. A package with nothing to measure runs
// nothing, not even a compile. An error is a package whose coverage could not
// be measured at all; a deadline is not one, and keeps what finished.
func ensureCoverage(ctx context.Context, root string, cfg MutantsConfig, req covRequest, log io.Writer) (covResult, error) {
	ctx = withTestTags(ctx, cfg.TestTags)
	start := commitNowFn()
	dir := req.Dir
	scan, err := scanPackage(filepath.Join(root, filepath.FromSlash(dir)), cfg.TestTags)
	if err != nil {
		return covResult{}, fmt.Errorf("reading the sources of %s: %w", dir, err)
	}
	if len(scan.TestKey) == 0 {
		return covResult{}, nil
	}
	envKey, err := coverEnvKey(ctx, root, dir, cfg)
	if err != nil {
		return covResult{}, err
	}
	st := loadCovStore(root, dir, envKey)
	plan := planCoverage(st, scan, mutantDecls(scan, req.Mutants))
	res := covResult{Built: true, Kept: len(plan.Valid)}
	// A test that starts the test binary again is never measured and joins
	// every selection, as a test that wrote no profile does.
	reexec := scan.reexecTests()
	unknown := slices.Clone(reexec)
	var absent []string
	if len(plan.Measure) > 0 {
		var measured int
		names := plan.Measure
		if !req.FillBy.IsZero() {
			names = slices.Concat(plan.Measure, plan.Fill)
		}
		var silent []string
		measured, silent, absent, res.Cut, err = runCoverage(ctx, root, cfg, req, scan, st, names, len(plan.Measure), log)
		if err != nil {
			return covResult{}, err
		}
		res.Measured = measured
		unknown = append(unknown, silent...)
		slices.Sort(unknown)
	}
	if len(plan.Measure) > 0 || plan.Reset || plan.Dropped > 0 {
		if serr := st.save(root); serr != nil {
			logf(log, "mutants: coverage of %s not kept for the next commit: %v", dir, serr)
		}
	}
	var valid []string
	for _, name := range scan.testNameList() {
		if _, ok := st.Tests[name]; ok {
			valid = append(valid, name)
		}
	}
	res.Unmeasured = len(scan.TestKey) - len(valid) - len(unknown) - len(absent)
	res.Map = st.view(scan, valid, unknown, res.Unmeasured)
	cut := ""
	if res.Cut {
		cut = ", cut off by the budget (the finished tests are kept)"
	}
	logf(log, "mutants: coverage of %s: %d tests kept, %d measured, %d not measured%s, in %s",
		dir, res.Kept, res.Measured, res.Unmeasured, cut, commitNowFn().Sub(start).Round(100*time.Millisecond))
	if len(reexec) > 0 {
		logf(log, "mutants: %d test(s) of %s start the test binary again (%s), so a child's coverage is in no profile: they are not measured and join every selection", len(reexec), dir, strings.Join(reexec[:min(len(reexec), 3)], ", "))
	}
	if silent := len(unknown) - len(reexec); silent > 0 {
		logf(log, "mutants: %d test(s) of %s wrote no profile, so it is not known what they execute and they join every selection", silent, dir)
	}
	return res, nil
}

// runCoverage compiles the package's test binary and runs the named tests
// alone, recording each finished one in st. It answers how many were
// recorded, the tests that wrote no profile (unknown) and the ones the binary
// does not have (absent, a test file the build tags leave out), and whether
// the deadline cut the run short.
func runCoverage(ctx context.Context, root string, cfg MutantsConfig, req covRequest, scan pkgScan, st *covStore,
	names []string, targeted int, log io.Writer) (measured int, unknown, absent []string, cut bool, err error) {
	dir := req.Dir
	workers := max(req.Workers, 1)
	area := measureTempDir(root)
	if err := os.MkdirAll(area, 0o755); err != nil {
		return 0, nil, nil, false, err
	}
	work, err := os.MkdirTemp(area, "testmap-")
	if err != nil {
		return 0, nil, nil, false, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	env := measureEnv(root, cfg)
	boxRoot, release, err := coverageBox(root, dir, req.Box, log)
	if err != nil {
		return 0, nil, nil, false, err
	}
	defer release()
	absDir := filepath.Join(boxRoot, filepath.FromSlash(dir))

	binary := filepath.Join(work, "pkg.test")
	if mutantsGOOSFn() == "windows" {
		binary += ".exe"
	}
	pattern := packagePattern(dir)
	var out bytes.Buffer
	// The compile is one process and takes the whole memory share; only the
	// solo runs, which are many at once, divide it.
	code, err := testMapExecFn(ctx, boxRoot, env,
		slices.Concat([]string{"go", "test", "-c"}, tagsFlag(cfg.TestTags), []string{"-covermode=set", "-coverpkg=" + pattern, "-o", binary, pattern}), &out)
	if ctx.Err() != nil {
		return 0, nil, nil, true, nil // the deadline ended the compile: nothing finished, nothing to keep
	}
	if err != nil || code != 0 {
		return 0, nil, nil, false, fmt.Errorf("compiling the test binary of %s exited %d (%v): %s", dir, code, err, tail(out.String()))
	}
	if _, err := os.Stat(binary); err != nil {
		return 0, nil, names, false, nil // go test -c wrote no binary: nothing to measure
	}
	ctx = withCapShare(ctx, workers)
	out.Reset()
	code, err = testMapExecFn(ctx, absDir, env, []string{binary, "-test.list=."}, &out)
	if ctx.Err() != nil {
		return 0, nil, nil, true, nil
	}
	if err != nil || code != 0 {
		return 0, nil, nil, false, fmt.Errorf("listing the tests of %s exited %d (%v)", dir, code, err)
	}
	listed := listedTests(out.String())
	var run []string
	for i, name := range names {
		if i >= targeted && !time.Now().Before(req.FillBy) {
			break // the fill stops with the time it was given; what it skips is not measured
		}
		if slices.Contains(listed, name) {
			run = append(run, name)
		} else {
			absent = append(absent, name)
		}
	}

	var mu sync.Mutex
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				covered, ok := soloCoverage(ctx, absDir, env, binary, filepath.Join(work, strconv.Itoa(i)+".out"), run[i])
				mu.Lock()
				switch {
				case ok:
					recordProfile(st, scan, run[i], covered)
					measured++
				case ctx.Err() == nil:
					unknown = append(unknown, run[i])
				}
				mu.Unlock()
			}
		})
	}
feed:
	for i := range run {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	slices.Sort(unknown)
	return measured, unknown, absent, ctx.Err() != nil, nil
}

// coverageBox answers the root of the copy the build runs in: the shared box
// when there is one, else a copy of its own that release removes.
func coverageBox(root, dir string, shared *commitBox, log io.Writer) (boxRoot string, release func(), err error) {
	if shared != nil {
		boxRoot, err = shared.open()
		return boxRoot, func() {}, err
	}
	lane := RepoRoot(root)
	if lane == "" {
		return "", nil, fmt.Errorf("%s is not inside a git repository to copy", root)
	}
	// The tests run in a copy of the checkout that has a git dir of its own,
	// never in the checkout: a test binary started in the real tree finds the
	// real repository from its working directory, and a fixture's bare git call
	// then writes to it (#1043).
	box, err := newProveSandbox(lane, root)
	if err != nil {
		return "", nil, fmt.Errorf("a disposable copy of %s to build the coverage of %s in could not be made: %v", lane, dir, err)
	}
	stop := watchProveSignals(box.remove, log)
	boxRoot, err = box.path(lane, root)
	if err != nil {
		stop()
		box.remove()
		return "", nil, err
	}
	return boxRoot, func() { stop(); box.remove() }, nil
}
