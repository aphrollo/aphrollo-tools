// Command flakehunt reads a `go test -json` stream, finds every test that
// FAILED at least once, and files (or updates) one GitHub issue per failing
// test. See main.go for the CLI; this file is the pure JSON-event parser.
package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// Failure is one test that failed at least once in the run: its top-level
// name (a failing subtest is folded up to the test function that owns it,
// since that is the unit `-run '^<Test>$'` reproduces), the package it lives
// in, the `-shuffle=on` seed the package's test binary ran under, and the
// most specific failure excerpt seen for it.
type Failure struct {
	Test    string
	Package string
	Seed    string
	Excerpt string
}

// Result is everything Parse learns from one `go test -json` stream: the
// failing tests to file issues for, and the packages that never got to run a
// single test because the build itself failed — a `go vet`/compile error, not
// a flake, and the caller's signal to fail the job instead of filing anything.
type Result struct {
	Failures      []Failure
	BuildFailures []string
}

// testEvent is one line of `go test -json` output. Two shapes share the
// stream: a per-test event carries Time/Package/Test/Action/Output, and a
// build failure carries ImportPath instead of Package and no Time — both
// unmarshal into the same struct, the fields that do not apply left zero.
type testEvent struct {
	Action     string `json:"Action"`
	Package    string `json:"Package"`
	ImportPath string `json:"ImportPath"`
	Test       string `json:"Test"`
	Output     string `json:"Output"`
}

// shuffleSeedPrefix is the exact text `go test -shuffle=on` writes to the
// package's own Output stream, once, before the first test runs.
const shuffleSeedPrefix = "-test.shuffle "

// topLevelTest folds a subtest's full name ("TestGroup/case2") up to the test
// function GitHub's issue and the reproduce command both address
// ("TestGroup") — `-run '^TestGroup$'` re-runs every subtest under it, so the
// fold loses no reproducibility.
func topLevelTest(name string) string {
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[:i]
	}
	return name
}

// Parse reads a `go test -json` stream and reports every test that failed at
// least once and every package whose build failed outright.
//
// A malformed line (a stray non-JSON warning some runtime crash wrote to the
// same stream) is skipped rather than treated as an error: the lines around
// it are still real signal, and a caller that wants "the build itself never
// produced valid output" already has BuildFailures and the exit code go test
// itself reported for that.
func Parse(r io.Reader) (Result, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	seedByPackage := map[string]string{}
	excerptByKey := map[string][]string{}
	failureByKey := map[string]*Failure{}
	var order []string
	var buildFailures []string
	seenBuildFailure := map[string]bool{}

	for scanner.Scan() {
		var ev testEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		pkg := ev.Package
		if pkg == "" {
			pkg = ev.ImportPath
		}

		switch ev.Action {
		case "build-fail":
			name := strings.TrimSuffix(pkg, ".test")
			if name != "" && !seenBuildFailure[name] {
				seenBuildFailure[name] = true
				buildFailures = append(buildFailures, name)
			}
		case "run":
			if ev.Test != "" {
				excerptByKey[pkg+"\x00"+ev.Test] = nil
			}
		case "output":
			switch {
			case ev.Test != "":
				key := pkg + "\x00" + ev.Test
				excerptByKey[key] = append(excerptByKey[key], ev.Output)
			case strings.HasPrefix(strings.TrimSpace(ev.Output), shuffleSeedPrefix):
				seedByPackage[pkg] = strings.TrimPrefix(strings.TrimSpace(ev.Output), shuffleSeedPrefix)
			}
		case "fail":
			if ev.Test == "" {
				// Either the whole-package summary after its own tests
				// already failed individually (nothing new to learn), or a
				// build failure's own package-level echo — build-fail above
				// is the definitive signal for that case.
				continue
			}
			top := topLevelTest(ev.Test)
			key := pkg + "\x00" + top
			isSubtest := top != ev.Test
			excerpt := strings.Join(excerptByKey[pkg+"\x00"+ev.Test], "")
			f, seen := failureByKey[key]
			if !seen {
				f = &Failure{Test: top, Package: pkg}
				failureByKey[key] = f
				order = append(order, key)
			}
			// A subtest's own excerpt is always the most specific thing this
			// test can say; the parent's synthesized "--- FAIL: Top" fail
			// event (emitted right after) carries no detail and must never
			// clobber it. First failure of any kind still gets recorded, so
			// a top-level-only test (no subtests) is never left empty.
			if isSubtest || f.Excerpt == "" {
				f.Excerpt = excerpt
				f.Seed = seedByPackage[pkg]
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Result{}, err
	}

	failures := make([]Failure, 0, len(order))
	for _, key := range order {
		failures = append(failures, *failureByKey[key])
	}
	return Result{Failures: failures, BuildFailures: buildFailures}, nil
}
