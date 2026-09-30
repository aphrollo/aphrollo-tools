package ratchet

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// maxListedFiles is how many files one finding's line names; the rest are
// counted, and the JSON form of the finding carries every one.
const maxListedFiles = 5

// FileCount is one file's share of a finding's excess.
type FileCount struct {
	File  string `json:"file"`
	Count int    `json:"count"`
}

// RegressionCount is the work a run's findings stand for, in lines: a finding
// that carries an excess is that many occurrences over its baseline, any other
// is one. len(Findings) counts KEYS, and a text-keyed baseline holds a whole
// workspace's occurrences of one text under one key.
func (r Result) RegressionCount() int {
	n := 0
	for _, f := range r.Findings {
		n += max(f.Excess, 1)
	}
	return n
}

// excessOf is how far a regressed identity sits over its ceiling and where the
// occurrences that no baseline row accounts for are. A key-per-file baseline
// names one file already, so only the text-keyed and exact-multiset forms
// carry an excess: those count occurrences of one identity, possibly across
// many files. sites is every literal `<path> | <text>` this scan found under
// the identity and baselineKeys the baseline read as a bag of literal keys;
// subtracting one baseline occurrence per site the scan revisits leaves the
// sites the baseline has never seen, and those are the files named.
func excessOf(form Form, r Regression, sites []string, hitsByKey map[string]Hit, baselineKeys map[string]int) (int, []FileCount) {
	if form == Counted {
		return 0, nil
	}
	remaining := make(map[string]int, len(baselineKeys))
	for k, n := range baselineKeys {
		remaining[k] = n
	}
	perFile := map[string]int{}
	for _, k := range sites {
		if remaining[k] > 0 {
			remaining[k]--
			continue
		}
		perFile[hitsByKey[k].File]++
	}
	files := make([]FileCount, 0, len(perFile))
	for f, n := range perFile {
		files = append(files, FileCount{File: f, Count: n})
	}
	slices.SortFunc(files, func(a, b FileCount) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(a.File, b.File))
	})
	return r.Measured - r.Baseline, files
}

// excessText is the clause a finding's line carries for its excess: how many,
// in how many files, and the first few of them. One occurrence in one file is
// what the line's own location already says, so it gets no clause.
func excessText(f Finding) string {
	if f.Excess <= 1 && len(f.Files) <= 1 {
		return ""
	}
	noun := "files"
	if len(f.Files) == 1 {
		noun = "file"
	}
	listed := f.Files[:min(len(f.Files), maxListedFiles)]
	var parts []string
	for _, fc := range listed {
		parts = append(parts, fmt.Sprintf("%s %d", fc.File, fc.Count))
	}
	if more := len(f.Files) - len(listed); more > 0 {
		parts = append(parts, fmt.Sprintf("+%d more", more))
	}
	return fmt.Sprintf(" — %d over its baseline in %d %s: %s", f.Excess, len(f.Files), noun, strings.Join(parts, ", "))
}
