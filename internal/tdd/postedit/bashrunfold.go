package postedit

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/store"
)

// FoldBashRun counts a test suite the agent ran itself from Bash or PowerShell as a
// run, exactly as a run the gate started: its verdict is written to the store under
// the tree it measured, and it is queued to be folded (shadow.Flush) into the lane
// record as the run of every unit it covered, so a green covers the lane's edits
// and a red opens the red of the unit whose test changed. It runs nothing: the
// command's own exit and output, which the PostToolUse hook (exit 0) and the
// PostToolUseFailure hook (any other exit) carry, are all it reads, through the same
// verdict reading a run of the gate gets (phaseVerdictOf). A call that is no suite
// run, whose exit is not one suite's, or that reached no verdict, folds nothing.
func FoldBashRun(raw []byte) {
	p, ok := shadow.ParsePayload(raw)
	if !ok || !p.IsShell() {
		return
	}
	res, ok := p.BashResult()
	if !ok {
		return
	}
	run, ok := shadow.ParseBashRun(p.ToolInput.Command, p.Cwd)
	if !ok {
		return
	}
	root := findRootFrom(run.Dir)
	if root == "" {
		return
	}
	suite := SuiteResult{Passed: res.Exit == 0, Output: res.Output}
	if !suite.Passed {
		suite.Err = fmt.Sprintf("exit status %d", res.Exit)
	}
	verdict, cause, ok := phaseVerdictOf("run", suite, PhaseOutcome{ExitCode: res.Exit})
	if !ok {
		return
	}
	tree, err := worktreeKeyFn(root)
	if err != nil || tree == "" {
		return
	}
	recordOwnRun(root, tree, run.Argv, verdict, cause, res)
	shadow.QueueFold(shadow.Source{Root: root, Actor: p.SessionID, Key: tree, Lang: shadow.LangOfCommand(run.Argv[0])}, shadowWorld(), shadow.Fold{
		Root: root, Actor: p.SessionID, Tree: tree, Job: "bash-" + res.ToolUseID, Own: true,
		Argv: run.Argv, Verdict: verdict, Cause: cause,
	})
}

// recordOwnRun writes a hand-run suite's verdict to the store under the tree it
// measured, the file a unit's cover is read from. It is best-effort and bounded, as
// the gate's own run's write is: a verdict not written only leaves the unit uncovered.
// No lane-red pointer is left, for the agent has read the red it ran.
func recordOwnRun(root, tree string, argv []string, verdict kernel.Verdict, cause string, res shadow.BashResult) {
	ctx, cancel := context.WithTimeout(context.Background(), runVerdictBudget)
	defer cancel()
	s, err := openRunStoreFn(root)
	if err != nil {
		return
	}
	lane := LaneOf(root)
	if lane == "" {
		lane = filepath.Base(root)
	}
	_, _ = s.RecordVerdict(ctx, tree, lane, store.Verdict{Runs: []store.RunVerdict{{
		Runner: argv[0], Unit: shadow.RunUnitName(root, argv), Result: verdict, Cause: cause,
		MS: res.DurationMS, At: time.Now().UTC(),
	}}})
}
