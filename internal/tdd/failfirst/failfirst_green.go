package failfirst

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Issue #922: the proof ran the staged tests at HEAD, saw them RED, and
// stopped. A test that is red at HEAD and still red with the staged change
// passed that proof all the same, and the commit landed with its own new
// test failing. Having proved RED, the proof therefore runs the SAME
// selected tests once more, in the same worktree and against the same target
// dir so the build is reused, with the whole staged change in place, and the
// commit is refused unless they pass. Every other suite still runs at the
// merge.

// greenProof is the second half of a fail-first proof: the proven tests run
// with the staged change applied.
type greenProof struct {
	// ran is false when the run reached no verdict: the staged tree could
	// not be written into the worktree, no build slot came free, or the run
	// was killed at its budget. why names which, and res stays zero, so a
	// run that reached no verdict never reads as passed.
	ran bool
	why string
	res SuiteResult
}

// proveGreenWithChange replaces the proof worktree wt's files with the
// staged tree, which carries the staged tests plus the staged source and
// data, and runs runner again in execRoot.
func proveGreenWithChange(repoRoot, wt, execRoot string, runner Runner, run SuiteRunner) greenProof {
	// A write-tree failure leaves no tree id, and read-tree below refuses
	// what it printed instead.
	tree, _ := git(repoRoot, "write-tree")
	// --reset takes the staged content over the test diff already applied,
	// adds the staged new files and drops the staged deletions.
	if out, err := git(wt, "read-tree", "-u", "--reset", strings.TrimSpace(tree)); err != nil {
		return greenProof{why: "the staged tree could not be written into the proof worktree: " + strings.TrimSpace(out)}
	}
	res, waited, acquired := runCargoLocked(run, runner, execRoot, precommitLockWait(), DefaultPrecommitTimeout, 0)
	if !acquired {
		return greenProof{why: fmt.Sprintf("no build slot came free in %s", waited.Round(time.Second))}
	}
	if res.TimedOut {
		return greenProof{why: fmt.Sprintf("the run was killed after %.0fs", res.Duration.Seconds())}
	}
	return greenProof{ran: true, res: res}
}

// stillRedMessage refuses a commit whose proven tests do not pass with the
// staged change, naming the tests that fail and the command that shows it.
func stillRedMessage(root, cmd string, g greenProof) string {
	if !g.ran {
		return fmt.Sprintf(
			"BLOCKED: fail-first proved your staged tests RED at HEAD, but could not run them with the staged change: %s.\n"+
				"Refusing rather than landing a commit whose new test nobody saw pass.\n"+
				"The run it owes: %s (in %s)", g.why, cmd, root)
	}
	names := ExtractFailingTests(g.res.Output)
	failing := "the selected tests"
	if len(names) > 0 {
		failing = strings.Join(names, ", ")
	}
	return fmt.Sprintf(
		"BLOCKED: fail-first: your staged tests are RED at HEAD and still RED with the staged change: %s.\n"+
			"A test that fails both before and after the change proves nothing about it; fix the change or the test.\n"+
			"`aphrollo gate output` shows the run; to repeat it:\n"+
			"    cd %s\n"+
			"    %s", failing, root, cmd)
}

// greenRefusal prints and logs the GREEN half of a conclusive RED proof,
// and reports the outcome the stage refuses on unless the proven tests
// passed with the change. Both refusals are the proof itself failing, so
// both are check errors to verdictFor; the still-red run's own output is
// logged under its own token first, for `aphrollo gate output`.
func greenRefusal(root, cmd string, g greenProof) (stageOutcome, bool) {
	verdict := "green-unproven"
	if g.ran {
		verdict = "still-red"
	}
	if g.res.Passed {
		verdict = "green-proven"
	}
	fmt.Fprintf(os.Stderr, "[fail-first] gate precommit: %s in %s → %s (%.1fs)\n", cmd, root, verdict, g.res.Duration.Seconds())
	logSuiteVerdict("precommit", root, cmd, verdict, g.res)
	if verdict == "green-proven" {
		return stageOutcome{}, false
	}
	return stageOutcome{
		Kind:    outcomeCheckError,
		Err:     fmt.Errorf("fail-first %s: the proven tests did not pass with the staged change", verdict),
		Message: stillRedMessage(root, cmd, g),
	}, true
}
