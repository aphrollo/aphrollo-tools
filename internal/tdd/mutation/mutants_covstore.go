package mutation

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The incremental coverage store: what each test of a package executed, kept
// per test and per function, so a commit that edits one function measures the
// tests that ran it and not the package again. An entry is valid while the
// functions it names, and the test's own function, hash as they did when it
// was measured; where a function sits in its file is not part of the key, so a
// function that moves, or a file that is renamed, keeps its entries and the
// view re-reads its lines. Anything that can change what every test does (a
// non-function declaration, TestMain, init, the toolchain and build settings)
// drops the store whole. What it deliberately does not key is the content of
// the packages this one imports and of its testdata: the coverage of the
// package's own lines is nearly independent of them, and a store built before
// such a change is a statement about the tests as they ran then. A store is
// bounded and swept like every kept map (mutants_testmap_store.go).

// covSchema is the shape of the store on disk. A store of another schema is no
// store.
const covSchema = 3

// covFunc is what one test executed in one function: the function's hash when
// measured, and the blocks as line pairs counted from the function's first line.
type covFunc struct {
	Func   string   `json:"fn"`
	Hash   string   `json:"h"`
	Blocks [][2]int `json:"b"`
}

// covTest is one test's measurement: the hash of the test's own function and
// what it executed.
type covTest struct {
	Hash  string    `json:"h"`
	Cover []covFunc `json:"c"`
}

// covShape is every block the coverage profile lists for a function, run or
// not, at the function's hash. It is what tells a line no test ran from a line
// that is not a block at all.
type covShape struct {
	Hash   string   `json:"h"`
	Blocks [][2]int `json:"b"`
}

// covStore is one package's store.
type covStore struct {
	Schema  int    `json:"schema"`
	Package string `json:"package"`
	// Env is the key of the build inputs the store was measured under.
	Env string `json:"env"`
	// Rest, Globals and Aux are the hashes of what reaches every test or the
	// tests' own helpers: the non-function declarations per file, TestMain and
	// init functions, and the test files' helper functions and variables.
	Rest    string              `json:"rest,omitempty"`
	Globals map[string]string   `json:"globals,omitempty"`
	Aux     map[string]string   `json:"aux,omitempty"`
	Shapes  map[string]covShape `json:"shapes,omitempty"`
	Tests   map[string]covTest  `json:"tests,omitempty"`
}

// covPlan is what a commit has to measure, from the store and the source.
type covPlan struct {
	// Reset says the store was dropped whole.
	Reset bool
	// Valid is the tests whose entries still hold, Stale the tests of the
	// package that have none, both sorted.
	Valid, Stale []string
	// Measure is the stale tests worth measuring now, the static candidates of
	// the commit's functions first, then the tests an edit took the entry of.
	Measure []string
	// Fill is the rest of the stale tests, sorted: measured after Measure
	// while a run that is happening anyway has time to spare, so the store
	// converges to every test of the package.
	Fill []string
	// Dropped is how many entries the source outdated or deleted.
	Dropped int
}

// planCoverage reconciles the store with the scan: it drops what the source
// has outdated, keeps what still holds, and answers what is left to measure
// for the functions the commit changes. It changes st, which is the store as
// it should be kept once the measurement is added.
func planCoverage(st *covStore, scan pkgScan, mutantFuncs []string) covPlan {
	globals, aux := map[string]string{}, map[string]string{}
	runners := map[string]bool{}
	for _, key := range scan.TestKey {
		runners[key] = true
	}
	for key, f := range scan.Funcs {
		switch {
		case f.Global:
			globals[key] = f.Hash
		case f.Test && !runners[key]:
			aux[key] = f.Hash
		}
	}
	for _, v := range scan.Vars {
		if v.Test {
			aux[v.Key] = v.Hash
		}
	}
	var plan covPlan
	if len(st.Tests) > 0 && (st.Rest != scan.Rest || !maps2Equal(st.Globals, globals)) {
		plan.Reset = true
		*st = covStore{Schema: covSchema, Package: st.Package, Env: st.Env}
	}
	changedAux := map[string]bool{}
	for key, hash := range aux {
		if old, ok := st.Aux[key]; ok && old != hash {
			changedAux[key] = true
		}
	}
	for key := range st.Aux {
		if _, ok := aux[key]; !ok {
			changedAux[key] = true
		}
	}
	reachesAux := map[string]bool{}
	if len(changedAux) > 0 {
		seed := make([]string, 0, len(changedAux))
		for key := range changedAux {
			if _, ok := scan.decl(key); ok {
				seed = append(seed, key)
			}
		}
		closure := scan.callersClosure(seed)
		for name, key := range scan.TestKey {
			if closure[key] {
				reachesAux[name] = true
			}
		}
	}
	st.Rest, st.Globals, st.Aux = scan.Rest, globals, aux
	if st.Tests == nil {
		st.Tests = map[string]covTest{}
	}

	invalidated := map[string]bool{}
	plan.Dropped = 0
	for name, entry := range st.Tests {
		key, ok := scan.TestKey[name]
		if !ok {
			delete(st.Tests, name)
			plan.Dropped++
			continue
		}
		if scan.Funcs[key].Hash != entry.Hash || reachesAux[name] || !coverHolds(scan, entry) {
			delete(st.Tests, name)
			plan.Dropped++
			invalidated[name] = true
		}
	}
	// A shape is read at its function's hash; one that no longer matches is
	// remeasured with the next profile.
	for key, shape := range st.Shapes {
		if d, ok := scan.decl(key); !ok || d.Hash != shape.Hash {
			delete(st.Shapes, key)
		}
	}
	for _, name := range scan.testNameList() {
		if _, ok := st.Tests[name]; ok {
			plan.Valid = append(plan.Valid, name)
		} else {
			plan.Stale = append(plan.Stale, name)
		}
	}
	candidates := scan.candidateTests(mutantFuncs)
	for _, name := range candidates {
		if slices.Contains(plan.Stale, name) {
			plan.Measure = append(plan.Measure, name)
		}
	}
	for _, name := range plan.Stale {
		if invalidated[name] && !slices.Contains(plan.Measure, name) {
			plan.Measure = append(plan.Measure, name)
		}
	}
	for _, name := range plan.Stale {
		if !slices.Contains(plan.Measure, name) {
			plan.Fill = append(plan.Fill, name)
		}
	}
	return plan
}

// maps2Equal reports whether two string maps hold the same pairs, a nil map
// and an empty one being the same.
func maps2Equal(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// coverHolds reports whether every function an entry names is still in the
// scan at the hash it was measured at.
func coverHolds(scan pkgScan, entry covTest) bool {
	for _, c := range entry.Cover {
		d, ok := scan.decl(c.Func)
		if !ok || d.Hash != c.Hash {
			return false
		}
	}
	return true
}

// recordProfile adds what one test's profile says to the store: the shape of
// every function the profile lists, and for the blocks the test executed, the
// functions they are in. A block outside every function of the scan is not
// kept. The entry replaces any the test had.
func recordProfile(st *covStore, scan pkgScan, test string, blocks map[coverBlock]bool) {
	if st.Shapes == nil {
		st.Shapes = map[string]covShape{}
	}
	if st.Tests == nil {
		st.Tests = map[string]covTest{}
	}
	shapes := map[string][][2]int{}
	hits := map[string][][2]int{}
	hashes := map[string]string{}
	for b, hit := range blocks {
		d, ok := scan.declAt(b.File, b.From)
		if !ok {
			continue
		}
		rel := [2]int{b.From - d.Start, b.To - d.Start}
		shapes[d.Key] = append(shapes[d.Key], rel)
		hashes[d.Key] = d.Hash
		if hit {
			hits[d.Key] = append(hits[d.Key], rel)
		}
	}
	for key, list := range shapes {
		slices.SortFunc(list, func(a, b [2]int) int { return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1])) })
		st.Shapes[key] = covShape{Hash: hashes[key], Blocks: list}
	}
	entry := covTest{Hash: scan.Funcs[scan.TestKey[test]].Hash}
	keys := make([]string, 0, len(hits))
	for key := range hits {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		list := hits[key]
		slices.SortFunc(list, func(a, b [2]int) int { return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1])) })
		entry.Cover = append(entry.Cover, covFunc{Func: key, Hash: hashes[key], Blocks: list})
	}
	st.Tests[test] = entry
}

// view is the store as the line-keyed map the selection reads, at the lines of
// the scan: valid names the tests whose entries are used, unknown the tests
// that wrote no profile, and unmeasured how many tests of the package have no
// measurement, which makes the map partial.
func (st *covStore) view(scan pkgScan, valid, unknown []string, unmeasured int) testMap {
	names := slices.Clone(valid)
	slices.Sort(names)
	index := map[string]int{}
	for i, name := range names {
		index[name] = i
	}
	// ran[decl][block] is the indexes of the tests that executed the block.
	ran := map[string]map[[2]int][]int{}
	for _, name := range names {
		for _, c := range st.Tests[name].Cover {
			if ran[c.Func] == nil {
				ran[c.Func] = map[[2]int][]int{}
			}
			for _, b := range c.Blocks {
				ran[c.Func][b] = append(ran[c.Func][b], index[name])
			}
		}
	}
	m := testMap{Schema: testMapSchema, Package: st.Package, Tests: names, Unknown: slices.Clone(unknown), Partial: unmeasured > 0}
	keys := make([]string, 0, len(st.Shapes))
	for key := range st.Shapes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		d, ok := scan.decl(key)
		if !ok || d.Hash != st.Shapes[key].Hash {
			continue
		}
		for _, b := range st.Shapes[key].Blocks {
			tests := slices.Clone(ran[key][b])
			slices.Sort(tests)
			m.Blocks = append(m.Blocks, mapBlock{coverBlock: coverBlock{File: d.File, From: d.Start + b[0], To: d.Start + b[1]}, Tests: tests})
		}
	}
	return m
}

// coverEnvKey is the key of the build inputs a store is measured under: the
// package, its import path, the Go version and build settings, the test tags,
// the mutation environment and the module files. It names no source content.
func coverEnvKey(ctx context.Context, root, dir string, cfg MutantsConfig) (string, error) {
	env, err := goEnvFn(ctx, root)
	if err != nil {
		return "", fmt.Errorf("go env for %s: %w", dir, err)
	}
	h := sha256.New()
	fmt.Fprintf(h, "schema %d\nimport %s/%s\nenv %s\ntags %s\nmutants-env %s\n", covSchema, modulePath(root), dir, strings.TrimSpace(env), strings.Join(testTags(ctx), ","), strings.Join(cfg.Env, ";"))
	for _, name := range []string{"go.mod", "go.sum", "go.work", "go.work.sum"} {
		hashFile(h, filepath.Join(root, name), name)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// covStorePath is the file the store of one package at one build key is kept
// in, "" when there is no directory to keep it in.
func covStorePath(root, dir, env string) string {
	base := CoverCacheDir(root)
	if base == "" {
		return ""
	}
	slug := strings.ReplaceAll(filepath.ToSlash(dir), "/", "__")
	if slug == "." {
		slug = "_root"
	}
	return filepath.Join(base, slug+"-v3-"+env[:min(len(env), 16)]+".json")
}

// loadCovStore reads the kept store of a package at a build key, or answers an
// empty one: a store of another schema, key or package, or one that does not
// parse, is no store. A store that is read is marked read, so what is trimmed
// and swept is what nothing has used lately.
func loadCovStore(root, dir, env string) *covStore {
	fresh := &covStore{Schema: covSchema, Package: dir, Env: env}
	path := covStorePath(root, dir, env)
	if path == "" {
		return fresh
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// absence-ok: no kept store is the cold case, which measures the commit's candidates
		return fresh
	}
	var st covStore
	if json.Unmarshal(data, &st) != nil || st.Schema != covSchema || st.Env != env || st.Package != dir {
		return fresh
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return &st
}

// save keeps the store, then trims the directory to its bounds.
func (st *covStore) save(root string) error {
	path := covStorePath(root, st.Package, st.Env)
	if path == "" {
		return errors.New("no git directory to keep the coverage in")
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".testmap-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	trimCoverCache(filepath.Dir(path), coverCacheMaxEntries, coverCacheMaxBytes)
	return nil
}
