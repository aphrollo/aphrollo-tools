package suite

import (
	"regexp"
	"sort"
	"strconv"
)

// #412 (cargo half): the counterpart to vacuousGoPackages (classify.go), over
// libtest's own summary line instead of `go test -json`'s structured stream —
// cargo has no equivalent machine-readable stream this gate reads today, so
// this parses the text libtest already prints for every target.
//
// Ground truth (measured against the real toolchain, not guessed): a target
// with genuinely zero #[test] items prints
//
//	test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s
//
// and a target whose NAME FILTER matched none of its real tests prints the
// identical zero passed/failed/ignored line but with filtered_out > 0:
//
//	test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 109 filtered out; finished in 0.00s
//
// filtered_out is the ONLY count that distinguishes them from the outside —
// it is cargo's own tally of tests that existed but were excluded by the
// filter, so a target report zero of everything else WITH filtered_out>0
// means a real test target lost every one of its tests to a filter mismatch
// (vacuousFailFirstMessage's exact "name/filter mismatch" shape) rather than
// never having any. ignored tests (ignored>0, ignored via #[ignore]) count
// toward "executed": libtest itself decided to skip them, the same way a Go
// per-test skip event still marks its package tested (classify.go's
// vacuousGoPackages) — skipped is not the same as not executed (#412).
var cargoTestResultRe = regexp.MustCompile(
	`(?m)^test result: (?:ok|FAILED)\. (\d+) passed; (\d+) failed; (\d+) ignored; \d+ measured; (\d+) filtered out;`)

// cargoRunningRe and cargoDocTestsRe are the two header shapes libtest prints
// immediately before a target's own result block, used only for ATTRIBUTING
// a vacuous verdict to a name in the block message — mirroring
// vacuousGoPackages' per-package naming. Neither participates in the
// pass/fail decision itself.
var (
	cargoRunningRe  = regexp.MustCompile(`(?m)^\s*Running(?:\s+unittests)?\s+(\S+)\s+\(`)
	cargoDocTestsRe = regexp.MustCompile(`(?m)^\s*Doc-tests\s+(\S+)`)
)

// cargoTargetNamer builds the "which target was this result block printed
// under" lookup: the name of the nearest Running/Doc-tests header BEFORE a
// given offset, "unknown target" when none precedes it (a header this gate
// has not seen a toolchain omit, but text is text). Shared with
// cargoSkippedOnlyTargets, which attributes a different verdict off the same
// blocks and must name them identically.
func cargoTargetNamer(output string) func(pos int) string {
	type header struct {
		pos  int
		name string
	}
	var headers []header
	for _, m := range cargoRunningRe.FindAllStringSubmatchIndex(output, -1) {
		headers = append(headers, header{pos: m[0], name: output[m[2]:m[3]]})
	}
	for _, m := range cargoDocTestsRe.FindAllStringSubmatchIndex(output, -1) {
		headers = append(headers, header{pos: m[0], name: output[m[2]:m[3]]})
	}
	sort.Slice(headers, func(i, j int) bool { return headers[i].pos < headers[j].pos })
	return func(pos int) string {
		name := "unknown target"
		for _, h := range headers {
			if h.pos >= pos {
				break
			}
			name = h.name
		}
		return name
	}
}

// cargoVacuousTargets returns the sorted, de-duplicated set of target names
// (attributed from the nearest preceding "Running"/"Doc-tests" header, or
// "unknown target" when none precedes it — a header this gate has not seen a
// toolchain omit, but text is text) whose libtest summary reports zero
// passed and zero failed while filtered_out is nonzero: a real test target
// that lost every one of its tests to a filter, not a target with none.
func cargoVacuousTargets(output string) []string {
	nameBefore := cargoTargetNamer(output)
	var vacuous []string
	seen := map[string]bool{}
	for _, m := range cargoTestResultRe.FindAllStringSubmatchIndex(output, -1) {
		passed, _ := strconv.Atoi(output[m[2]:m[3]])
		failed, _ := strconv.Atoi(output[m[4]:m[5]])
		filteredOut, _ := strconv.Atoi(output[m[8]:m[9]])
		if passed != 0 || failed != 0 || filteredOut == 0 {
			continue
		}
		name := nameBefore(m[0])
		if !seen[name] {
			seen[name] = true
			vacuous = append(vacuous, name)
		}
	}
	sort.Strings(vacuous)
	return vacuous
}
