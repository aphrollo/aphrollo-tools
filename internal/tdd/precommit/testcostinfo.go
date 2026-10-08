package precommit

import (
	"fmt"
	"sort"
	"strings"

	langtable "github.com/aphrollo/aphrollo-tools/internal/lang"
	"github.com/aphrollo/aphrollo-tools/internal/testcost"
)

// testCostDoc is where the info line sends a reader: what makes a test
// expensive, and what to do about it.
const testCostDoc = "https://github.com/aphrollo/aphrollo-tools/blob/main/docs/test-cost.md"

// testCostNamed bounds how many tests one info line names; the rest are
// counted.
const testCostNamed = 3

// testCostInfoLine is the one non-blocking line a commit that adds a slow test
// gets, "" when there is none to say. A test the commit adds is slow when the
// suite this commit just ran (first) or the merges on record (history) put it
// at testcost.DefaultThresholdSecs or more. With no timing for any added test
// it says nothing. history is read only when a commit adds a test and the run
// just made did not time it: the record is the whole event log.
func testCostInfoLine(repoRoot string, first testcost.Run, history func() []testcost.Run) string {
	type slow struct {
		name string
		secs float64
	}
	var found []slow
	var recorded []testcost.Run
	read := false
	for _, name := range addedTestNames(repoRoot) {
		secs, ok := testcost.Lookup([]testcost.Run{first}, name)
		if !ok {
			if !read {
				recorded, read = history(), true
			}
			secs, ok = testcost.Lookup(recorded, name)
		}
		if ok && secs >= testcost.DefaultThresholdSecs {
			found = append(found, slow{name, secs})
		}
	}
	if len(found) == 0 {
		return ""
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].secs > found[j].secs })
	var parts []string
	for _, f := range found[:min(len(found), testCostNamed)] {
		parts = append(parts, fmt.Sprintf("%s %gs", f.name, f.secs))
	}
	list := strings.Join(parts, ", ")
	if extra := len(found) - testCostNamed; extra > 0 {
		list += fmt.Sprintf(" and %d more", extra)
	}
	return fmt.Sprintf(
		"[info] slow new test: %s (over %gs). Usual causes: a real git repo or subprocess per test, a sleep or poll, a binary built per test. See %s",
		list, testcost.DefaultThresholdSecs, testCostDoc)
}

// addedTestNames is the tests the staged change declares that HEAD's version of
// the same file did not, by the language table's test patterns.
func addedTestNames(repoRoot string) []string {
	table, err := langtable.ForRoot(repoRoot)
	if err != nil {
		return nil
	}
	var added []string
	for _, file := range stagedFiles(repoRoot) {
		row, ok := table.For(file)
		if !ok {
			continue
		}
		now, err := git(repoRoot, "show", ":"+file)
		if err != nil {
			continue
		}
		had := map[string]bool{}
		if before, err := git(repoRoot, "show", "HEAD:"+file); err == nil {
			for _, n := range row.TestNames(before) {
				had[n] = true
			}
		}
		for _, n := range row.TestNames(now) {
			if !had[n] {
				added = append(added, n)
			}
		}
	}
	return added
}
