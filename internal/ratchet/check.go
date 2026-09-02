package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Options configures one `ratchet check` run.
type Options struct {
	// Root is the consuming repo.
	Root string
	// Only, when set, runs exactly one law by name.
	Only string
	// Proposed overlays in-memory content for files being edited, keyed by
	// repo-relative slash path. It is how the pre-edit hook judges a write
	// that has not happened yet — and why a run carrying one never tightens a
	// baseline: the tree it measured does not exist.
	Proposed map[string]string
	// Files, when non-empty, narrows the scan to these repo-relative paths.
	// The pre-edit path uses it: one file's laws, not the tree's.
	Files []string
	// Tighten writes every baseline down to what this run measured.
	Tighten bool
	// CacheDir holds the per-file scan cache; empty disables caching.
	CacheDir string
}

// Finding is one regression: a key measured above its baseline ceiling.
type Finding struct {
	Law      string `json:"law"`
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	What     string `json:"what"`
	Key      string `json:"key"`
	Baseline int    `json:"baseline"`
	Measured int    `json:"measured"`
	Escape   string `json:"escape,omitempty"`
}

// Result is one run's verdict.
type Result struct {
	Laws         int       `json:"laws"`
	FilesScanned int       `json:"files_scanned"`
	FilesRead    int       `json:"files_read"`
	Findings     []Finding `json:"findings"`
	Tightened    []string  `json:"tightened,omitempty"`
}

// Blocked reports whether any deny law regressed — the exit-1 condition.
func (r Result) Blocked() bool {
	for _, f := range r.Findings {
		if f.Severity == Deny.String() {
			return true
		}
	}
	return false
}

// Lines renders one output line per finding.
func (r Result) Lines() []string {
	out := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		where := f.File
		if f.Line > 0 {
			where = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		line := fmt.Sprintf("%s: %s %s (baseline %d, now %d", f.Law, where, f.What, f.Baseline, f.Measured)
		if f.Escape != "" {
			line += "; escape: " + f.Escape
		}
		out = append(out, line+")")
	}
	return out
}

// Check scans the laws' scope once, applies every law, and compares each
// law's measured hits to its baseline.
func Check(opts Options) (Result, error) {
	laws, err := LoadLaws(opts.Root)
	if err != nil {
		return Result{}, err
	}
	if opts.Only != "" {
		var kept []Law
		for _, l := range laws {
			if l.Name == opts.Only {
				kept = append(kept, l)
			}
		}
		if len(kept) == 0 {
			return Result{}, fmt.Errorf("no law named %q under %s", opts.Only, LawsDir)
		}
		laws = kept
	}
	res := Result{Laws: len(laws)}
	if len(laws) == 0 {
		return res, nil
	}

	scan, err := scanTree(opts, laws)
	if err != nil {
		return Result{}, err
	}
	res.FilesScanned, res.FilesRead = scan.scanned, scan.read

	for _, law := range laws {
		if disarmed(law) {
			continue
		}
		hits := scan.byLaw[law.Name]
		switch law.Matcher.Kind {
		case KindRegistryBothWays:
			// A narrowed run has read ONE file, so it can see a use nobody
			// registered but never that a registry line is stale — that needs
			// the whole tree, and claiming it here would call every OTHER
			// file's switches dead.
			if hits, err = registryHits(opts.Root, law, scan.files, scan.content, true, len(opts.Files) == 0); err != nil {
				return Result{}, err
			}
		case KindDepGraphForbids:
			if hits, err = depGraphHits(opts.Root, law); err != nil {
				return Result{}, err
			}
		case KindFileSetContainment:
			if hits, err = containmentHits(opts.Root, law); err != nil {
				return Result{}, err
			}
		case KindJSONNumberCeiling:
			if hits, err = jsonCeilingHits(opts.Root, law, true); err != nil {
				return Result{}, err
			}
		}
		measured := map[string]int{}
		located := map[string]Hit{}
		for _, h := range hits {
			measured[h.Key] += h.Weight
			if _, seen := located[h.Key]; !seen {
				located[h.Key] = h
			}
		}
		baseline, path, err := loadLawBaseline(opts.Root, law)
		if err != nil {
			return Result{}, err
		}
		for _, r := range regressions(baseline, measured, law.Matcher.TolerancePct) {
			h := located[r.Key]
			res.Findings = append(res.Findings, Finding{
				Law:      law.Name,
				Severity: law.Severity.String(),
				File:     h.File,
				Line:     h.Line,
				What:     h.What,
				Key:      r.Key,
				Baseline: r.Baseline,
				Measured: r.Measured,
				Escape:   law.Escape,
			})
		}
		// A hypothetical tree must never rewrite a baseline: the content it
		// measured is not what is on disk, and a narrowed run has not even
		// looked at the rest of the tree.
		if !opts.Tighten || path == "" || len(opts.Proposed) > 0 || len(opts.Files) > 0 {
			continue
		}
		if baseline.Tighten(measured).Changed() {
			wrote, err := baseline.WriteIfChanged(path)
			if err != nil {
				return Result{}, err
			}
			if wrote {
				res.Tightened = append(res.Tightened, law.Baseline)
			}
		}
	}
	return res, nil
}

// disarmed reports whether a law declares an arming switch that is not set.
// A perf law reads generated output that only exists after a deliberate bench
// run: unarmed there is nothing to read, and both checking AND tightening must
// be skipped — tightening against no data would wipe the baseline.
func disarmed(law Law) bool {
	return law.Matcher.EnabledEnv != "" && os.Getenv(law.Matcher.EnabledEnv) == ""
}

// regressions compares measured to the baseline, allowing a per-law tolerance.
// A tolerance belongs to a MEASURED quantity (a wall-clock bench figure on a
// machine that is not the baseline's machine); every other law compares exactly.
func regressions(baseline *Baseline, measured map[string]int, tolerancePct int) []Regression {
	if tolerancePct <= 0 {
		return baseline.Regressions(measured)
	}
	var out []Regression
	for _, r := range baseline.Regressions(measured) {
		ceiling := r.Baseline + r.Baseline*tolerancePct/100
		if r.Measured > ceiling {
			out = append(out, r)
		}
	}
	return out
}

// loadLawBaseline reads a law's baseline in the form its key kind implies. A
// law with no baseline declared is judged at a bar of zero.
func loadLawBaseline(root string, law Law) (*Baseline, string, error) {
	form := Multiset
	if law.Matcher.Key == KeyFile {
		form = Counted
	}
	if law.Baseline == "" {
		return &Baseline{form: form}, "", nil
	}
	path := filepath.Join(root, filepath.FromSlash(law.Baseline))
	b, err := LoadBaseline(path, form)
	return b, path, err
}

// treeScan is one walk's result: every in-scope file's content-derived hits,
// grouped by law, plus the file contents the registry matcher needs.
type treeScan struct {
	byLaw           map[string][]Hit
	files           []string
	content         map[string]string
	scanned, read   int
	proposedContent map[string]string
}

// scanTree walks every law's scope ONCE, reading each file at most once and
// serving unchanged files from the mtime cache.
func scanTree(opts Options, laws []Law) (*treeScan, error) {
	scan := &treeScan{byLaw: map[string][]Hit{}, content: map[string]string{}}
	cache := loadCache(opts.CacheDir, opts.Root, laws)

	paths, err := collectFiles(opts, laws)
	if err != nil {
		return nil, err
	}
	needsContent := false
	for _, l := range laws {
		if l.Matcher.Kind == KindRegistryBothWays {
			needsContent = true
		}
	}
	for _, rel := range paths {
		scan.scanned++
		proposed, overlaid := opts.Proposed[rel]
		var (
			hits map[string][]Hit
			ok   bool
		)
		if !overlaid && !needsContent {
			hits, ok = cache.lookup(opts.Root, rel)
		}
		if !ok {
			content := proposed
			if !overlaid {
				data, err := os.ReadFile(filepath.Join(opts.Root, filepath.FromSlash(rel)))
				if err != nil {
					continue // a file that vanished mid-walk is not a finding
				}
				content = string(data)
				scan.read++
			}
			if needsContent {
				scan.content[rel] = content
			}
			hits = map[string][]Hit{}
			for _, law := range laws {
				if !law.Scope.Matches(rel) {
					continue
				}
				if h := law.HitsIn(rel, content); len(h) > 0 {
					hits[law.Name] = h
				}
			}
			if !overlaid {
				cache.store(opts.Root, rel, hits)
			}
		}
		for name, h := range hits {
			scan.byLaw[name] = append(scan.byLaw[name], h...)
		}
		scan.files = append(scan.files, rel)
	}
	cache.save()
	return scan, nil
}

// collectFiles is the union of every law's scope, walked once and sorted, plus
// any proposed file that does not exist on disk yet.
func collectFiles(opts Options, laws []Law) ([]string, error) {
	if len(opts.Files) > 0 {
		return dedupe(append([]string{}, opts.Files...)), nil
	}
	inScope := func(rel string) bool {
		for _, l := range laws {
			if l.Scope.Matches(rel) {
				return true
			}
		}
		return false
	}
	walkable := func(rel string) bool {
		for _, l := range laws {
			if l.Scope.couldMatchUnder(rel) {
				return true
			}
		}
		return false
	}

	ignore := loadGitignore(opts.Root)
	var out []string
	var walk func(dir, rel string) error
	walk = func(dir, rel string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil // an unreadable dir is not a finding
		}
		for _, e := range entries {
			child := path(rel, e.Name())
			if ignore.ignored(child, e.IsDir()) {
				continue
			}
			if e.IsDir() {
				if !walkable(child) {
					continue
				}
				if err := walk(filepath.Join(dir, e.Name()), child); err != nil {
					return err
				}
				continue
			}
			if inScope(child) {
				out = append(out, child)
			}
		}
		return nil
	}
	if err := walk(opts.Root, ""); err != nil {
		return nil, err
	}
	for rel := range opts.Proposed {
		if inScope(rel) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return dedupe(out), nil
}

func dedupe(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for i, s := range in {
		if i == 0 || in[i-1] != s {
			out = append(out, s)
		}
	}
	return out
}

// registryHits answers a registry-both-ways law: every use must be registered
// AND every registry line must be used. Both directions are the point — a
// registry nobody prunes rots into a list of names that no longer exist.
func registryHits(root string, law Law, files []string, content map[string]string, applyScope, wholeTree bool) ([]Hit, error) {
	registryPath := filepath.Join(root, filepath.FromSlash(law.Matcher.RegistryFile))
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, fmt.Errorf("law %q: reading registry %s: %w", law.Name, law.Matcher.RegistryFile, err)
	}
	registered := map[string]int{}
	var order []string
	for i, line := range splitLines(string(data)) {
		m := law.Matcher.EntryPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if _, seen := registered[m[1]]; !seen {
			order = append(order, m[1])
		}
		registered[m[1]] = i + 1
	}

	used := map[string]Hit{}
	var useOrder []string
	for _, rel := range files {
		if applyScope && !law.Scope.Matches(rel) {
			continue
		}
		for i, line := range splitLines(content[rel]) {
			for _, m := range law.Matcher.UsePattern.FindAllStringSubmatch(line, -1) {
				name := m[len(m)-1]
				if _, seen := used[name]; seen {
					continue
				}
				used[name] = Hit{File: rel, Line: i + 1}
				useOrder = append(useOrder, name)
			}
		}
	}

	var hits []Hit
	sort.Strings(useOrder)
	for _, name := range useOrder {
		if _, ok := registered[name]; ok {
			continue
		}
		at := used[name]
		hits = append(hits, Hit{
			Law: law.Name, File: at.File, Line: at.Line, Weight: 1,
			Key:  "unregistered | " + name,
			What: fmt.Sprintf("%s is used but not in %s", name, law.Matcher.RegistryFile),
		})
	}
	if !wholeTree {
		return hits, nil
	}
	sort.Strings(order)
	for _, name := range order {
		if _, ok := used[name]; ok {
			continue
		}
		hits = append(hits, Hit{
			Law: law.Name, File: law.Matcher.RegistryFile, Line: registered[name], Weight: 1,
			Key:  "stale | " + name,
			What: fmt.Sprintf("%s is registered but nothing uses it", name),
		})
	}
	return hits, nil
}

// LawNames lists a repo's law names, for a caller that wants to say "no laws"
// without scanning anything.
func LawNames(root string) ([]string, error) {
	laws, err := LoadLaws(root)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(laws))
	for _, l := range laws {
		names = append(names, l.Name)
	}
	return names, nil
}

// HasLaws reports whether a repo declares any law at all.
func HasLaws(root string) bool {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(LawsDir)))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			return true
		}
	}
	return false
}
