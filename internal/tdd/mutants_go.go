package tdd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// gremlins is the Go half of the runner, invoked by MeasureLane exactly as
// cargo-mutants is invoked for a Cargo repo, and its report read into the
// same outcome shape.
//
// The tool was chosen by measurement rather than argument:
// go-mutesting does not BUILD on this box (its osutil dependency uses
// syscall.Dup and RLIMIT_NOFILE, neither of which exists on Windows), so its
// wall time is not a number that exists. gremlins builds, scopes to a diff
// (--diff), writes machine-readable results (--output) and takes a worker cap
// (--workers) — the three things this design needs from a mutation tool. On
// internal/tdd its analysis pass found 1626 runnable mutants (85.76% mutator
// coverage) in 2.6 s, behind one full coverage run of the module.

// gremlinsBin is the tool this runner drives. Resolved from PATH: a box
// without it reaches no verdict and says so rather than passing silently.
const gremlinsBin = "gremlins"

// gremlinsArgv is one diff-scoped run: mutate only what the lane changed,
// write the machine-readable report, and stay inside the worker cap. gremlins
// re-runs the package's tests once per mutant, so an uncapped run owns the box
// for as long as it takes.
//
// excludeFiles is repo-relative paths the run must not even WALK — gremlins'
// own `--exclude-files` takes a filepath regexp (exclusion.Rules in internal/exclusion,
// matched against the fs.WalkDir path gremlins mutates from), so each one is
// anchored and escaped into `^<path>$` before being passed. This is the CI
// runner's incremental lever (issue #143): a file whose blob and package
// fence are unchanged since the last measured push is excluded here rather
// than re-mutated, while `--diff` keeps scoping which LINES are mutable
// within whatever gremlins does walk. gremlins takes exactly one positional
// path (cobra.MaximumNArgs(1)) — "./..." makes it walk nothing, report no
// results and exit 0 — so narrowing happens through exclusion, never through
// a second positional argument.
func gremlinsArgv(baseSHA, outPath string, workers int, excludeFiles []string) []string {
	if workers < 1 {
		workers = 1
	}
	argv := []string{"unleash", "--silent",
		"--diff", baseSHA,
		"--output", outPath,
		"--workers", strconv.Itoa(workers),
	}
	for _, f := range excludeFiles {
		argv = append(argv, "--exclude-files", "^"+regexp.QuoteMeta(filepath.ToSlash(f))+"$")
	}
	// A PATH, not a package pattern: gremlins walks the tree from here.
	return append(argv, ".")
}

// gremlinsFileReport is the shape gremlins writes with --output, captured from
// a real run (testdata/gremlins_report.json). Its own totals are NOT read: the
// captured run reported 0 for every total while listing three mutants, so the
// counts here come from the list itself.
type gremlinsFileReport struct {
	FileName  string `json:"file_name"`
	Mutations []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"mutations"`
}

// parseGremlinsReport reads one run's mutants out of its report.
func parseGremlinsReport(data []byte) ([]MutantOutcome, error) {
	var report struct {
		Files []gremlinsFileReport `json:"files"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	var out []MutantOutcome
	for _, f := range report.Files {
		for _, m := range f.Mutations {
			if gremlinsStatus(m.Status) == gremlinsSkipped {
				continue
			}
			file := filepath.ToSlash(f.FileName)
			out = append(out, MutantOutcome{
				File:     file,
				Line:     m.Line,
				Col:      m.Column,
				Mutation: m.Type,
				Name:     mutantLineOf(file, m.Line, m.Column, m.Type),
				Status:   gremlinsStatus(m.Status),
			})
		}
	}
	sortOutcomes(out)
	return out, nil
}

// gremlinsStatus maps gremlins' vocabulary onto the receipt's. NOT COVERED is
// a MISS: "no test runs this code at all" is the strongest version of the
// thing a survivor reports. Anything unrecognised is unviable — never caught,
// because a gate that reads an unknown status as a pass is not a gate.
func gremlinsStatus(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "KILLED":
		return "caught"
	case "LIVED":
		return "missed"
	case "NOT COVERED":
		return gremlinsNotCovered
	case "TIMED OUT":
		return "timeout"
	case "SKIPPED":
		return gremlinsSkipped
	default:
		return "unviable"
	}
}

// gremlinsNotCovered is a mutant gremlins never ran a test for, because its
// coverage profile had no block at the mutant's position. That is NOT the
// claim a survivor makes ("a test ran and did not notice") and must not refuse
// a merge on its own.
//
// The mapping is not reliable enough to read as "no test covers this". Go's
// coverage blocks split at a closure, so for
//
//	return strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) < 0
//
// gremlins calls the two mutants inside the closure RUNNABLE and the outer
// `< 0` after it NOT COVERED, although that comparison plainly executes and a
// hand-mutation of it fails a test. On Windows the mapping fails wholesale:
// 4890 NOT COVERED, mutator coverage 0.00%.
//
// So it is counted and reported, never silently dropped, and never confused
// with a measured survivor.
const gremlinsNotCovered = "notcovered"

// gremlinsSkipped is the report's word for a mutant the run's own --diff scope
// left out. It is not an outcome: the report lists every mutant the ANALYSIS
// found, and on a lane diff that is thousands of them against a handful the
// run actually measured.
const gremlinsSkipped = "skipped"

// acceptEntryGroup is every accept-list entry written for one file:line
// mutator combination. cargo-mutants and gremlins both regularly emit
// several distinct mutants on one line with identical mutator text — a real
// accept-list carries entries for creator.rs:108 at columns 5, 33 and 71
// side by side (issue #282) — so a group holds any number of column-specific
// entries plus at most one column-less Fallback.
type acceptEntryGroup struct {
	ByCol    map[int]acceptEntry
	Fallback *acceptEntry
}

// splitAcceptedSurvivors divides survivors by an already-parsed accept-list
// (acceptedMutants), tallying the KIND each matched entry claims alongside
// the accepted/unaccepted split. ambiguous names every column-less entry that
// matched a line carrying more than one distinct mutant: such an entry
// cannot tell its reviewed mutant apart from an unreviewed sibling, so it is
// refused rather than applied to any of them — the caller is expected to
// report each one (judgeMutants passes them to measureReport, the same way a
// bad accept-kind entry is quoted).
func splitAcceptedSurvivors(list map[string]acceptEntryGroup, survivors []MutantOutcome) (accepted, unaccepted []MutantOutcome, kinds AcceptKindCounts, ambiguous []string) {
	sameLine := make(map[string]int, len(survivors))
	for _, m := range survivors {
		sameLine[survivorKey(m.File, m.Line, m.Mutation)]++
	}
	warned := map[string]bool{}
	for _, m := range survivors {
		key := survivorKey(m.File, m.Line, m.Mutation)
		group, found := list[key]
		if !found {
			unaccepted = append(unaccepted, m)
			continue
		}
		if entry, ok := group.ByCol[m.Col]; ok {
			accepted = append(accepted, m)
			kinds.add(entry.Kind)
			continue
		}
		if group.Fallback == nil {
			unaccepted = append(unaccepted, m)
			continue
		}
		if sameLine[key] > 1 {
			if !warned[key] {
				ambiguous = append(ambiguous, fmt.Sprintf(
					"column-less entry for %s admits %d same-line mutants — add a column to disambiguate", key, sameLine[key]))
				warned[key] = true
			}
			unaccepted = append(unaccepted, m)
			continue
		}
		accepted = append(accepted, m)
		kinds.add(group.Fallback.Kind)
	}
	return accepted, unaccepted, kinds, ambiguous
}

func survivorKey(file string, line int, mutation string) string {
	return filepath.ToSlash(file) + ":" + strconv.Itoa(line) + " " + strings.TrimSpace(mutation)
}

// acceptEntryLocationWithColRe and acceptEntryLocationRe parse the location
// half of an accept-list key, tried in this order so "file:108:33" is never
// misread as file "file:108" at line 33: the three-group pattern is tried
// FIRST, and only a location with exactly two trailing colon-digit groups
// matches it.
var (
	acceptEntryLocationWithColRe = regexp.MustCompile(`^(.+):(\d+):(\d+)$`)
	acceptEntryLocationRe        = regexp.MustCompile(`^(.+):(\d+)$`)
)

// parseAcceptEntryLocation splits "<file>:<line>" or "<file>:<line>:<col>"
// into its parts. ok is false when loc matches neither shape.
func parseAcceptEntryLocation(loc string) (file string, line, col int, hasCol, ok bool) {
	if m := acceptEntryLocationWithColRe.FindStringSubmatch(loc); m != nil {
		l, lerr := strconv.Atoi(m[2])
		c, cerr := strconv.Atoi(m[3])
		if lerr == nil && cerr == nil {
			return m[1], l, c, true, true
		}
	}
	if m := acceptEntryLocationRe.FindStringSubmatch(loc); m != nil {
		if l, err := strconv.Atoi(m[2]); err == nil {
			return m[1], l, 0, false, true
		}
	}
	return "", 0, 0, false, false
}

// acceptedMutants reads the accept-list, mutation-accept under [aphrollo] in
// aphrollo.toml. An entry reads "<file>:<line> <MUTATOR> # why it is
// acceptable" or, to name one mutant among several sharing a line,
// "<file>:<line>:<col> <MUTATOR> # why". The reason is not decoration: an
// accept-list nobody had to justify is a list of survivors somebody
// silenced, so an entry with no reason at all is dropped rather than kept.
//
// bad names every entry whose reason DID carry a "kind=" directive that
// failed to parse — a misspelled kind, or a runner/capability kind missing
// its required test=/issue= evidence — or whose location half parsed as
// neither shape above, verbatim, so the caller can refuse it loudly instead
// of letting it read as an ordinary equivalence claim.
func acceptedMutants(root string) (list map[string]acceptEntryGroup, bad []string) {
	return parseAcceptedMutants(tomlStringsIn(filepath.Join(root, "aphrollo.toml"), "[aphrollo]", mutantsAcceptKey))
}

// parseAcceptedMutants is acceptedMutants over entries somebody else read:
// MeasureLane takes the list off MutantsConfig, which resolves both the Cargo
// and the aphrollo.toml spelling of the table, and must key it exactly the
// way the reader above does.
func parseAcceptedMutants(entries []string) (list map[string]acceptEntryGroup, bad []string) {
	list = map[string]acceptEntryGroup{}
	for _, raw := range entries {
		key, reason, ok := strings.Cut(raw, "#")
		reason = strings.TrimSpace(reason)
		if !ok || reason == "" {
			continue
		}
		kind, evidence, kindOK := parseAcceptKind(reason)
		if !kindOK {
			bad = append(bad, raw)
			continue
		}
		loc, mutation, ok := strings.Cut(strings.TrimSpace(key), " ")
		if !ok {
			bad = append(bad, raw)
			continue
		}
		file, line, col, hasCol, locOK := parseAcceptEntryLocation(loc)
		if !locOK {
			bad = append(bad, raw)
			continue
		}
		groupKey := survivorKey(file, line, mutation)
		entry := acceptEntry{Kind: kind, Evidence: evidence}
		group := list[groupKey]
		if hasCol {
			if group.ByCol == nil {
				group.ByCol = map[int]acceptEntry{}
			}
			group.ByCol[col] = entry
		} else {
			group.Fallback = &entry
		}
		list[groupKey] = group
	}
	return list, bad
}
