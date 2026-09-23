package mutation

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cargo-mutants mutates the tree IN PLACE. That is the whole reason the run
// is cheap — no 135 MB copy per mutant, no cold rebuild of the world — and it
// is also the one way this stage can damage the thing it is judging: the tool
// restores each mutation as it finishes with it, so a run that is killed, or
// that dies on a full drive, leaves the last one in the source. Nothing
// noticed. The merge that followed would have merged a mutant.
//
// So the tree is checked, not trusted. Two rules:
//
//	compared with ITSELF — the patch before the run against the patch after,
//	   never against a clean tree: at pre-merge-commit the worktree carries
//	   the whole merge result and is legitimately dirty, so "is it clean" is
//	   the wrong question and would refuse every merge.
//	never restored — the refusal names the files and the exact command, and
//	   stops. A tool that rewrites source on its way out is not one anybody
//	   can reason about mid-incident, and the operator may want to look at
//	   what was left behind before it goes.

// worktreeSnapshot is what the tracked files looked like at one moment,
// as the patch against HEAD. ok is false when git could not say, in which
// case nothing is judged: this side's own blind spot must not refuse a merge.
type worktreeSnapshot struct {
	patch string
	ok    bool
	// failed carries what git said when it could not answer. "Could not
	// say" is not "did not change": a corrupt index, a repo that vanished
	// under the run or a killed git would otherwise wave through exactly
	// the tree this stage exists to check.
	failed string
}

// snapshotWorktree records the tracked working tree as one patch. Untracked
// files are deliberately out of scope — the run's own mutants.out, build
// directory and diff file all land there, and counting them would make every
// run report itself as having changed the tree.
func snapshotWorktree(root string) worktreeSnapshot {
	// STDOUT alone is the patch. git prints ADVICE on stderr — "warning: in
	// the working copy of 'src/lib.rs', LF will be replaced by CRLF the next
	// time Git touches it" is what a box with core.autocrlf on says the
	// first time it looks at a file the run rewrote — and advice is not a
	// change to the tree. Read as part of the patch it made the after
	// snapshot differ from the before one with no content difference at all,
	// and refused a measurement whose every mutant had been caught, naming
	// no files and printing an empty --stat.
	out, errText, err := gitDiffOutFn(root, "diff")
	if err != nil {
		return worktreeSnapshot{failed: gitFailureText(errText, err)}
	}
	return worktreeSnapshot{patch: out, ok: true}
}

// gitFailureText is what to tell the operator when git could not answer: its
// own stderr when it wrote any, and the exec error when it did not (a git
// that never started prints nothing at all).
func gitFailureText(stderr string, err error) string {
	if text := strings.TrimSpace(stderr); text != "" {
		return text
	}
	return err.Error()
}

// gitDiffOutFn is the seam every diff that decides this verdict goes
// through: one call, both streams kept apart, so a test can say what git
// printed where.
var gitDiffOutFn = gitDiffOut

// gitDiffOut runs git in dir with the same scrubbed environment the rest of
// this package uses, and answers stdout and stderr SEPARATELY.
func gitDiffOut(dir string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command(gitBinary(), args...)
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err = cmd.Run()
	return out.String(), errBuf.String(), err
}

// setGitDiffOutForTest replaces that seam for one test.
func setGitDiffOutForTest(fn func(dir string, args ...string) (string, string, error)) (restore func()) {
	prev := gitDiffOutFn
	gitDiffOutFn = fn
	return func() { gitDiffOutFn = prev }
}

// refuseIfTreeChanged compares the tree with what it looked like before the
// run and refuses when the run left it different. It is asked on every exit
// path a measurement can take, including a no-verdict exit and the lone
// re-run, because those are the paths a killed run actually takes.
func refuseIfTreeChanged(root string, before worktreeSnapshot, log io.Writer) (Verdict, bool) {
	after := snapshotWorktree(root)
	if v, refused := refuseIfGitFailed(root, before, after, log); refused {
		return v, true
	}
	if !before.ok || !after.ok || before.patch == after.patch {
		return Verdict{}, false
	}
	files := changedBetweenPatches(before.patch, after.patch)
	var b strings.Builder
	b.WriteString("mutants: the run left the working tree changed:\n")
	if stat, _, err := gitDiffOutFn(root, "diff", "--stat"); err == nil {
		b.WriteString(strings.TrimRight(stat, "\n") + "\n")
	}
	b.WriteString("mutants: cargo-mutants restores each mutation as it finishes with it, so a mutation still in the tree " +
		"means the run was killed part-way — merging this merges a mutant.\n")
	if len(files) > 0 {
		b.WriteString("mutants: put them back and measure again — git checkout -- " + strings.Join(files, " "))
	} else {
		b.WriteString("mutants: put the tree back and measure again")
	}
	msg := b.String()
	logf(log, "%s", msg)
	AppendGateLog("mutants", measureLogRoot(root), "mutants", "mutants-refused:tree-changed", 0)
	return Verdict{Refused: true, Message: msg}, true
}

// refuseIfGitFailed stops a measurement whose tree nobody could read. A
// snapshot that failed answers neither "changed" nor "unchanged", and the
// silent version of that — treating it as nothing to compare — is the one
// answer that must never be given: it passes the merge on the strength of a
// check that did not run. Either end of the comparison is enough, since one
// missing end leaves nothing to compare against.
func refuseIfGitFailed(root string, before, after worktreeSnapshot, log io.Writer) (Verdict, bool) {
	said := before.failed
	if said == "" {
		said = after.failed
	}
	if said == "" {
		return Verdict{}, false
	}
	msg := "mutants: refused — git could not read the working tree, so the tree the run measured cannot be " +
		"compared with the one it started from: " + said
	logf(log, "%s", msg)
	AppendGateLog("mutants", measureLogRoot(root), "mutants", "mutants-refused:git-failed", 0)
	return Verdict{Refused: true, Message: msg}, true
}

// changedBetweenPatches names the files whose own hunks differ between two
// snapshots — the ones the RUN touched, not every file the merge already
// carried. Sorted, so the remedy is the same command whatever order git
// listed them in.
func changedBetweenPatches(before, after string) []string {
	old, now := patchByFile(before), patchByFile(after)
	var files []string
	for path, patch := range now {
		if old[path] != patch {
			files = append(files, path)
		}
	}
	for path := range old {
		if _, still := now[path]; !still {
			files = append(files, path)
		}
	}
	sortStrings(files)
	return files
}

// patchByFile splits a unified diff into one patch per file, keyed on the
// path git names it by. A `diff --git a/<x> b/<y>` line starts each section,
// and the b-side is the path as it stands now, which is the one a restore
// command has to name — diffHeaderPath (escape_verify.go) already reads it,
// space-bearing paths and all.
func patchByFile(patch string) map[string]string {
	out := map[string]string{}
	path, section := "", &strings.Builder{}
	flush := func() {
		if path != "" {
			out[path] = section.String()
		}
		section = &strings.Builder{}
	}
	for line := range strings.Lines(patch) {
		if p, ok := diffHeaderPath(line); ok {
			flush()
			path = p
		}
		section.WriteString(line)
	}
	flush()
	return out
}

// measureNoVerdictOrTreeChanged is the refusal for a run that reached no
// verdict, preferring the tree damage when there is any: "the run exited 1"
// is a fact about the run, and "it left a mutation in your source" is a fact
// about the repository, which is the more urgent of the two.
func measureNoVerdictOrTreeChanged(root, logDir string, code int, cause error, before worktreeSnapshot, log io.Writer) Verdict {
	if v, refused := refuseIfTreeChanged(root, before, log); refused {
		logf(log, "mutants: the run also exited %d and reached no verdict", code)
		return v
	}
	return measureNoVerdict(root, logDir, code, cause, log)
}

// worktreeChangedPaths lists the repo-relative paths that differ between a
// base revision and the WORKING TREE — no second revision operand, which is
// the whole point.
//
// A lane checkout answers with the lane's own commits, exactly as a
// base..HEAD diff would. A pre-merge-commit hook does not: it fires before
// .git/MERGE_HEAD is written, HEAD is still trunk, and the merged tree exists
// only in the index and the worktree. Selecting against HEAD there names
// nothing at all, so the stage passed on "nothing to measure" for precisely
// the event it exists to judge — while the diff FILE handed to the runner was
// rendered base..worktree the whole time, making selection and content two
// different diffs. One diff answers both.
func worktreeChangedPaths(root, base string) ([]string, bool) {
	if root == "" || base == "" {
		return nil, false
	}
	// Stdout alone, for the same reason the snapshot reads stdout alone: on
	// combined output a "warning: in the working copy of …" line becomes one
	// more changed PATH, and a scope that claims a sentence about line
	// endings as a file is a scope nobody can read.
	out, _, err := gitDiffOutFn(root, "diff", "--name-only", base)
	if err != nil {
		// absence-ok: false is "git could not say", which measureDiff turns
		// into an error naming root and base that the run then refuses on —
		// never into an empty change set, which is the failure this law is
		// about. The same contract changedPaths already has.
		return nil, false
	}
	var paths []string
	for line := range strings.SplitSeq(out, "\n") {
		if p := strings.TrimSpace(line); p != "" {
			paths = append(paths, p)
		}
	}
	return paths, true
}

// measureDiff is what the lane actually proposes to change: its own crate
// SOURCES against the merge base, as they stand in the working tree. A test
// file is not mutated (mutating the oracle proves nothing), and the crate set
// that falls out of it is what scopes both the mutant pool and the unmutated
// baseline.
func measureDiff(root, base string) (files, crates []string, err error) {
	changed, ok := worktreeChangedPaths(root, base)
	if !ok {
		return nil, nil, fmt.Errorf("could not read %s's diff against %s", root, base)
	}
	for _, p := range changed {
		if isMutableRustSource(p) {
			files = append(files, filepath.ToSlash(p))
		}
	}
	sortStrings(files)
	ws := cargoWorkspaceRoot(root)
	if ws == "" {
		ws = root
	}
	return files, cargoPackagesOwning(ws, toRootRelative(root, ws, files)), nil
}

// isMutableRustSource matches `crates/**/src/**.rs` and the single-crate
// `src/**.rs` that is the same shape without the workspace directory.
func isMutableRustSource(p string) bool {
	p = filepath.ToSlash(p)
	return strings.HasSuffix(p, ".rs") && (strings.HasPrefix(p, "src/") || strings.Contains(p, "/src/"))
}

// measureGoDiff is the same question for a Go module: the changed sources,
// with the test files left out for the same reason.
func measureGoDiff(root, base string) ([]string, error) {
	changed, ok := worktreeChangedPaths(root, base)
	if !ok {
		return nil, fmt.Errorf("could not read %s's diff against %s", root, base)
	}
	var files []string
	for _, p := range changed {
		p = filepath.ToSlash(p)
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			files = append(files, p)
		}
	}
	sortStrings(files)
	return files, nil
}

// writeMeasureDiff renders the scoped diff cargo-mutants is pointed at with
// --in-diff, beside the run's other temporary files.
func writeMeasureDiff(root, base string, files []string) (string, error) {
	// Stdout alone again, and here it is the tool that would trip: this file
	// is handed to cargo-mutants as --in-diff and PARSED as a patch. A
	// warning line lands ahead of the first `diff --git`, where a patch
	// parser has nowhere to put it.
	out, _, err := gitDiffOutFn(root, append([]string{"diff", base, "--"}, files...)...)
	if err != nil {
		return "", err
	}
	path := filepath.Join(measureTempDir(root), "changed.diff")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
