package tdd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Outcome is the classified result of a test run after an edit. It is the
// signal PostToolUse reports back to the model. The vocabulary is deliberately
// small: enough to tell "keep going" from "you broke something" from "your
// test can't fail", without the speculative sub-categories that made the
// original classifier brittle.
type Outcome string

const (
	Green              Outcome = "green"               // passed, clean output
	GreenUnconstrained Outcome = "green-unconstrained" // passed, but no test came with the source edit
	GreenWithWarnings  Outcome = "green-with-warnings" // passed, warnings present
	WritingTest        Outcome = "writing-test"        // passed but NO tests actually ran (scaffolding)
	RedMissingImpl     Outcome = "red-missing-impl"    // failed: the symbol under test is undefined (clean RED)
	RedBogus           Outcome = "red-bogus"           // failed: test setup is broken (syntax/import/collection)
	Red                Outcome = "red"                 // failed: a plain assertion failure
	NoDelta            Outcome = "no-delta"            // failed, but only with pre-existing failures
)

// IsRed reports whether the outcome is actionable failure the agent should see.
// PostToolUse is silent unless the outcome IsRed, so green/writing-test/no-delta
// runs add no noise.
func (o Outcome) IsRed() bool {
	return strings.HasPrefix(string(o), "red")
}

// zeroTestsRe recognises a passing run in which no test actually executed —
// the single most important false-NEGATIVE fix. The original only caught
// "collected 0 items"/"no test files", so vitest's "0 tests" and a bare
// "0 passed" were stamped GREEN, hiding scaffolding that was never exercised.
// The `all 0 tests? passed` alternative is Zig's no-tests-ran summary:
// `zig test` prints `All 0 tests passed.` when a file/step executed none.
// `\b0 tests?\b` already covers the bare count, but the phrase is kept
// explicit so the Zig signal is legible.
//
// Authority split with vacuousGoPackages below: zeroTestsRe is the ONLY
// signal at post-edit (the WritingTest advisory, every runner, never
// blocking) because post-edit has no structured per-package data to read —
// a phrase guess is all there is. At the commit/merge stages, for Go
// specifically, vacuousGoPackages is authoritative and zeroTestsRe plays no
// part in that decision at all: a phrase match is a text pattern a test's
// own output could imitate, while a package-level PASS with no per-test
// event behind it is a fact `go test -json` states about itself. Neither
// function calls the other; this comment is the only place the split is
// recorded, so read it before changing either.
var zeroTestsRe = regexp.MustCompile(`(?i)no tests? (?:found|to run|ran|executed)|no test files|collected 0 items|\b0 tests?\b|\btests?:\s+0\b|testing: warning: no tests to run|all 0 tests? passed`)

// goTestEvent is the subset of `go test -json`'s per-line event schema this
// package reads. Package is empty for a build-output/build-fail event
// (which carries ImportPath instead, not read here — those precede any
// Package-scoped event and only matter to a run that never got to build,
// which res.Passed already excludes from every caller below). Test is empty
// for a package-level event, set for a per-test one.
type goTestEvent struct {
	Action  string
	Package string
	Test    string
	Output  string
}

// vacuousGoPackages returns the sorted, de-duplicated set of packages whose
// `go test -json` stream shows a package-level PASS (Action=="pass", Test
// unset — go test's own "ok" verdict for that package) with not one
// per-test PASS/FAIL/SKIP event behind it: the #194 shape, attributed to
// the exact package it happened in.
//
// Package attribution matters because judgement over the whole run's
// concatenated TEXT cannot tell packages apart: one sibling package's real
// "--- PASS" line anywhere in a multi-package run's output used to make
// the entire run read as non-vacuous, even with another package's TestMain
// never calling m.Run() right beside it — inert on exactly the multi-package
// case #194 was (`go test ./...` is DetectRunner's Go default). `-json`
// carries an explicit Package field on every event, so this reads that fact
// directly rather than inferring package boundaries from interleaved text —
// parallel package execution interleaves the plain-text stream, so a
// text-segmentation approach would be guessing at a boundary the data
// already states outright.
//
// A package with no test files at all reports Action=="skip" at the
// package level, never "pass" — go test's own distinction — so it is
// excluded without a "no test files" text guess.
//
// A stream that stops with io.EOF is the normal, complete end of a `go test
// -json` run and is not an error. Any OTHER decode failure — a truncated
// write from a killed process, an interleaved non-JSON line — means this
// function cannot tell what it did not get to see, so it returns an error
// rather than judging the partial prefix it decoded so far: reporting on
// half a stream as though it were the whole thing would silently
// under-report a vacuous package hiding in the unread remainder, which is
// exactly the "unmeasured run reads as a pass" shape #317 exists to refuse.
// The caller (runSuiteStage's check-error path, failFirstViolatedAt's
// vacuous path) treats "could not read the stream" as its own block rather
// than folding it into "wrote a clean pass".
func vacuousGoPackages(rawJSON string) ([]string, error) {
	ranPkgs := map[string]bool{}
	testedPkgs := map[string]bool{}
	dec := json.NewDecoder(strings.NewReader(rawJSON))
	for {
		var e goTestEvent
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("vacuousGoPackages: reading go test -json stream: %w", err)
		}
		if e.Package == "" {
			continue
		}
		if e.Test != "" {
			switch e.Action {
			case "pass", "fail", "skip":
				testedPkgs[e.Package] = true
			}
			continue
		}
		if e.Action == "pass" {
			ranPkgs[e.Package] = true
		}
	}
	var vacuous []string
	for pkg := range ranPkgs {
		if !testedPkgs[pkg] {
			vacuous = append(vacuous, pkg)
		}
	}
	sort.Strings(vacuous)
	return vacuous, nil
}

// isGoTestInvocation reports whether cmd/args is a `go test ...` command —
// the shape goExecArgs arms and RunSuite reads a JSON stream from.
func isGoTestInvocation(cmd string, args []string) bool {
	return cmd == "go" && len(args) > 0 && args[0] == "test"
}

// ensureGoTestArg inserts flag right after "test" in a `go test` argv,
// unless it is already present — idempotent so a caller that already named
// the flag (or a second pass over the same args) never doubles it.
func ensureGoTestArg(args []string, flag string) []string {
	for _, a := range args {
		if a == flag {
			return args
		}
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, args[0], flag)
	out = append(out, args[1:]...)
	return out
}

// goExecArgs is RunSuite's one place every `go test` invocation is finalized
// right before it actually executes, regardless of which detector or
// narrowing built the Runner. It inserts two flags, for two different
// reasons:
//
//   - -json carries the human-readable text RunSuite reconstructs into
//     SuiteResult.Output (it already implies -v's per-test verbosity —
//     confirmed against the toolchain: a bare -json run emits the same
//     "=== RUN"/"--- PASS" lines a -v run would, inside each event's Output
//     field) AND the structured stream vacuousGoPackages needs, from one
//     invocation.
//   - -count=1 defeats go's own test-result cache. A cached PASS is a
//     record of an EARLIER tree, not a measurement of the one on disk right
//     now, and this is the one seam every "go test" the gate runs —
//     post-edit advisory, fail-first, and the mechanical suite — passes
//     through on its way to exec.CommandContext, so adding it here is what
//     makes "-count=1 belongs anywhere the gate claims to have tested the
//     current tree" (issue #421) true without touching every
//     Runner-construction call site. The mechanical stage (withGoCIParity)
//     also names -count=1 explicitly among its own CI-parity flags; the
//     idempotent insert here is a deliberate no-op in that case, not a
//     second flag.
//
// Idempotent in both flags, and a no-op for anything that is not
// `go test ...`.
func goExecArgs(cmd string, args []string) []string {
	if !isGoTestInvocation(cmd, args) {
		return args
	}
	args = ensureGoTestArg(args, "-json")
	args = ensureGoTestArg(args, "-count=1")
	return args
}

// renderGoTestJSON reconstructs the plain-text stream a `-v` run would have
// printed (concatenating each "output"/"build-output" event's own Output
// field, in stream order) alongside the untouched raw JSON, from one
// `go test -json` capture. The reconstruction is what lets every existing
// text-based consumer (ExtractFailingTests, zeroTestsRe, blockedVerdict's
// diagnostic scan) read SuiteResult.Output exactly as before, unaware the
// invocation changed at all; rawJSON exists ONLY so vacuousGoPackages can
// attribute a pass to the package that produced it. ok is false when raw
// parses as NO recognisable event at all (an unexpected toolchain, a
// completely garbled stream) — the caller must then fall back to raw as
// Output and treat this run as having no package-level data to check.
func renderGoTestJSON(raw string) (humanOutput, rawJSON string, ok bool) {
	dec := json.NewDecoder(strings.NewReader(raw))
	var b strings.Builder
	seen := false
	for {
		var e goTestEvent
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// A real decode error partway through the stream (a killed
			// process, an interleaved non-JSON write) is not the same as a
			// clean end-of-input: whatever was decoded before it is a
			// PARTIAL reconstruction, and presenting it as complete could
			// silently drop a failing test's own output from the tail this
			// never got to read. The raw stream is the same honest fallback
			// the whole-stream failure below already returns.
			return raw, "", false
		}
		seen = true
		if e.Action == "output" || e.Action == "build-output" {
			b.WriteString(e.Output)
		}
	}
	if !seen {
		return raw, "", false
	}
	return b.String(), raw, true
}

// goRenderedOutput is RunSuite's one call into this file: raw is the
// subprocess's captured stdout+stderr for cmd/args exactly as spawned
// (already carrying -json for a go test invocation, via goExecArgs). For
// anything else it passes raw straight through with no GoTestJSON. A stream
// renderGoTestJSON cannot parse at all falls back to raw as Output too,
// rather than handing SuiteResult.Output a broken reconstruction.
func goRenderedOutput(cmd string, args []string, raw string) (output, testJSON string) {
	if !isGoTestInvocation(cmd, args) {
		return raw, ""
	}
	if human, rawJSON, ok := renderGoTestJSON(raw); ok {
		return human, rawJSON
	}
	return raw, ""
}

// warningRe marks otherwise-clean output as carrying warnings.
var warningRe = regexp.MustCompile(`(?i)\bwarning:|\bdeprecat|\bunused (?:variable|import)\b`)

// setupErrRe matches a broken test SETUP — syntax, import, or collection
// errors — which means "fix the test, not the implementation". It is
// intentionally narrow: an ambiguous failure falls through to a plain Red
// rather than being mislabeled (the audit's Go-testdata / Python-import FPs).
// The Zig alternatives catch a structural compile failure (`zig build test`
// emits `error: expected <token>` for a parse/type error, and a `referenced
// by:` trail under a propagated @compileError) — distinct from a clean
// missing-symbol RED, which missingImplRe catches below. setupErrRe is checked
// first, so it must NOT match Zig's undeclared-identifier / no-member output
// (both end in the generic `error: N compilation errors`, deliberately not
// keyed on here).
var setupErrRe = regexp.MustCompile(`(?i)syntaxerror|indentationerror|importerror|modulenotfounderror|error collecting|cannot find module|transform failed|\berror ts\d+\b|error: expected |referenced by:`)

// missingImplRe matches the canonical clean-RED signal: the symbol under test
// does not exist yet. This is the expected first step of a TDD cycle.
// The Zig alternatives are its undefined-symbol phrasings: `use of undeclared
// identifier` (a bare name with no decl), `use of undefined identifier` (older
// wording, kept for forward/back compat), and `has no member named` (a missing
// field/decl on a struct, e.g. the library root) — all the clean-RED "write
// the impl next" signal. The Rust alternatives are rustc's missing-symbol
// diagnostics: `cannot find function/value/…` (E0425/E0412), `no method named`
// (E0599), and `use of undeclared crate or module` (E0433). There is
// deliberately no bare `no such` alternative: a runtime "no such file or
// directory" in an assertion message is a plain failure, not a missing
// implementation.
var missingImplRe = regexp.MustCompile(`(?i)undefined: |is not defined|has no attribute|cannot find name|cannot find (?:function|value|struct|type|trait|macro|method)|no method named|undeclared name|use of undeclared (?:identifier|crate or module)|use of undefined identifier|has no member named`)

// linkFailureRes are the signatures of a build that died at the LINK step
// rather than at a test. A linker never sees an assertion: it reports the
// symbols the object files do or do not carry, so its failure describes the
// artifacts on disk — a stale one, a half-written sibling crate, another
// session's concurrent build — as readily as it describes anything the edit
// did. That is only ever a REASON to look further, never a verdict on its
// own: foreignBuildFailure pairs it with the crate the failure names.
var linkFailureRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)rust-lld: error:`),
	regexp.MustCompile(`(?i)error: linking with `),
	regexp.MustCompile(`(?i)undefined symbols? for architecture`),
}

// couldNotCompileRe captures the crate rustc gave up on. cargo prints one
// such line per crate it could not finish, and the name in it is the only
// place the output states WHOSE failure this is.
var couldNotCompileRe = regexp.MustCompile("(?m)^\\s*error: could not compile `([^`]+)`")

// foreignBuildFailure names the crates a failing run could not link or
// compile when the crate this edit touched is not among them — the shape
// issue #593 reported: a docs-only change harvested against a shared tree
// ran a suite that died with `rust-lld: error: undefined symbol` in a crate
// the session had never opened, and the gate called it RED. It is nil (judge
// the run normally) whenever the failure could be this edit's: no link-step
// signature at all, no crate named, or the edited crate among the names.
//
// edited is the [package] name owning the edited file. "" — no cargo package
// owns it, or the caller could not say — answers nil in every case: a run
// this cannot attribute to a crate is one it cannot call foreign either, and
// downgrading an unattributable failure would hide a red that IS the
// session's. Conservative in the direction that keeps failures visible.
func foreignBuildFailure(output, edited string) []string {
	if edited == "" || !matchesAny(linkFailureRes, output) {
		return nil
	}
	seen := map[string]bool{}
	var crates []string
	for _, m := range couldNotCompileRe.FindAllStringSubmatch(output, -1) {
		name := m[1]
		if name == edited {
			return nil
		}
		if !seen[name] {
			seen[name] = true
			crates = append(crates, name)
		}
	}
	sort.Strings(crates)
	return crates
}

// matchesAny reports whether output matches at least one of the patterns.
func matchesAny(res []*regexp.Regexp, output string) bool {
	for _, re := range res {
		if re.MatchString(output) {
			return true
		}
	}
	return false
}

// foreignBuildAdvisory is the ONE place a foreign link failure becomes a
// verdict, for the foreground run, the same-hook deferred phase and the
// later harvest alike — three paths that reach ClassifyOutcome from three
// different places and would otherwise each decide this for themselves. "" —
// judge the run normally — for a pass, for a failure this edit's crate is
// named in, and for anything with no crate to attribute at all. It logs the
// InfraFailed verdict it returns, so the stand-down is counted, not just
// printed.
func foreignBuildAdvisory(root, target, runnerText string, res SuiteResult) string {
	if res.Passed {
		return ""
	}
	crates := foreignBuildFailure(res.Output, editedCargoPackage(root, target))
	if len(crates) == 0 {
		return ""
	}
	appendGateLog("postedit", root, runnerText, InfraFailed, res.Duration)
	return foreignBuildFailureLine(root, crates, res.Duration)
}

// editedCargoPackage is the [package] name owning the edited file, "" when
// no cargo package does (a Go or JS project, a doc file under a virtual
// workspace manifest, a path that cannot be related to root at all).
func editedCargoPackage(root, target string) string {
	if target == "" {
		return ""
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "" // absence-ok: a path that cannot be related to root owns no package
	}
	return cargoPackageFor(root, filepath.ToSlash(rel))
}

// foreignBuildFailureLine is what that run reports instead of a RED summary:
// the InfraFailed family, named by the crate whose build actually failed, so
// the session reads "not your edit, and nothing was tested" rather than going
// to fix a crate it never wrote to.
func foreignBuildFailureLine(root string, crates []string, dur time.Duration) string {
	return fmt.Sprintf("gate: → %s in %s (the build failed to link %s, which this edit did not change, in %.1fs — the code was NOT tested)",
		InfraFailed, root, strings.Join(crates, ", "), dur.Seconds())
}

// ClassifyOutcome maps a test run to an Outcome. prevFailing is the failing-test
// set recorded after the previous edit, used to recognise that a still-failing
// run introduced NO new failures (no-delta) so the agent is not nagged about
// pre-existing breakage.
//
// A PASSING run is never RED here — not even a freshly edited test that passes
// immediately. Post-edit cannot know whether the implementation already existed
// (backfilling coverage, splitting a case, and refactoring a test all pass
// legitimately), so "passed test ⇒ tautology" would be a guess that fires
// constantly on a mature codebase, breaking the silent-on-green contract. The
// authoritative fail-first check is precommit, which runs the new tests in a
// worktree WITHOUT the new source and can actually tell.
func ClassifyOutcome(passed bool, output string, prevFailing []string) Outcome {
	if passed {
		switch {
		case zeroTestsRe.MatchString(output):
			return WritingTest
		case warningRe.MatchString(output):
			return GreenWithWarnings
		default:
			return Green
		}
	}

	// The failing-set delta is judged BEFORE the regex classes: a run whose
	// failures were all already failing is NoDelta no matter what its message
	// text matches. A pre-existing failure often carries missing-impl/setup-error
	// phrasing ("has no attribute", "ImportError: …"), and relabeling it
	// red-missing-impl on every unrelated edit nags the agent about breakage it
	// did not cause. A run with no parseable failing names (e.g. a compile error)
	// never qualifies as NoDelta, so fresh clean-RED signals keep their class.
	if len(prevFailing) > 0 && noNewFailures(ExtractFailingTests(output), prevFailing) {
		return NoDelta
	}

	switch {
	case setupErrRe.MatchString(output):
		return RedBogus
	case goUndefinedIsMissingImportOnly(output):
		return RedBogus
	case missingImplRe.MatchString(output):
		return RedMissingImpl
	}
	return Red
}

// noNewFailures reports whether every currently-failing test was already
// failing before this edit (curr ⊆ prev).
func noNewFailures(curr, prev []string) bool {
	if len(curr) == 0 {
		return false // a failed run with no parsed names is not provably pre-existing
	}
	prevSet := make(map[string]bool, len(prev))
	for _, p := range prev {
		prevSet[p] = true
	}
	for _, c := range curr {
		if !prevSet[c] {
			return false
		}
	}
	return true
}

// failLineRes extract failing test names across the supported runners. Each is
// anchored to a per-line failure marker; the vitest/jest form strips a trailing
// `(123 ms)` duration WITHOUT truncating a name that itself contains
// parentheses (the audit's non-greedy-capture fix).
var failLineRes = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^\s*--- FAIL:\s+(\S+)`),                     // go test
	regexp.MustCompile(`(?m)^FAILED\s+(\S+::\S+)`),                      // pytest: FAILED path::test
	regexp.MustCompile(`(?m)^(\S+::\S+)\s+FAILED`),                      // pytest: path::test FAILED
	regexp.MustCompile(`(?m)^\s*[✗×]\s+(.+?)(?:\s+\(\d+\s*m?s\))?\s*$`), // vitest/jest
	regexp.MustCompile(`(?m)^test\s+(\S+)\s+\.\.\.\s+FAILED`),           // cargo (libtest)
	// cargo nextest: FAIL [ 0.4s] <binary-id> <module::name>, optionally with
	// an `(n/m)` progress counter after the duration. Everything before the
	// LAST field is context — the counter, and a binary id that for an
	// integration test is itself `::`-qualified (`pose_ik::integration`) — so
	// the name is read from the end of the line, not at a fixed offset from
	// the duration (#590). The whitespace classes are HORIZONTAL only: Go's
	// `\s` matches `\n`, so a `$`-anchored tail written with `\s` runs past
	// the end of its own line and captures the last word of the summary.
	//
	// FAIL is not the only status a failing test is spelled with, and which
	// one a repo sees is decided by its nextest PROFILE rather than by the
	// test (#666): a `slow-timeout` with `terminate-after` reports TIMEOUT, a
	// test process that dies instead of failing an assertion reports ABORT on
	// Windows and its signal name (SIGSEGV, SIGABRT, …) elsewhere, and under
	// `retries` a failing test is never spelled bare FAIL at all — every
	// line, the run's own final summary included, reads `TRY <n> FAIL`.
	// Reading only FAIL is what let a red run that named its failing test
	// three times be reported as unreadable. Statuses that do NOT mean the
	// test failed stay out by construction: PASS, SLOW, LEAK and the
	// TERMINATING progress line all carry the same shape.
	regexp.MustCompile(`(?m)^[^\S\n]*(?:TRY[^\S\n]+\d+[^\S\n]+)?(?:FAIL|TIMEOUT|ABORT|SIG[A-Z]+)[^\S\n]+\[[^\]]*\][^\S\n]+(?:\S+[^\S\n]+)*(\S+)[^\S\n]*$`),
	regexp.MustCompile(`(?m)^\s*error: '([^']+)' failed:`), // zig build test
}

// ExtractFailingTests returns the sorted, de-duplicated set of failing test
// names found in runner output. Sorting makes the set stable for delta
// comparison across runs.
func ExtractFailingTests(output string) []string {
	seen := map[string]bool{}
	for _, re := range failLineRes {
		for _, m := range re.FindAllStringSubmatch(output, -1) {
			if name := strings.TrimSpace(m[1]); name != "" {
				seen[name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
