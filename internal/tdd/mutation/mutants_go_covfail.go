package mutation

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// gremlins' coverage gather is a bare `go test -cover ./...` with no
// -timeout (mutants_go.go), run before it ever mutates a line. When the
// module's own tests are red at HEAD, that gather dies and gremlins writes
// no --output report at all — the run reaches os.ReadFile(out) with nothing
// to read. Read as a generic measureNoVerdict, that refusal names an exit
// code and a log directory and says nothing about WHY, which reads as the
// mutation tool's own fault rather than the tree's. --silent is dropped from
// gremlinsArgv for exactly this reason (issue #704): this run's own captured
// output carries go test's failure verbatim, and this file is what reads it
// back out.

// goTestFailRe matches go test's own "--- FAIL: <test> (<duration>)" line,
// the one line its output always carries once per failing test regardless
// of how many packages or subtests failed.
var goTestFailRe = regexp.MustCompile(`(?m)^\s*--- FAIL: (\S+)`)

// coverageRunFailedTests names every test go test's own output blames a
// failure on, sorted and de-duplicated. Empty when output carries no such
// line — a coverage gather can die for reasons go test itself never printed
// a test name for (a compile failure, a killed process), and those stay a
// generic no-verdict refusal rather than an empty, uninformative "coverage
// run failed ()".
func coverageRunFailedTests(output string) []string {
	matches := goTestFailRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// goCoverageNoVerdict is measureNoVerdictOrTreeChanged's Go-lane sibling: the
// same tree-damage check first (that fact is more urgent than any other), then
// the coverage-gather diagnosis this file exists for, and only then the
// generic no-verdict refusal every other unexplained gremlins failure still
// gets. Unlike a refusal, a coverage-run diagnosis is reported as NOT
// MEASURED — gremlins never reached the tree at all, so there is nothing here
// for a merge to be refused over.
func goCoverageNoVerdict(root, logDir string, code int, cause error, before worktreeSnapshot, output string, log io.Writer) Verdict {
	if v, refused := refuseIfTreeChanged(root, before, log); refused {
		logf(log, "mutants: the run also exited %d and reached no verdict", code)
		return v
	}
	if failing := coverageRunFailedTests(output); len(failing) > 0 {
		why := fmt.Sprintf("coverage run failed (%s)", strings.Join(failing, ", "))
		return measureUnmeasured(root, why, "coverage-run-failed", log)
	}
	return measureNoVerdict(root, logDir, code, cause, log)
}
