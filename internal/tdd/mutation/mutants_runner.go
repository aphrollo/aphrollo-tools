package mutation

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Measuring somewhere else, judging here.
//
// gremlins cannot measure this repo on Windows: v0.6.0 maps a mutant's
// position onto its coverage profile through filepath.Rel, which returns
// `calc\calc.go` there against a slash-separated lookup key, so every mutant
// in every package below the module root reads as NOT COVERED — 4890 of 4890
// on this tree (issue #697). It is Windows-only by construction, and the
// self-hosted Linux runner that already runs this repo's suites measures the
// same tree correctly.
//
// So the measurement moves to the box that can make it, and the box that
// cannot CONSUMES the answer. The whole difficulty is in the word "the
// answer": a verdict from another machine is evidence about the tree that
// machine measured, and about no other. The pre-merge gate does not judge a
// branch or a commit — it builds trunk in a throwaway worktree, merges the
// lane into it with --no-commit and judges the tree that results, which may
// never have been pushed anywhere and may not exist on any other box.
//
// Binding by branch name, PR number or head sha would all bind to something
// that is not what is being judged: trunk moves, and the same head sha merged
// into two different trunks is two different trees. The binding is therefore
// the tree's OWN identity — the git tree object the merged index writes out.
// Same id, same bytes; different id, different code and no evidence at all.

// RunnerReport is one mutation measurement made on another box, and the tree
// identity that says what it is a measurement OF. It carries the tool's raw
// per-mutant outcomes rather than a finished verdict on purpose: the
// accept-list, the sources and the tests live IN the tree the id names, so
// the same finishMeasure judges a runner's outcomes and a local run's, and
// the two boxes cannot disagree about what a survivor means.
type RunnerReport struct {
	// Tree is the git tree object id of the tree that was measured. It is
	// the whole binding, and it is content-addressed: the index stores a
	// repo's canonical (LF) bytes on every platform, so a tree built on
	// Linux and the same tree built on Windows have the same id.
	Tree string `json:"tree"`
	// Runner names the box, for the report a human reads. It is not part of
	// the binding — a name proves nothing — and nothing is decided on it.
	Runner string `json:"runner"`
	// Base is what the measured diff was scoped against, recorded so a
	// report that measured a different slice of the tree can be recognised
	// by eye.
	Base    string          `json:"base"`
	Mutants []MutantOutcome `json:"mutants"`
}

// runnerArtifactName is where a measurement of one tree is published and
// looked up. The tree id is in the NAME so the lookup is one request rather
// than a scan of every artifact the repo has ever produced — but the name is
// only an index. What is checked is the id inside the report, because a name
// is something anyone can write and a tree id is something only that tree
// produces.
func runnerArtifactName(tree string) string { return "mutants-verdict-" + tree }

// runnerReportFile is the report's own name inside that artifact.
const runnerReportFile = "mutants-verdict.json"

// mutantsTreeID is the identity of the tree this gate is judging: the tree
// object the CURRENT INDEX writes out. The index is the right question and
// HEAD is the wrong one — at pre-merge-commit, and in the throwaway checkout
// GatePRMerge builds, HEAD is still trunk and the merge result exists only in
// the index and the working tree, so HEAD's tree names the code that is NOT
// being judged.
//
// why is non-empty exactly when there is no id, and an unidentifiable tree is
// never waved through as "matches": a merge whose index git could not write
// (unmerged entries, a broken object store) has no identity, and a
// measurement cannot be shown to be about it.
func mutantsTreeID(root string) (id, why string) {
	// Stdout alone, the same rule the snapshot and the scoped diff follow: a
	// box with core.autocrlf on prints advice about line endings on stderr,
	// and read as part of the answer a sentence about CRLF becomes part of a
	// tree id.
	out, errText, err := gitDiffOutFn(root, "write-tree")
	if err != nil {
		return "", "git could not write out the tree being judged: " + gitFailureText(errText, err)
	}
	if id = strings.TrimSpace(out); id == "" {
		return "", "git write-tree named no tree for " + root
	}
	return id, ""
}

// consumeRunnerReport decides whether a measurement made elsewhere is a
// measurement of the tree in front of this gate. It touches no disk and no
// clock: what it answers is a function of two tree ids and nothing else.
//
// A mismatch is NOT weak evidence to be discounted — it is evidence about
// other code, which is no evidence about this merge, and the caller reports
// it the way this package reports every measurement that did not happen.
func consumeRunnerReport(tree string, r RunnerReport) (why string, ok bool) {
	if r.Tree == "" {
		return "the report names no tree at all, so there is nothing it can be shown to be a measurement of", false
	}
	if r.Tree != tree {
		return fmt.Sprintf("it measured tree %s and this gate is judging tree %s — a different tree is different code",
			r.Tree, tree), false
	}
	return "", true
}

// measureOnRunner is the Go half on a box whose own runner cannot measure:
// identify the tree, ask for a measurement of THAT tree, and judge the
// outcomes it brings back. Every path that does not end in a matching
// measurement ends in measureUnmeasured — the outcome issue #699 built, not a
// parallel one beside it — because "no evidence" is what all of them are.
func measureOnRunner(root string, cfg MutantsConfig, log io.Writer) Verdict {
	tree, why := mutantsTreeID(root)
	if tree == "" {
		return measureUnmeasured(root, gremlinsWindowsGap+
			", and the tree this gate is judging could not be identified, so no measurement made anywhere "+
			"could be shown to be a measurement of it: "+why, "no-tree-identity", log)
	}
	report, absent := runnerReportFn(root, tree)
	if absent != "" {
		return measureUnmeasured(root, gremlinsWindowsGap+
			", and no measurement of tree "+tree+" was available from the runner: "+absent, "gremlins-windows", log)
	}
	if mismatch, ok := consumeRunnerReport(tree, report); !ok {
		return measureUnmeasured(root, gremlinsWindowsGap+
			", and the measurement offered for this merge is not a measurement of it: "+mismatch,
			"runner-tree-mismatch", log)
	}
	logf(log, "mutants: consuming the measurement made on %s of tree %s — the tree this gate is judging, byte "+
		"for byte, so its outcomes are judged here against this repo's own accept-list", report.Runner, tree)
	return finishMeasure(root, cfg, report.Mutants, log)
}

// writeRunnerReport publishes what THIS box measured, for a box that cannot.
// It is written after the reach classification and before the judgement, so
// what travels is the outcomes — the judging is the consumer's, against the
// accept-list that is in the tree the id names.
//
// A report that cannot be written is logged and never changes the verdict:
// this run measured what it measured, and a failed handoff is the next box's
// missing evidence, not this one's refused merge.
func writeRunnerReport(root, path, base string, mutants []MutantOutcome, log io.Writer) {
	if path == "" {
		return
	}
	tree, why := mutantsTreeID(root)
	if tree == "" {
		logf(log, "mutants: no report written — %s; a measurement nothing can bind to a tree is not publishable", why)
		return
	}
	data, err := json.MarshalIndent(RunnerReport{
		Tree: tree, Runner: runnerIdentity(), Base: base, Mutants: mutants}, "", "  ")
	if err != nil {
		logf(log, "mutants: no report written — %v", err)
		return
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		logf(log, "mutants: no report written — %v", err)
		return
	}
	logf(log, "mutants: wrote the measurement of tree %s to %s", tree, path)
}

// runnerIdentity names the box a measurement was made on, for the line a
// human reads at merge time. Nothing is decided on it.
func runnerIdentity() string {
	if name := strings.TrimSpace(os.Getenv("RUNNER_NAME")); name != "" {
		return name + " (" + mutantsGOOSFn() + ")"
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return mutantsGOOSFn()
	}
	return host + " (" + mutantsGOOSFn() + ")"
}
