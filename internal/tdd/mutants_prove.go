package tdd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MutantsProveOptions is one hand mutation proof: the exact text the
// production file is expected to carry (Old) and the one specific error to
// introduce in its place (New), plus the test the mutation is PREDICTED to
// fail before the run ever starts. WantFail is required for the same reason
// the tdd skill's mutation-proof step names the failing line: "something in
// the suite went red" is not evidence a mutation was tested by anything in
// particular, and a generic assertion message makes a wrong-place mutation
// and a real kill read identical. "The test I predicted would fail is the
// one that moved" is (issue #519).
type MutantsProveOptions struct {
	File     string
	Old      string
	New      string
	WantFail string
}

// Exit codes for `aphrollo gate mutants prove`, named so a script — or an
// agent reading only the exit code — can tell every outcome apart without
// parsing prose.
const (
	// ExitMutantsProveKilled: the mutation was verified applied and the named
	// test failed, as predicted. The proof succeeded.
	ExitMutantsProveKilled = 0
	// ExitMutantsProveRefused: the mutation never registered — the pattern
	// matched zero or more than one time, --old equalled --new, or
	// the file's content was the same object after the write. Nothing ran, or
	// what ran proves nothing, because the file was never actually mutated.
	ExitMutantsProveRefused = 1
	// ExitMutantsProveUsage: bad flags.
	ExitMutantsProveUsage = 2
	// ExitMutantsProveSurvived: the mutation WAS verified applied — git saw a
	// real diff — and the suite stayed green anyway. Unlike a refusal, this is
	// a real survivor: the test does not constrain this line.
	ExitMutantsProveSurvived = 3
	// ExitMutantsProveWrongFailure: the mutation was verified applied and the
	// suite went red, but not on the named test — a mutation in the wrong
	// place, or an unrelated failure already in the tree.
	ExitMutantsProveWrongFailure = 4
	// ExitMutantsProveTimedOut: the run never reached a verdict either way.
	ExitMutantsProveTimedOut = 5
	// ExitMutantsProveUnreadable: the mutation was verified applied and the
	// suite went red, but not one failing test NAME could be read out of the
	// run — a build or link failure, or a runner shape the extractor does not
	// know. Distinct from ExitMutantsProveWrongFailure, which asserts the
	// mutation failed some other named test: over evidence that names none,
	// that verdict contradicts itself (#590).
	ExitMutantsProveUnreadable = 6
	// ExitMutantsProveNoTestsSelected: the mutation was verified applied and
	// the run finished — but it SELECTED no tests, so nothing exercised the
	// mutated line. A refusal, and a code of its own rather than the generic
	// one, because the cause is specific and fixable: the filter named a
	// module nothing is under (#637). Never ExitMutantsProveSurvived — a run
	// that tested nothing is no evidence about what the tests constrain, and
	// "survivor" is the reading most likely to be believed and acted on.
	ExitMutantsProveNoTestsSelected = 7
	// ExitMutantsProveScopeUnknown: the mutation was verified applied, the
	// narrowed run stayed green — and which OTHER tests could have killed the
	// mutant could not be established, so there is no way to tell whether the
	// selection that ran was the whole story. Inconclusive, in the family
	// NoTestsSelected and the timeout belong to, and deliberately not
	// ExitMutantsProveSurvived: a survivor claim asserts that nothing kills
	// the line, which a proof that cannot see what reaches the line has no
	// standing to make (#691).
	ExitMutantsProveScopeUnknown = 8
)

// matchWantFail picks the failing test the prediction named, or "" when none
// of them is it. A runner spells a test by its module path
// (`solve_clip_bin::solve_clip_exits_nonzero_on_bad_argv`) while --want-fail
// spells the test, so the match is a `::`-suffix question first. The plain
// substring pass stays last because the flag documents "a unique substring of
// it" — but it must never OUTRANK a real suffix match: the failing set is
// sorted, so an unrelated name that merely contains the want
// (`helpers::…_smoke`) can sort ahead of the predicted one and get reported
// as the killed test (#590).
func matchWantFail(failing []string, want string) string {
	for _, name := range failing {
		if name == want || strings.HasSuffix(name, "::"+want) {
			return name
		}
	}
	for _, name := range failing {
		if strings.Contains(name, want) {
			return name
		}
	}
	return ""
}

// repoRelSlashPath computes the repoRoot-relative slash path to absFile,
// canonicalising both sides through filepath.EvalSymlinks first so a path
// that reaches the same directory through two different spellings — a
// symlink, or on Windows an 8.3 short name (GitHub's Windows runner sets TMP
// to C:\Users\RUNNER~1\...) — still agrees rather than producing a bogus
// filepath.Rel result that walks out of the repository. EvalSymlinks is a
// no-op off Windows and on any path that does not exist yet, so a missing
// path falls back to its original spelling rather than losing the lookup.
// ok is false only when filepath.Rel itself errors (e.g. the two paths name
// different volumes on Windows); a relative path that walks outside repoRoot
// ("../...") is still returned with ok true — the caller decides what an
// escaping path means.
func repoRelSlashPath(repoRoot, absFile string) (relPath string, ok bool) {
	canonRoot, canonFile := repoRoot, absFile
	if resolved, err := filepath.EvalSymlinks(repoRoot); err == nil {
		canonRoot = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absFile); err == nil {
		canonFile = resolved
	}
	rel, err := filepath.Rel(canonRoot, canonFile)
	if err != nil {
		// absence-ok: filepath.Rel errors only when the two paths cannot be
		// made relative at all (e.g. different Windows volumes) — the sole
		// caller already treats ok=false identically to "not a git repo" by
		// leaving repoRoot unset, unchanged from the inline `err == nil`
		// check this replaces.
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// mutationDiffVerdict is what the landing check below found: a real change
// (the mutation registered), a relPath that never named a location inside
// repoRoot at all, a git invocation failure, or git running cleanly and
// reporting nothing to diff.
type mutationDiffVerdict int

const (
	mutationDiffChanged mutationDiffVerdict = iota
	mutationDiffEscapesRepo
	mutationDiffGitFailed
	mutationDiffNoChange
)

// classifyMutationDiff separates three situations `gitErr != nil ||
// strings.TrimSpace(numstat) == ""` used to collapse into a single "reports
// no change" message: relPath resolving outside repoRoot (a caller/resolution
// mistake, checked first — git reporting nothing for a pathspec outside the
// repository is not evidence the file did not change), the git invocation
// itself failing, and git running fine inside the repo and reporting nothing.
// A caller cannot tell these apart from one message, and the collapsed
// message asserts the third even when it was the first (issue #360's
// class: absence returned from an error branch).
func classifyMutationDiff(relPathEscapesRepo bool, gitErr error, evidence string) mutationDiffVerdict {
	switch {
	case relPathEscapesRepo:
		return mutationDiffEscapesRepo
	case gitErr != nil:
		return mutationDiffGitFailed
	case strings.TrimSpace(evidence) == "":
		return mutationDiffNoChange
	default:
		return mutationDiffChanged
	}
}

// RunMutantsProve is the hand mutation proof the tdd skill's "existing code"
// step describes in prose: introduce one specific error, watch the named test
// fail, restore byte-identically. Nothing before this verb checked that the
// "introduce one specific error" half actually happened on disk — a pattern
// that silently matched nothing (stale CRLF bytes, a typo, an edit written to
// the wrong worktree, a file already left in the mutated state) left the file
// untouched, and the suite staying green then read as a survivor rather than
// as evidence about nothing at all.
//
// The check (mutationLandedEvidence) hashes the content git reads for the
// file from the proof's starting bytes and again after the write, between the
// write and the test run: EOL-independent, blind to unstaged work already in
// the file, and it catches every one of those causes at once, because it asks
// whether the content actually changed rather than asking why it might not
// have. It runs AFTER a cheaper, more specific check
// (the replace itself must match --old exactly once) that names the same
// class of failure without a subprocess.
//
// The file is restored byte-identically on every exit path once it has been
// written at all — refused, killed, survived, wrong-failure or timed out.
func RunMutantsProve(opts MutantsProveOptions, run SuiteRunner, stdout, stderr io.Writer) int {
	absFile, err := filepath.Abs(opts.File)
	if err != nil {
		fmt.Fprintf(stderr, "gate: mutants prove refused — cannot resolve %q: %v\n", opts.File, err)
		return ExitMutantsProveRefused
	}
	origBytes, err := os.ReadFile(absFile)
	if err != nil {
		fmt.Fprintf(stderr, "gate: mutants prove refused — cannot read %s: %v\n", absFile, err)
		return ExitMutantsProveRefused
	}
	orig := string(origBytes)

	// repoRoot/relPath are resolved up front — before the pattern is even
	// checked — so a refusal below this point can name a stale CRLF/attribute
	// mismatch as a likely CAUSE alongside its own, EOL-independent reason.
	// "" for either just means no such note is available; it never changes
	// what the refusal itself checks or reports.
	//
	// rr (from `git rev-parse --show-toplevel`) and absFile (from the caller,
	// via filepath.Abs) can spell the SAME directory two different ways on
	// Windows: GitHub's Windows runner sets TMP to an 8.3 short name
	// (C:\Users\RUNNER~1\...), and only one of the two sides may have gone
	// through it. filepath.Rel compares strings, not filesystem identity, so
	// a spelling mismatch produces a bogus multi-level "../" path that
	// escapes the repo entirely rather than an error — EvalSymlinks resolves
	// an 8.3 component to its long form on Windows and is a no-op elsewhere,
	// so canonicalising both sides through it before Rel makes them agree. A
	// path that does not exist (rare here, since the mutation is already
	// written) falls back to its original spelling rather than losing the
	// lookup.
	var repoRoot, relPath string
	var relPathEscapesRepo bool
	root := FindProjectRoot(absFile)
	if root != "" {
		if rr := RepoRoot(root); rr != "" {
			if rel, ok := repoRelSlashPath(rr, absFile); ok {
				repoRoot, relPath = rr, rel
				relPathEscapesRepo = !filepath.IsLocal(filepath.FromSlash(rel))
			}
		}
	}
	eolSuffix := func() string {
		if repoRoot == "" {
			return ""
		}
		if note := eolDriftNote(repoRoot, relPath); note != "" {
			return " Possibly related: " + note
		}
		return ""
	}

	if opts.Old == opts.New {
		fmt.Fprintf(stderr, "gate: mutants prove refused — --old and --new are identical for %s: "+
			"a no-op by construction, git diff would never move\n", absFile)
		return ExitMutantsProveRefused
	}
	switch count := strings.Count(orig, opts.Old); {
	case count == 0:
		fmt.Fprintf(stderr, "gate: mutants prove refused — --old matched 0 matches in %s; "+
			"check for a stale CRLF/line-ending mismatch or a typo — nothing was written, nothing was proved.%s\n",
			absFile, eolSuffix())
		return ExitMutantsProveRefused
	case count > 1:
		fmt.Fprintf(stderr, "gate: mutants prove refused — --old matched %d times in %s; "+
			"a mutation proof must target exactly one location — narrow --old until it is unique\n", count, absFile)
		return ExitMutantsProveRefused
	}

	perm := os.FileMode(0o644)
	if fi, err := os.Stat(absFile); err == nil {
		perm = fi.Mode().Perm()
	}
	restore := func() {
		_ = os.WriteFile(absFile, origBytes, perm)
	}

	mutated := strings.Replace(orig, opts.Old, opts.New, 1)
	if err := os.WriteFile(absFile, []byte(mutated), perm); err != nil {
		fmt.Fprintf(stderr, "gate: mutants prove refused — cannot write the mutation to %s: %v\n", absFile, err)
		return ExitMutantsProveRefused
	}

	if root == "" {
		restore()
		fmt.Fprintf(stderr, "gate: mutants prove refused — no project root found above %s; restored, nothing was run\n", absFile)
		return ExitMutantsProveRefused
	}
	if repoRoot == "" {
		restore()
		fmt.Fprintf(stderr, "gate: mutants prove refused — %s is not inside a git repository (or is outside it); "+
			"restored, nothing was run\n", root)
		return ExitMutantsProveRefused
	}
	// A caller mistake (the file genuinely lives outside repoRoot), not a
	// missing change — reported as such rather than folded into the "no
	// change" message below, which would otherwise assert the file did not
	// change when it was never even in scope for git to report on.
	if relPathEscapesRepo {
		restore()
		fmt.Fprintf(stderr, "gate: mutants prove refused — %s resolves outside the repository at %s (relative path "+
			"%q escapes it); restored, nothing was proved\n", absFile, repoRoot, relPath)
		return ExitMutantsProveRefused
	}

	landed, gitErr := mutationLandedEvidence(repoRoot, relPath, origBytes)
	switch classifyMutationDiff(relPathEscapesRepo, gitErr, landed) {
	case mutationDiffEscapesRepo:
		// Unreachable here — the earlier relPathEscapesRepo check already
		// returned — but classifyMutationDiff stays the single place this
		// three-way split is decided, so a future caller that skips the
		// early check still gets the right message instead of "no change".
		restore()
		fmt.Fprintf(stderr, "gate: mutants prove refused — %s resolves outside the repository at %s (relative path "+
			"%q escapes it); restored, nothing was proved\n", absFile, repoRoot, relPath)
		return ExitMutantsProveRefused
	case mutationDiffGitFailed:
		restore()
		fmt.Fprintf(stderr, "gate: mutants prove refused — cannot tell whether the mutation changed %s: %v\n", relPath, gitErr)
		return ExitMutantsProveRefused
	case mutationDiffNoChange:
		restore()
		fmt.Fprintf(stderr, "gate: mutants prove refused — the mutation left the content git reads for %s unchanged "+
			"(the same object before and after the write), so nothing was mutated; restored, nothing was proved. "+
			"An edit the repository's line-ending attributes or clean filters normalise away is not a mutation.%s\n",
			relPath, eolSuffix())
		return ExitMutantsProveRefused
	}

	runner, ok := DetectRunner(root)
	if !ok {
		restore()
		fmt.Fprintf(stderr, "gate: mutants prove refused — no known test runner under %s; restored\n", root)
		return ExitMutantsProveRefused
	}
	runner = NarrowToRelatedTests(runner, absFile, root)
	res := run(runner, root)
	// A green NARROWED run is not yet a survivor, in either language: `--lib`
	// plus a module filter cannot reach a test in an integration binary, and
	// `go test ./<dir>` runs the mutated file's own package and no other, so
	// the one selection that could have killed the mutant may never have been
	// compiled (#691). Widen once, and judge on the wider run — the runner
	// and result below are the widened pair whenever there was anything to
	// widen to, so the verdict, the failing names read out of it and the
	// retained run all describe the same selection.
	narrow := runner
	wider := widenSurvivorSelection(run, runner, root, res)
	runner, res, widened := wider.runner, wider.res, wider.outcome
	restore()

	// The reach could not be established, so whether this selection was the
	// whole story is unknown. Inconclusive, never a survivor.
	if widened == widenUnknown {
		fmt.Fprint(stdout, scopeUnknownAdvisory(narrow, relPath, wider.why))
		return retainProveRun(root, narrow, res, ExitMutantsProveScopeUnknown)
	}

	if res.TimedOut {
		fmt.Fprintf(stdout, "gate: mutants prove TIMED OUT — %s in %s never reached a verdict; restored, "+
			"not proven either way\n", cmdString(runner), root)
		return retainProveRun(root, runner, res, ExitMutantsProveTimedOut)
	}

	// An empty SELECTION is judged before the pass/fail question, because it
	// answers neither: the run finished, but nothing it ran touched the
	// mutated line. Left to the switch below, a green empty run reads as a
	// SURVIVOR (#637's false survivor: the reading most likely to be believed,
	// and the one that sends someone to write a test that already exists) and
	// a runner that exits non-zero over an empty selection reads as an
	// unreadable red. This branch is kept even though #637's known cause — a
	// filter derived from a file's stem rather than from the `#[path]` that
	// mounts it — is fixed: it is the half that holds for every FUTURE cause
	// of an empty selection, and the filter it names is what makes the next
	// one diagnosable.
	//
	// The predicate is the post-edit half's own (emptyselection.go, #642), so
	// "nothing ran" means one thing across the gate rather than two that can
	// drift; what differs is the VERDICT. Post-edit widens once and reports an
	// inconclusive run, because the session can simply run again. A proof
	// cannot: its whole claim is about tests that ran, so an empty selection
	// ends it.
	if proveSelectedZeroTests(runner, res) {
		hint := "The filter matched no test at all: check it against the module path the tests are really " +
			"under (a Rust `#[path = \"…\"] mod <name>;` mounts a file under <name>, not under its own file stem)."
		if runner.Cmd == "go" {
			hint = "No test in the mutated package or in any package whose tests import it ran."
		}
		if widened == widenDone {
			hint = fmt.Sprintf("It was already widened from %s, which selected none either: no test in "+
				"this package's reach exercises the file.", cmdString(narrow))
		}
		fmt.Fprintf(stderr, "gate: mutants prove refused — %s: %s in %s selected zero tests, so nothing "+
			"exercised the mutation; restored, nothing was proved — this is NOT a survivor. %s\n",
			strings.ToUpper(NoTestsSelected), cmdString(runner), root, hint)
		return retainProveRun(root, runner, res, ExitMutantsProveNoTestsSelected)
	}

	failing := ExtractFailingTests(res.Output)
	matched := matchWantFail(failing, opts.WantFail)

	switch {
	case !res.Passed && matched != "":
		fmt.Fprintf(stdout, "gate: mutant KILLED — %s failed as predicted (mutation: %q -> %q in %s; "+
			"mutation landed: %s)%s\n", matched, opts.Old, opts.New, relPath, landed,
			widenedProveNote(narrow, widened))
		return retainProveRun(root, runner, res, ExitMutantsProveKilled)
	case !res.Passed && len(failing) == 0:
		fmt.Fprintf(stdout, "gate: mutant UNREADABLE — unreadable red run: no failing test name could be read "+
			"from the output, so nothing is proved either way about %q (mutation verified applied via "+
			"%s; restored). Usually a build or link failure, or a runner shape the "+
			"extractor does not know — the run's own text says which, and `aphrollo gate output` serves "+
			"it: this proof's run is the record kept for this root.\n",
			opts.WantFail, landed)
		return retainProveRun(root, runner, res, ExitMutantsProveUnreadable)
	case !res.Passed:
		fmt.Fprintf(stdout, "gate: mutant WRONG FAILURE — the suite went red but not on %q; failing: %s "+
			"(mutation verified applied: %s; restored)\n",
			opts.WantFail, strings.Join(failing, ", "), landed)
		return retainProveRun(root, runner, res, ExitMutantsProveWrongFailure)
	default:
		fmt.Fprintf(stdout, "gate: mutant SURVIVED — %s stayed green after a mutation VERIFIED applied "+
			"(%s); this is a real survivor, not a no-op — restored.%s\n",
			cmdString(runner), landed, widenedProveNote(narrow, widened))
		return retainProveRun(root, runner, res, ExitMutantsProveSurvived)
	}
}
