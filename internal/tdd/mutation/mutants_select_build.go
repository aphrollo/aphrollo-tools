package mutation

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Building the selection index of one tag set, on the spot, in the foreground,
// by the run that needs it: `go list` names the packages whose test binary
// links a package under mutation (the only ones whose tests can reach it), each
// of them is compiled once with coverage of the packages under mutation
// (-coverpkg), its tests are listed, and each test is run alone under a cover
// profile. A package that cannot be measured completely is a doubt about that
// package, never a silent gap in the index. The index is kept by content
// (mutants_select_store.go): an entry that still holds is never rebuilt.

// selTagSet is one set of build tags the tests are run under: the unit set has
// none, a mutants-test-tags set has the repo's.
type selTagSet struct {
	Label string
	Tags  []string
}

// selWorkers is how many tests of the set run at once: a tagged set runs one
// at a time, as `-p 1` would, since its tests share what the tags unlock (a
// database, a port, a directory).
func selWorkers(set selTagSet, workers int) int {
	if len(set.Tags) > 0 {
		return 1
	}
	return max(workers, 1)
}

// selBuild is the answer for one tag set.
type selBuild struct {
	// Idx is the index, nil with Why set when it could not be had at all.
	Idx *selIndex
	Why string
	// Packages is how many packages' tests the index covers, Extra how many of
	// them are not themselves under mutation.
	Packages, Extra int
	// Hit says the index was read from the store and nothing was built.
	Hit  bool
	Took time.Duration
}

// selPkgInfo is one module package of the listing.
type selPkgInfo struct {
	Path    string
	Imports []string
	// TestImports are what its tests import, in-package and external.
	TestImports []string
	HasTests    bool
}

// selListFormat is the `go list` template parseSelListing reads.
const selListFormat = `{{.ImportPath}}|{{join .Imports " "}}|{{join .TestImports " "}}|{{join .XTestImports " "}}|{{len .TestGoFiles}}|{{len .XTestGoFiles}}`

// parseSelListing reads the listing by import path. A package outside the
// module is left out.
func parseSelListing(out, module string) map[string]selPkgInfo {
	infos := map[string]selPkgInfo{}
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "|")
		if len(f) != 6 || f[0] == "" {
			continue
		}
		if f[0] != module && !strings.HasPrefix(f[0], module+"/") {
			continue
		}
		tests, _ := strconv.Atoi(f[4])
		xtests, _ := strconv.Atoi(f[5])
		infos[f[0]] = selPkgInfo{
			Path: f[0], Imports: strings.Fields(f[1]),
			TestImports: slices.Concat(strings.Fields(f[2]), strings.Fields(f[3])),
			HasTests:    tests+xtests > 0,
		}
	}
	return infos
}

// selDir is a module package's directory, repo-relative, "." for the root.
func selDir(module, imp string) string {
	if imp == module {
		return "."
	}
	return strings.TrimPrefix(imp, module+"/")
}

// selImportPath is the import path of a package directory.
func selImportPath(module, dir string) string {
	if dir == "." {
		return module
	}
	return module + "/" + dir
}

// selTestPackages is the directories of the packages whose test binary links
// one of the targets (the target itself if it has tests), sorted, and every
// module directory those binaries are built from, which is what the index is
// keyed by.
func selTestPackages(infos map[string]selPkgInfo, module string, targets []string) (tests, deps []string) {
	want := map[string]bool{}
	for _, dir := range targets {
		want[selImportPath(module, dir)] = true
	}
	depSet := map[string]bool{}
	for _, info := range infos {
		if !info.HasTests {
			continue
		}
		seen := map[string]bool{}
		var visit func(path string)
		visit = func(path string) {
			if seen[path] {
				return
			}
			seen[path] = true
			for _, imp := range infos[path].Imports {
				visit(imp)
			}
		}
		visit(info.Path)
		for _, imp := range info.TestImports {
			visit(imp)
		}
		linked := false
		for path := range want {
			linked = linked || seen[path]
		}
		if !linked {
			continue
		}
		tests = append(tests, selDir(module, info.Path))
		for path := range seen {
			if _, ok := infos[path]; ok {
				depSet[selDir(module, path)] = true
			}
		}
	}
	slices.Sort(tests)
	for dir := range depSet {
		deps = append(deps, dir)
	}
	slices.Sort(deps)
	return tests, deps
}

// selWhyCut is the reason of an index the deadline ended before it was done.
const selWhyCut = "cut-off"

// buildSelIndex answers the index of one tag set for the packages under
// mutation: the kept one if its content key still holds, else one built now.
// box is the disposable copy the build runs in, shared with the mutant runs;
// nil makes the build its own.
func buildSelIndex(ctx context.Context, root string, cfg MutantsConfig, set selTagSet, targets []string, workers int, box *commitBox, log io.Writer) (b selBuild) {
	start := commitNowFn()
	defer func() { b.Took = commitNowFn().Sub(start) }()
	module := modulePath(root)
	if module == "" {
		b.Why = selWhyBuild
		return b
	}
	env := measureEnv(root, cfg)
	var listing bytes.Buffer
	code, err := testMapExecFn(ctx, root, env, slices.Concat([]string{"go", "list"}, tagsFlag(set.Tags), []string{"-f", selListFormat, "./..."}), &listing)
	if ctx.Err() != nil {
		b.Why = selWhyCut
		return b
	}
	if err != nil || code != 0 {
		logf(log, "mutants: the packages of %s could not be listed (%v, exit %d): %s", set.Label, err, code, tail(listing.String()))
		b.Why = selWhyBuild
		return b
	}
	tests, deps := selTestPackages(parseSelListing(listing.String(), module), module, targets)
	key, err := selKey(ctx, root, cfg, set.Tags, targets, deps)
	if err != nil {
		logf(log, "mutants: the coverage key of %s could not be made: %v", set.Label, err)
		b.Why = selWhyBuild
		return b
	}
	if idx := loadSelIndex(root, key); idx != nil {
		b.Idx, b.Hit = idx, true
		b.Packages, b.Extra = len(idx.Pkgs), selExtra(idx.Pkgs, targets)
		return b
	}
	idx, why := measureSelIndex(ctx, root, cfg, set, targets, tests, workers, box, env, log)
	if idx == nil {
		b.Why = why
		return b
	}
	idx.Key = key
	for file := range idx.Files {
		idx.FileHash[file] = selFileHash(root, file)
	}
	if why := idx.boxDoubt(); why != "" {
		logf(log, "mutants: the coverage of %s is not kept: %s", set.Label, why)
	} else if err := idx.save(root); err != nil {
		logf(log, "mutants: the coverage of %s is not kept for the next run: %v", set.Label, err)
	}
	b.Idx, b.Packages, b.Extra = idx, len(idx.Pkgs), selExtra(idx.Pkgs, targets)
	return b
}

// selExtra is how many of the measured packages are not under mutation.
func selExtra(pkgs, targets []string) int {
	extra := 0
	for _, p := range pkgs {
		if !slices.Contains(targets, p) {
			extra++
		}
	}
	return extra
}

// measureSelIndex compiles and runs the tests of each package and assembles
// the index. A package that fails to build or list, or has a test that fails
// alone, writes no profile or starts the binary again, is a doubt.
func measureSelIndex(ctx context.Context, root string, cfg MutantsConfig, set selTagSet, targets, pkgs []string,
	workers int, shared *commitBox, env []string, log io.Writer) (*selIndex, string) {
	module := modulePath(root)
	area := measureTempDir(root)
	if err := os.MkdirAll(area, 0o755); err != nil {
		logf(log, "mutants: the coverage work area %s could not be made: %v", area, err)
		return nil, selWhyBuild
	}
	work, err := os.MkdirTemp(area, "select-")
	if err != nil {
		logf(log, "mutants: the coverage work area could not be made: %v", err)
		return nil, selWhyBuild
	}
	defer func() { _ = os.RemoveAll(work) }()
	boxRoot, release, err := coverageBox(root, strings.Join(targets, ", "), shared, log)
	if err != nil {
		logf(log, "mutants: the copy to measure coverage in could not be made: %v", err)
		return nil, selWhyBuild
	}
	defer release()
	coverpkg := make([]string, len(targets))
	for i, dir := range targets {
		coverpkg[i] = selImportPath(module, dir)
	}
	idx := &selIndex{Schema: selSchema, Tags: slices.Clone(set.Tags), Doubt: map[string]string{}, Always: map[string][]string{}, PkgTests: map[string]int{}, FileHash: map[string]string{}, Pkgs: pkgs}
	per := map[selTest]map[selBlockKey]bool{}
	for n, dir := range pkgs {
		blocks, why := measureSelPackage(ctx, root, boxRoot, work, strconv.Itoa(n), set, dir, coverpkg, selWorkers(set, workers), env, module, log)
		if ctx.Err() != nil {
			return nil, selWhyCut
		}
		idx.PkgTests[dir] = len(blocks.names)
		if why != "" {
			idx.Doubt[dir] = why
		}
		if len(blocks.always) > 0 {
			idx.Always[dir] = blocks.always
		}
		for name, covered := range blocks.per {
			per[selTest{Pkg: dir, Name: name}] = covered
		}
	}
	built := assembleSelIndex(per)
	idx.Tests, idx.Files = built.Tests, built.Files
	return idx, ""
}

// selMeasured is what one package's tests executed.
type selMeasured struct {
	names []string
	// always are the tests that start the test binary again.
	always []string
	per    map[string]map[selBlockKey]bool
}

// measureSelPackage compiles the test binary of dir with coverage of coverpkg
// and runs each of its tests alone, answering the blocks each executed and, when
// the package cannot be trusted whole, why.
func measureSelPackage(ctx context.Context, root, boxRoot, work, id string, set selTagSet, dir string, coverpkg []string,
	workers int, env []string, module string, log io.Writer) (selMeasured, string) {
	res := selMeasured{per: map[string]map[selBlockKey]bool{}}
	binary := filepath.Join(work, id+".test")
	if mutantsGOOSFn() == "windows" {
		binary += ".exe"
	}
	var out bytes.Buffer
	code, err := testMapExecFn(ctx, boxRoot, env, slices.Concat([]string{"go", "test", "-c"}, tagsFlag(set.Tags),
		[]string{"-vet=off", "-covermode=set", "-coverpkg=" + strings.Join(coverpkg, ","), "-o", binary, packagePattern(dir)}), &out)
	if ctx.Err() != nil {
		return res, ""
	}
	if err != nil || code != 0 {
		logf(log, "mutants: the tests of %s (%s) did not compile for coverage: %s", dir, set.Label, tail(out.String()))
		return res, selWhyBuild
	}
	if _, err := os.Stat(binary); err != nil {
		return res, selWhyBuild
	}
	scan, scanErr := scanPackage(filepath.Join(root, filepath.FromSlash(dir)), set.Tags)
	absDir := filepath.Join(boxRoot, filepath.FromSlash(dir))
	ctx = withCapShare(ctx, workers)
	out.Reset()
	code, err = testMapExecFn(ctx, absDir, env, []string{binary, "-test.list=."}, &out)
	if ctx.Err() != nil {
		return res, ""
	}
	if err != nil || code != 0 {
		logf(log, "mutants: the tests of %s (%s) could not be listed (exit %d)", dir, set.Label, code)
		return res, selWhyList
	}
	res.names = listedTests(out.String())
	why := ""
	var unsure []string
	var mu sync.Mutex
	doubt := func(w string) {
		mu.Lock()
		defer mu.Unlock()
		if why == "" {
			why = w
		}
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				name := res.names[i]
				covered, w := soloSelCoverage(ctx, absDir, env, binary, filepath.Join(work, id+"-"+strconv.Itoa(i)+".out"), name, module)
				if w != "" {
					doubt(w)
					mu.Lock()
					unsure = append(unsure, name)
					mu.Unlock()
					continue
				}
				mu.Lock()
				res.per[name] = covered
				mu.Unlock()
			}
		})
	}
feed:
	for i := range res.names {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if len(unsure) > 0 {
		slices.Sort(unsure)
		logf(log, "mutants: %d test(s) of %s (%s) failed alone or wrote no profile, so the package runs whole (e.g. %s)", len(unsure), dir, set.Label, strings.Join(unsure[:min(len(unsure), 3)], ", "))
	}
	if scanErr != nil {
		doubt(selWhyBuild)
	} else {
		res.always = scan.reexecTests()
	}
	return res, why
}

// soloSelCoverage runs one test alone and answers the blocks its profile
// names, or the doubt that follows: it failed alone, or it wrote no profile.
func soloSelCoverage(ctx context.Context, absDir string, env []string, binary, profile, name, module string) (map[selBlockKey]bool, string) {
	runCtx, cancel := context.WithTimeout(ctx, perTestTimeout)
	defer cancel()
	code, err := testMapExecFn(runCtx, absDir, env, []string{
		binary, "-test.run=^" + name + "$", "-test.count=1", "-test.timeout=" + perTestTimeout.String(),
		"-test.coverprofile=" + profile,
	}, io.Discard)
	if err != nil || code != 0 {
		return nil, selWhyFailed
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		// absence-ok: no profile is a doubt about the package, never a test that covers nothing
		return nil, selWhyNoProfile
	}
	return parseSelProfile(string(data), module), ""
}

// boxDoubt says why the index is not worth keeping: it names a doubt that may
// be the box's (a build, a listing or a solo run that failed, a test that wrote
// no profile), which the next run, on a box in better order, measures away.
func (x *selIndex) boxDoubt() string {
	if len(x.Doubt) == 0 {
		return ""
	}
	dirs := make([]string, 0, len(x.Doubt))
	for dir := range x.Doubt {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	return "it names a doubt about " + strings.Join(dirs, ", ") + " that the next run measures again"
}
