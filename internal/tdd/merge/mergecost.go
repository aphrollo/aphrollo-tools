package merge

import (
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
	"github.com/aphrollo/aphrollo-tools/internal/testcost"
)

// judgeAndRecordCost is judge run over a suite runner that notes what each
// passed suite cost, and, when the merge was not refused, writes the sum as one
// suite.cost event of the repo the lane belongs to. The numbers come from the
// run the gate makes anyway; a gate that ran no suite (a reused CI verdict)
// writes nothing.
func judgeAndRecordCost(laneWorktree string, run SuiteRunner, judge func(SuiteRunner) error) error {
	rec := NewCostRecorder()
	if err := judge(rec.Wrap(run)); err != nil {
		return err
	}
	if cost, ok := rec.Run(); ok {
		core.AppendEvent(core.Event{
			Kind:   testcost.EventKind,
			Root:   laneWorktree,
			Secs:   cost.Secs,
			Detail: cost.Detail(),
		})
	}
	return nil
}
