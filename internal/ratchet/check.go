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
	// SkippedLaws names every law whose [matcher].kind this binary's compiled
	// matcherKeys table does not recognize at all — not judged, not scanned,
	// not baselined. The caller warns once per name and counts it the way it
	// counts every other stand-down (see Law.UnknownKind).
	SkippedLaws []SkippedLaw `json:"skipped_laws,omitempty"`
	// UnusedScopeSets names every set declared in .ratchet/scopes.toml that no
	// law's [scope].alias references — a set nobody uses is dead weight the
	// next reader has no way to tell from a live one.
	UnusedScopeSets []string `json:"unused_scope_sets,omitempty"`
	// Notes are informational, never a Finding: a code-mode line-count law
	// whose baseline still carries a higher, text-mode ceiling is not a
	// regression (the measure only went down), but the ceiling is stale and
	// a report-only run would otherwise say nothing about it.
	Notes []string `json:"notes,omitempty"`
	// RegressedBaselines names, for every law with at least one Finding this
	// run, the baseline file responsible — see RegressedBaseline. A caller on
	// the refusing path uses it to ask BaselineHeadRegressionNotes whether
	// that file lost a row relative to HEAD before this run ever started.
	RegressedBaselines []RegressedBaseline `json:"regressed_baselines,omitempty"`
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
	res := Result{UnusedScopeSets: unusedScopeSets}
	var judged []Law
	for _, l := range laws {
		if l.Newer {
			res.NewerLaws = append(res.NewerLaws, NewerLaw{Name: l.Name, Schema: l.Schema})
		}
		// An unknown matcher kind is a binary that predates this law, not a
		// broken law: skip judging it — and it alone — rather than reject
		// the whole set the way LoadLaws itself did before #440. The caller
		// (ratchetStage) is what turns this into a loud, counted warning.
		if l.UnknownKind != "" {
			res.SkippedLaws = append(res.SkippedLaws, SkippedLaw{Name: l.Name, Kind: l.UnknownKind})
			continue
		}
		note, err := presetDrift(l)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", l.Name, err)
		}
		if note != "" {
			res.PresetDrift = append(res.PresetDrift, note)
		}
		judged = append(judged, l)
	}
	laws = judged
	res.Laws = len(laws)
	if len(laws) == 0 {
		return res, nil
	}

	scan, err := scanTree(opts, laws)
	if err != nil {
		return Result{}, err
	}
	res.FilesScanned, res.FilesRead, res.FilesMatched = scan.scanned, scan.read, scan.matched

	var pending []pendingTighten
	for _, law := range laws {
		if disarmed(law) {
			continue
		}
		hits := scan.byLaw[law.Name]
		switch law.Matcher.Kind {
		case KindMarkerInPackage:
			if hits, err = packageMarkerHits(opts.Root, law, scan.files, scan.content); err != nil {
				return Result{}, err
			}
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
		case KindDepGraphCeiling:
			law.CacheDir = opts.CacheDir
			if hits, err = depGraphCeilingHits(opts.Root, law); err != nil {
				return Result{}, err
			}
		case KindGoDepGraphForbids:
			if hits, err = goDepGraphHits(opts.Root, law); err != nil {
				return Result{}, err
			}
		case KindFileSetContainment:
			if hits, err = containmentHits(opts.Root, law); err != nil {
				return Result{}, err
			}
		case KindJSONNumberCeiling, KindGoBenchCeiling:
			if hits, err = ceilingHits(opts.Root, law, true, cargoTargetDir()); err != nil {
				return Result{}, err
			}
		case KindSymbolRemoved:
			if hits, err = symbolRemovedLawHits(law, resolveBaseTree(opts), opts.Base, scan.files, scan.content, &res); err != nil {
				return Result{}, err
			}
		case KindCoChange:
			if hits, err = changedLawHits(law, opts, scan.files, scan.content, &res,
				func(base BaseReader, changed []string, content map[string]string) ([]Hit, error) {
					return coChangeHits(law, base, changed, content)
				}); err != nil {
				return Result{}, err
			}
		case KindHunkRegex:
			if hits, err = changedLawHits(law, opts, scan.files, scan.content, &res,
				func(base BaseReader, changed []string, content map[string]string) ([]Hit, error) {
					return hunkRegexHits(law, base, changed, content, opts.CommitMessage)
				}); err != nil {
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
		hitsByKey := map[string]Hit{}
		sites := map[string][]string{}
		for _, h := range hits {
			id := baseline.Identity(h.Key)
			measured[id] += h.Weight
			sites[id] = append(sites[id], h.Key)
			if _, seen := located[id]; !seen {
				located[id] = h
			}
			if _, seen := hitsByKey[h.Key]; !seen {
				hitsByKey[h.Key] = h
			}
		}
		for _, keys := range sites {
			sort.Strings(keys)
		}
		// A whole-tree comparison is meaningless over a hypothetical overlay or a
		// narrowed single-file scan -- the pre-edit hook's two uses of Check --
		// so repathing, like tightening itself, is scoped to a real run over
		// the real tree (#490).
		if len(opts.Proposed) == 0 && len(opts.Files) == 0 {
			repathCountedBaseline(opts, baseline, measured)
		}
		baselineKeys := baseline.LiteralKeyCounts()
		findingsBefore := len(res.Findings)
		for _, r := range regressions(baseline, measured, law.Matcher.TolerancePct) {
			h := representativeHit(r.Key, sites[r.Key], hitsByKey, baselineKeys, located)
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
		// A law with at least one finding this run is a caller's candidate for
		// the baseline-history note (#497): a run that reports ANY regression
		// never writes ANY baseline (see commitTightened's guard below), so a
		// caller comparing this file's disk content to HEAD is asking whether
		// the row this law is missing was already missing before this run
		// ever started — the aftermath of an interrupted or otherwise stale
		// write, a hand edit, or a rebase that dropped it, never something
		// this run itself could have done.
		if len(res.Findings) > findingsBefore && law.Baseline != "" {
			res.RegressedBaselines = append(res.RegressedBaselines, RegressedBaseline{
				Law:  law.Name,
				Path: law.Baseline,
				Form: baseline.form,
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
		if tightenBaseline(opts, law, baseline, path, measured, sites) {
			pending = append(pending, pendingTighten{law: law, baseline: baseline, path: path})
		}
	}
	// A run that reports a regression must leave every baseline
	// byte-identical -- even one belonging to an unrelated, perfectly clean
	// law -- so writing waits until every law has been judged (#490).
	if len(res.Findings) == 0 {
		tightened, err := commitTightened(pending)
		if err != nil {
			return Result{}, err
		}
		res.Tightened = tightened
	}
	return res, nil
}

// representativeHit picks the occurrence a regression finding NAMES. keys is
// every literal `<path> | <text>` site this scan found under the regressed
// identity, sorted; baselineKeys is the same law's baseline read as a bag of
// literal keys (LiteralKeyCounts). Subtracting one baseline occurrence for
// every already-recorded site the scan revisits leaves exactly the literal
// keys the baseline has never seen — the first of those, not the first key
// in scan order, is the occurrence that actually caused the regression. Only
// a path-agnostic multiset (several literal keys sharing one identity) can
// disagree with scan order; every other baseline form has one literal key
// per identity, so the subtraction always leaves that same key and this is a
// no-op for it. located is the fallback for the case every site the scan
// found is already accounted for in the baseline (should not arise for a
// real regression, but a missing representative must never panic).
func representativeHit(id string, keys []string, hitsByKey map[string]Hit, baselineKeys map[string]int, located map[string]Hit) Hit {
	remaining := make(map[string]int, len(baselineKeys))
	for k, n := range baselineKeys {
		remaining[k] = n
	}
	for _, k := range keys {
		if remaining[k] > 0 {
			remaining[k]--
			continue
		}
		return hitsByKey[k]
	}
	return located[id]
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
	KindRegexNear:         true,
	KindMarkerInPackage:   true,
	KindDocPathResolves:   true,
	KindHunkRegex:         true,
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
