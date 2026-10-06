package shadow

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

// foldOwn is a run the agent started itself, folded with no edit id: it judges what
// the ledger holds. The ledger's edits are folded first, as the edit hook did.
func (b *shadowBox) foldOwn(tree, job string, v kernel.Verdict, argv ...string) string {
	b.t.Helper()
	for _, e := range b.ledger {
		if cause := b.world.FoldEdit(b.ctx(), EditFold{Root: b.root, Actor: "s1", EditID: e.ID, File: e.File}); cause != "" {
			b.t.Fatalf("FoldEdit(%s) = %q", e.ID, cause)
		}
	}
	return b.world.FoldRun(b.ctx(), Fold{Root: b.root, Actor: "s1", Tree: tree, Job: job, Own: true, Argv: argv, Verdict: v})
}

// A suite the agent ran by hand is a run of the units it covered, judging every edit
// the ledger holds: a red of a changed test opens the unit's red.
func TestFoldRun_ARedRunTheAgentStartedItselfOpensTheRedOfTheUnitItCovered(t *testing.T) {
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{{ID: "e1", File: b.file("internal/lane/lane_test.go"), At: t0}}
	if cause := b.foldOwn("a1", "bash-1", kernel.VerdictRed, "go", "test", "./internal/lane"); cause != "" {
		t.Fatalf("FoldRun = %q", cause)
	}
	if got := b.unit("internal/lane"); got.Phase != kernel.PhaseOpen || got.LastReal != kernel.VerdictRed || got.LastRealTree != "a1" {
		t.Errorf("unit = %+v, want the hand-run red open on tree a1", got)
	}
}

func TestFoldRun_AGreenRunTheAgentStartedItselfStampsOnlyTheUnitsItCovered(t *testing.T) {
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{
		{ID: "e1", File: b.file("internal/lane/lane.go"), At: t0},
		{ID: "e2", File: b.file("internal/store/store.go"), At: t0},
	}
	if cause := b.foldOwn("a1", "bash-2", kernel.VerdictGreen, "go", "test", "./internal/store"); cause != "" {
		t.Fatalf("FoldRun = %q", cause)
	}
	if got := b.unit("internal/store"); got.LastReal != kernel.VerdictGreen || got.LastRealTree != "a1" {
		t.Errorf("the covered unit = %+v, want its green on tree a1", got)
	}
	if got := b.unit("internal/lane"); got.LastReal != "" {
		t.Errorf("the unit the command does not name = %+v, want no verdict", got)
	}
}

// A hand-run suite with no edit of the lane to judge is nothing to fold, and no
// unjudged record either: every run of every test would leave one.
func TestFoldRun_ARunTheAgentStartedItselfWithNoEditsFoldsNothingAndSaysNothing(t *testing.T) {
	b := newShadowBox(t)
	if cause := b.foldOwn("a1", "bash-3", kernel.VerdictRed, "go", "test", "./..."); cause != "" {
		t.Errorf("FoldRun = %q, want no cause for a run that has no edit to judge", cause)
	}
	if got := b.unit("internal/lane"); got.LastReal != "" {
		t.Errorf("unit = %+v, want no verdict", got)
	}
}
