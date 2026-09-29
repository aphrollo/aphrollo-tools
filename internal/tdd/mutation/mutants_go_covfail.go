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
// line — a compile failure names a package and no test, and
// coverageRunFailedPackages reads that one.
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

// goTestPackageFailRe matches go test's per-package verdict line for a
// package that failed: "FAIL\t<pkg>\t<duration>" for a failing test binary,
// "FAIL\t<pkg> [build failed]" or "[setup failed]" for one that never ran.
// The bare "FAIL" summary line carries no tab and no package, so it never
// matches.
var goTestPackageFailRe = regexp.MustCompile(`(?m)^FAIL\t(\S+)`)

// coverageRunFailedPackages names every package go test's own output reports
// as failed, sorted. go test prints one verdict line per package, so the
// list carries no duplicate.
func coverageRunFailedPackages(output string) []string {
	var pkgs []string
	for _, m := range goTestPackageFailRe.FindAllStringSubmatch(output, -1) {
		pkgs = append(pkgs, m[1])
	}
	sort.Strings(pkgs)
	return pkgs
}

// coverageBoxCauses are the texts go test and the kernel print when the BOX,
// not the code, broke a test binary or a build: a process killed by a signal
// (the OOM killer, a reaper), a full drive, no memory for a fork, the
// process limit reached. Each still ends in go test's per-package FAIL line,
// so the package line alone cannot say whose fault the failure was; these
// can.
var coverageBoxCauses = []string{
	"signal: killed",
	"no space left on device",
	"cannot allocate memory",
	"resource temporarily unavailable",
}

// coverageBoxCause is the first box-side cause the gather's output carries,
// empty when it carries none.
func coverageBoxCause(output string) string {
	for _, c := range coverageBoxCauses {
		if strings.Contains(output, c) {
			return c
		}
	}
	return ""
}

// goCoverageNoVerdict is measureNoVerdictOrTreeChanged's Go-lane sibling: the
// same tree-damage check first (that fact is more urgent than any other),
// then the coverage-gather diagnosis this file exists for, and only then the
// generic no-verdict refusal every other unexplained gremlins failure still
// gets.
//
// Where the gather's failure came from decides the verdict (issue #966). go
// test names every package it failed; when the output also carries a
// box-side cause (coverageBoxCauses) the box broke the run and the lane is
// NOT MEASURED, which blocks nothing. Otherwise the failing packages are the
// lane's own tree — a build error, a failing test, a generated file the
// manifest does not explain — and the measurement is refused, naming them:
// a lane that cannot pass its own suite must never pass as unmeasured.
func goCoverageNoVerdict(root, logDir string, code int, cause error, before worktreeSnapshot, output string, log io.Writer) Verdict {
	if v, refused := refuseIfTreeChanged(root, before, log); refused {
		logf(log, "mutants: the run also exited %d and reached no verdict", code)
		return v
	}
	pkgs := coverageRunFailedPackages(output)
	tests := coverageRunFailedTests(output)
	if len(pkgs) == 0 && len(tests) == 0 {
		return measureNoVerdict(root, logDir, code, cause, log)
	}
	if box := coverageBoxCause(output); box != "" {
		why := fmt.Sprintf("the coverage run failed on this box (%s), not on the lane's code", box)
		return measureUnmeasured(root, why, "coverage-run-box", log)
	}
	return refuseCoverageRun(root, pkgs, tests, log)
}

// refuseCoverageRun is the refusal for a lane whose own tree failed the
// coverage gather: the packages go test failed, the tests it named, and the
// remedy.
func refuseCoverageRun(root string, pkgs, tests []string, log io.Writer) Verdict {
	var b strings.Builder
	b.WriteString("mutants: REFUSED — the coverage run failed on this lane's own tree, so no mutant could be judged")
	if len(pkgs) != 0 {
		b.WriteString("\nmutants: failing packages: " + strings.Join(pkgs, ", "))
	}
	if len(tests) != 0 {
		b.WriteString("\nmutants: failing tests: " + strings.Join(tests, ", "))
	}
	b.WriteString("\nmutants: make `go test ./...` pass in this checkout (a build error, a failing test, a file " +
		"missing from a manifest), then measure again")
	msg := b.String()
	logf(log, "%s", msg)
	AppendGateLog("mutants", measureLogRoot(root), "mutants", "mutants-refused:coverage-run-failed", 0)
	return Verdict{Refused: true, Message: msg}
}
