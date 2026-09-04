package ratchet

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// readFile and readDir stand in for os.ReadFile/os.ReadDir at every call site
// that has to tell a legitimately-vanished path (a concurrent delete mid-walk,
// which stays silent) apart from a path the engine could not read for any
// other reason (locked, permission-denied, I/O error — which must not report
// the tree clean). Indirecting them lets a test simulate the second class
// without depending on OS-specific filesystem behavior to produce it.
var (
	readFile = os.ReadFile
	readDir  = os.ReadDir
)

// vanished reports whether a read error is the one case that is not a
// finding: the path stopped existing between the walk seeing it and the read
// running. Every other error — locked, permission-denied, I/O — means the
// engine could not perform the scan it owes, and must not be swallowed into
// a clean verdict over data nobody looked at.
func vanished(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// ScanReadError reports that the tree scan could not read a file or
// directory a law's scope needed, for a reason other than the path
// vanishing mid-walk (see vanished). It is a DIFFERENT offence from every
// other error Check can return (a malformed law TOML, an unknown matcher
// kind): the law tooling itself is fine, but the read it needed to judge
// THIS path failed — so it is the path, not the whole rule set, that needs
// a retry or a fix. A caller distinguishes the two with errors.As; matching
// on the error string is not the contract.
type ScanReadError struct {
	// Path is the path the read failed against, in whatever form the
	// failing call site had it (repo-relative for the tree walk, or the
	// absolute path passed to readFile) — always the value worth printing.
	Path string
	Err  error
}

func (e *ScanReadError) Error() string {
	return fmt.Sprintf("reading %s: %s — a clean verdict would be over a scan the engine could not perform", e.Path, e.Err)
}

func (e *ScanReadError) Unwrap() error { return e.Err }

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
	// Tracked, when non-empty, is the ONLY set of paths the whole-tree walk
	// may consider — the commit gate passes `git ls-files`, because it judges
	// what is IN the commit and an untracked file is part of no commit. A
	// shared checkout is full of other people's scaffolding, and rejecting a
	// merge over a file nobody is committing is a rejection nobody can clear.
	// Everything else about the run is unchanged: scope floors and stale
	// registry entries are still whole-tree questions.
	Tracked []string
	// TrackedIgnored is the subset of Tracked that .gitignore also matches. A
	// repo can ignore a whole extension and still track those files; the disk
	// walk hands one to a law only when it declared `ignore_gitignore`, and
	// the tracked set carries the same flag so the two agree.
	TrackedIgnored []string
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
	// Remedy is the way through, in the imperative: the escape comment to
	// write, or the fact that there is none. A denial that names the offence
	// and stops sends the reader to open the law file.
	Remedy string `json:"remedy,omitempty"`
}

// Result is one run's verdict.
type Result struct {
	Laws         int `json:"laws"`
	FilesScanned int `json:"files_scanned"`
	FilesRead    int `json:"files_read"`
	// FilesMatched counts the files whose LAW MATCHERS ran, as against the
	// ones served from the mtime cache. It is the cache's own measure: a
	// repeat run over an unchanged tree matches nothing.
	FilesMatched int       `json:"files_matched"`
	Findings     []Finding `json:"findings"`
	Tightened    []string  `json:"tightened,omitempty"`
	// NewerLaws names every law whose declared schema exceeds SchemaVersion:
	// it was judged by the keys this binary knows and the rest were skipped.
	// The caller warns once per name and logs `ratchet-law-newer:<law>` — a
	// half-read rule that says nothing looks exactly like a clean one.
	NewerLaws []NewerLaw `json:"newer_laws,omitempty"`
	// UnusedScopeSets names every set declared in .ratchet/scopes.toml that no
	// law's [scope].alias references — a set nobody uses is dead weight the
	// next reader has no way to tell from a live one.
	UnusedScopeSets []string `json:"unused_scope_sets,omitempty"`
	// Notes are informational, never a Finding: a code-mode line-count law
	// whose baseline still carries a higher, text-mode ceiling is not a
	// regression (the measure only went down), but the ceiling is stale and
	// a report-only run would otherwise say nothing about it.
	Notes []string `json:"notes,omitempty"`
	// PresetDrift names every law that `extends` a preset whose [matcher],
	// re-rendered with the law's own Params, no longer matches what the law
	// actually declares — a hand-fork nobody flagged as one.
	PresetDrift []string `json:"preset_drift,omitempty"`
}

// lineModeNotes reports every key whose baseline ceiling sits above what a
// code-mode line-count law just measured — the fingerprint of a baseline
// still recorded under `count = "text"`.
func Check(opts Options) (Result, error) {
	laws, err := LoadLaws(opts.Root)
	if err != nil {
		return Result{}, err
	}
	var unusedScopeSets []string
	if sets, err := LoadScopeSets(opts.Root); err != nil {
		return Result{}, err
	} else if len(sets) > 0 {
		used := map[string]bool{}
		for _, l := range laws {
			if l.Scope.Alias != "" {
				used[l.Scope.Alias] = true
			}
		}
		for name := range sets {
			if !used[name] {
				unusedScopeSets = append(unusedScopeSets, name)
			}
		}
		sort.Strings(unusedScopeSets)
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
	res := Result{Laws: len(laws), UnusedScopeSets: unusedScopeSets}
	for _, l := range laws {
		if l.Newer {
			res.NewerLaws = append(res.NewerLaws, NewerLaw{Name: l.Name, Schema: l.Schema})
		}
		note, err := presetDrift(l)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", l.Name, err)
		}
		if note != "" {
			res.PresetDrift = append(res.PresetDrift, note)
		}
	}
	if len(laws) == 0 {
		return res, nil
	}

	scan, err := scanTree(opts, laws)
	if err != nil {
		return Result{}, err
	}
	res.FilesScanned, res.FilesRead, res.FilesMatched = scan.scanned, scan.read, scan.matched

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
			law.CacheDir = opts.CacheDir
			if hits, err = depGraphHits(opts.Root, law); err != nil {
				return Result{}, err
			}
		case KindFileSetContainment:
			if hits, err = containmentHits(opts.Root, law); err != nil {
				return Result{}, err
			}
		case KindJSONNumberCeiling:
			if hits, err = jsonCeilingHits(opts.Root, law, true, cargoTargetDir()); err != nil {
				return Result{}, err
			}
		}
		if len(opts.Files) == 0 {
			hits = append(hits, scopeHits(opts.Root, law, scan.files, scan.ignored)...)
		}
		baseline, path, err := loadLawBaseline(opts.Root, law)
		if err != nil {
			return Result{}, err
		}
		// Measured and baselined are counted by the SAME rule — the baseline
		// owns it, because it is the file whose rows define the identity.
		measured := map[string]int{}
		located := map[string]Hit{}
		sites := map[string][]string{}
		for _, h := range hits {
			id := baseline.Identity(h.Key)
			measured[id] += h.Weight
			sites[id] = append(sites[id], h.Key)
			if _, seen := located[id]; !seen {
				located[id] = h
			}
		}
		for _, keys := range sites {
			sort.Strings(keys)
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
				Remedy:   remedyFor(law),
			})
		}
		// A law switched from text to code counting measures FEWER lines than
		// the baseline recorded it under — every key just looks tightened, so
		// a report-only run (which never writes) would otherwise say nothing
		// while the ceiling still reflects the old mode.
		if law.Matcher.Kind == KindLineCount && law.Matcher.LineMode == LineCountCode && !opts.Tighten {
			actual := map[string]int{}
			for _, rel := range scan.files {
				content, ok := scan.content[rel]
				if !ok || !law.Scope.Matches(rel) {
					continue
				}
				for k, n := range law.lineCountMeasures(rel, splitLines(content)) {
					actual[k] = n
				}
			}
			res.Notes = append(res.Notes, lineModeNotes(law.Name, baseline.Counts(), actual)...)
		}
		// A hypothetical tree must never rewrite a baseline: the content it
		// measured is not what is on disk, and a narrowed run has not even
		// looked at the rest of the tree.
		if !opts.Tighten || path == "" || len(opts.Proposed) > 0 || len(opts.Files) > 0 {
			continue
		}
		// Tighten unconditionally and let WriteIfChanged decide: a count that
		// did not move can still leave a row naming a file that is gone, and
		// re-pathing it is the whole point of a path-agnostic key. The write
		// is byte-stable, so a tree with nothing to fix still writes nothing.
		baseline.TightenWithSites(measured, sites)
		wrote, err := baseline.WriteIfChanged(path)
		if err != nil {
			return Result{}, err
		}
		if wrote {
			res.Tightened = append(res.Tightened, law.Baseline)
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

// lineKeyedKinds are the matchers whose hits are `<path> | <trimmed line>`:
// one offending LINE inside one in-scope file. Their debt is a multiset of
// TEXT, so moving the file that carries a line is not a regression. The
// whole-tree kinds are excluded on purpose — a dependency PATH, a registry
// name or a containment capture is already path-free, and stripping at the
// first ` | ` there would only blur two categories into one.
var lineKeyedKinds = map[MatcherKind]bool{
	KindRegexAbsent:       true,
	KindMarkerWithinLines: true,
	KindDocPathResolves:   true,
}

// baselineForm is the shape a law's baseline file is read in: counted per
// file, a multiset of exact keys, or a multiset of offending text.
func baselineForm(law Law) Form {
	switch {
	case law.Matcher.Key == KeyFile:
		return Counted
	case lineKeyedKinds[law.Matcher.Kind]:
		return MultisetByText
	default:
		return Multiset
	}
}

// loadLawBaseline reads a law's baseline in the form its key kind implies. A
// law with no baseline declared is judged at a bar of zero.
func loadLawBaseline(root string, law Law) (*Baseline, string, error) {
	form := baselineForm(law)
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
	byLaw                  map[string][]Hit
	ignored                map[string]bool
	files                  []string
	content                map[string]string
	scanned, read, matched int
}

// scanTree walks every law's scope ONCE, reading each file at most once and
// serving unchanged files from the mtime cache.
func scanTree(opts Options, laws []Law) (*treeScan, error) {
	scan := &treeScan{byLaw: map[string][]Hit{}, content: map[string]string{}}
	cache := loadCache(opts.CacheDir, opts.Root, laws)

	paths, ignored, err := collectFiles(opts, laws)
	if err != nil {
		return nil, err
	}
	scan.ignored = ignored
	// A registry-both-ways law answers a WHOLE-TREE question, so it needs the
	// raw content of every file in ITS scope. That is a reason to read those
	// files again; it is not a reason to re-run 26 laws' matchers over the
	// whole tree. Keeping the two apart is what makes a repeat run cheap:
	// measured in borld, 26 laws over 1936 files, 5.6s became 1.2s.
	var contentLaws []Law
	for _, l := range laws {
		if l.Matcher.Kind == KindRegistryBothWays {
			contentLaws = append(contentLaws, l)
		}
		// A code-mode line-count law's stale-baseline note needs the actual
		// measured count on a file that no longer produces a hit at all — the
		// cache's "unchanged, no hits" fast path never reads that file's
		// content otherwise.
		if l.Matcher.Kind == KindLineCount && l.Matcher.LineMode == LineCountCode {
			contentLaws = append(contentLaws, l)
		}
	}
	for _, rel := range paths {
		scan.scanned++
		proposed, overlaid := opts.Proposed[rel]
		needsContent := scopedByAny(contentLaws, rel)
		var (
			hits map[string][]Hit
			ok   bool
		)
		if !overlaid {
			hits, ok = cache.lookup(opts.Root, rel)
		}
		content := proposed
		if !overlaid && (!ok || needsContent) {
			data, err := readFile(filepath.Join(opts.Root, filepath.FromSlash(rel)))
			if err != nil {
				if vanished(err) {
					continue // a file that vanished mid-walk is not a finding
				}
				return nil, &ScanReadError{Path: rel, Err: err}
			}
			content = string(data)
			scan.read++
		}
		if needsContent {
			scan.content[rel] = content
		}
		if !ok {
			scan.matched++
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

// scopedByAny reports whether any of these laws claims rel.
func scopedByAny(laws []Law, rel string) bool {
	for _, l := range laws {
		if l.Scope.Matches(rel) {
			return true
		}
	}
	return false
}

// collectFiles is the union of every law's scope, walked once and sorted, plus
// any proposed file that does not exist on disk yet.
func collectFiles(opts Options, laws []Law) ([]string, map[string]bool, error) {
	ignoredFiles := map[string]bool{}
	if len(opts.Files) > 0 {
		return dedupe(append([]string{}, opts.Files...)), ignoredFiles, nil
	}
	// A gitignored path is judged only by a law that opted out, so the walk
	// carries whether it is under one: a repo that ignores a whole extension
	// (borld ignores *.md) would otherwise hide the very files a doc law is about.
	inScope := func(rel string, ignored bool) bool {
		for _, l := range laws {
			if ignored && !l.Scope.IgnoreGitignore {
				continue
			}
			if l.Scope.Matches(rel) {
				return true
			}
		}
		return false
	}
	walkable := func(rel string, ignored bool) bool {
		for _, l := range laws {
			if ignored && !l.Scope.IgnoreGitignore {
				continue
			}
			if l.Scope.couldMatchUnder(rel) {
				return true
			}
		}
		return false
	}

	// A tracked set replaces the walk entirely — but it carries the SAME
	// gitignore flag the walk would have computed, so a law that never opted
	// into ignored files does not suddenly see them just because git tracks
	// them.
	if len(opts.Tracked) > 0 {
		ignoredTracked := map[string]bool{}
		for _, rel := range opts.TrackedIgnored {
			ignoredTracked[normalizeSlashes(rel)] = true
		}
		var out []string
		for _, rel := range opts.Tracked {
			rel = normalizeSlashes(rel)
			if rel == "" || !inScope(rel, ignoredTracked[rel]) {
				continue
			}
			out = append(out, rel)
			if ignoredTracked[rel] {
				ignoredFiles[rel] = true
			}
		}
		for rel := range opts.Proposed {
			if inScope(rel, false) {
				out = append(out, rel)
			}
		}
		sort.Strings(out)
		return dedupe(out), ignoredFiles, nil
	}

	ignore := loadGitignore(opts.Root)
	var out []string
	var walk func(dir, rel string, ignored bool) error
	walk = func(dir, rel string, ignored bool) error {
		entries, err := readDir(dir)
		if err != nil {
			if vanished(err) {
				return nil // a dir that vanished mid-walk is not a finding
			}
			return &ScanReadError{Path: dir, Err: err}
		}
		for _, e := range entries {
			child := path(rel, e.Name())
			if e.IsDir() && e.Name() == ".git" {
				continue // never a subject, and no law may opt into it
			}
			childIgnored := ignored || ignore.ignored(child, e.IsDir())
			if e.IsDir() {
				if !walkable(child, childIgnored) {
					continue
				}
				if err := walk(filepath.Join(dir, e.Name()), child, childIgnored); err != nil {
					return err
				}
				continue
			}
			if inScope(child, childIgnored) {
				out = append(out, child)
				if childIgnored {
					ignoredFiles[child] = true
				}
			}
		}
		return nil
	}
	if err := walk(opts.Root, "", false); err != nil {
		return nil, nil, err
	}
	for rel := range opts.Proposed {
		if inScope(rel, false) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return dedupe(out), ignoredFiles, nil
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// scopeHits judges the SCOPE itself, over the whole tree only: a law whose
// globs quietly stopped matching reports green over files it never opened, and
// an include naming one file that is gone is a broken citation, not an empty
// set. Both are findings the baseline has never seen, so both surface at once.
func scopeHits(root string, law Law, files []string, ignored map[string]bool) []Hit {
	var hits []Hit
	for _, p := range law.Scope.ExplicitPaths() {
		if isFile(filepath.Join(root, filepath.FromSlash(p))) {
			continue
		}
		hits = append(hits, Hit{
			Law: law.Name, File: p, Weight: 1,
			Key:  "missing-scope-file | " + p,
			What: p + " is named by scope.include but is not there",
		})
	}
	if law.Scope.MinFiles == 0 {
		return hits
	}
	matched := 0
	for _, rel := range files {
		if ignored[rel] && !law.Scope.IgnoreGitignore {
			continue
		}
		if law.Scope.Matches(rel) {
			matched++
		}
	}
	if matched >= law.Scope.MinFiles {
		return hits
	}
	return append(hits, Hit{
		Law: law.Name, Weight: 1, Key: "scope-floor",
		What: fmt.Sprintf("scope matched %d %s, min_files = %d", matched, plural(matched, "file"), law.Scope.MinFiles),
	})
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
// lastCapture is the last group of a match that captured anything.
func lastCapture(m []string) string {
	for i := len(m) - 1; i >= 1; i-- {
		if m[i] != "" {
			return m[i]
		}
	}
	return ""
}

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
				// An alternation carries one group per branch, and every branch
				// that did not match captured nothing: the LAST non-empty group
				// is the one that did.
				name := lastCapture(m)
				if name == "" {
					continue
				}
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
