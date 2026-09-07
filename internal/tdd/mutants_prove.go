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
	// `git diff --numstat` came back empty after the write. Nothing ran, or
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
)

// RunMutantsProve is the hand mutation proof the tdd skill's "existing code"
// step describes in prose: introduce one specific error, watch the named test
// fail, restore byte-identically. Nothing before this verb checked that the
// "introduce one specific error" half actually happened on disk — a pattern
// that silently matched nothing (stale CRLF bytes, a typo, an edit written to
// the wrong worktree, a file already left in the mutated state) left the file
// untouched, and the suite staying green then read as a survivor rather than
// as evidence about nothing at all.
//
// The check is `git diff --numstat` on the file, taken between the write and
// the test run: EOL-independent, and it catches every one of those causes at
// once, because it asks git whether the tree actually changed rather than
// asking why it might not have. It runs AFTER a cheaper, more specific check
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
	var repoRoot, relPath string
	root := FindProjectRoot(absFile)
	if root != "" {
		if rr := RepoRoot(root); rr != "" {
			if rel, err := filepath.Rel(rr, absFile); err == nil {
				repoRoot, relPath = rr, filepath.ToSlash(rel)
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

	numstat, gitErr := git(repoRoot, "diff", "--numstat", "--", relPath)
	if gitErr != nil || strings.TrimSpace(numstat) == "" {
		restore()
		fmt.Fprintf(stderr, "gate: mutants prove refused — git diff --numstat reports no change for %s after the "+
			"mutation; restored, nothing was proved. Common causes: the file is untracked, the edit landed in a "+
			"different worktree, or the tree was already in the mutated state.%s\n", relPath, eolSuffix())
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
	restore()

	if res.TimedOut {
		fmt.Fprintf(stdout, "gate: mutants prove TIMED OUT — %s in %s never reached a verdict; restored, "+
			"not proven either way\n", cmdString(runner), root)
		return ExitMutantsProveTimedOut
	}

	failing := ExtractFailingTests(res.Output)
	matched := ""
	for _, name := range failing {
		if strings.Contains(name, opts.WantFail) {
			matched = name
			break
		}
	}

	switch {
	case !res.Passed && matched != "":
		fmt.Fprintf(stdout, "gate: mutant KILLED — %s failed as predicted (mutation: %q -> %q in %s; "+
			"git diff --numstat: %s)\n", matched, opts.Old, opts.New, relPath, strings.TrimSpace(numstat))
		return ExitMutantsProveKilled
	case !res.Passed:
		fmt.Fprintf(stdout, "gate: mutant WRONG FAILURE — the suite went red but not on %q; failing: %s "+
			"(mutation verified applied via git diff --numstat: %s; restored)\n",
			opts.WantFail, strings.Join(failing, ", "), strings.TrimSpace(numstat))
		return ExitMutantsProveWrongFailure
	default:
		fmt.Fprintf(stdout, "gate: mutant SURVIVED — %s stayed green after a mutation VERIFIED applied "+
			"(git diff --numstat: %s); this is a real survivor, not a no-op — restored\n",
			cmdString(runner), strings.TrimSpace(numstat))
		return ExitMutantsProveSurvived
	}
}
