package shadow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/store"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// shadowBox is a repository with one Go module and a store, wired as a World: the
// lane is fixed and the ledger is what the test says it is.
type shadowBox struct {
	t      *testing.T
	root   string
	lane   string
	store  *store.Store
	ledger []LedgerEdit
	world  World
}

func newShadowBox(t *testing.T) *shadowBox {
	t.Helper()
	// .git is a file, as a linked worktree has it: the primary checkout is not followed.
	b := &shadowBox{t: t, root: tree(t, ".git", "go.mod", "internal/lane/lane.go", "internal/lane/lane_test.go", "internal/store/store.go", "README.md")}
	// A lane outside the holdout arm, so the kernel's answer for red-green is the
	// deny and not the shadowed guide.
	b.lane = laneIn(t, RuleRedGreen, false)
	st, err := store.Open(filepath.Join(b.root, "state"), store.Options{Config: Config, Warn: func(string) {}})
	if err != nil {
		t.Fatal(err)
	}
	b.store = st
	b.world = World{
		Lane:        func(string) string { return b.lane },
		ProjectRoot: manifestRoot,
		Edits:       func(string) []LedgerEdit { return b.ledger },
		Open:        func(string) (*store.Store, error) { return b.store, nil },
	}
	return b
}

func (b *shadowBox) file(rel string) string { return filepath.Join(b.root, filepath.FromSlash(rel)) }

func (b *shadowBox) ctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	b.t.Cleanup(cancel)
	return ctx
}

func (b *shadowBox) askRedGreen(rel string) Record {
	b.t.Helper()
	recs := b.world.RedGreen(b.ctx(), Payload{SessionID: "s1", ToolName: "Edit"}, []string{b.file(rel)})
	if len(recs) != 1 {
		b.t.Fatalf("RedGreen made %d records, want 1: %+v", len(recs), recs)
	}
	return recs[0]
}

// fold is what the hooks do for a finished run: each edit it judged is folded when
// it was made, and the run's result later.
func (b *shadowBox) fold(tree, job string, v kernel.Verdict, ids ...string) string {
	b.t.Helper()
	return b.foldRun(Fold{Root: b.root, Actor: "s1", Tree: tree, Job: job, EditIDs: ids, Verdict: v})
}

func (b *shadowBox) foldRun(f Fold) string {
	b.t.Helper()
	for _, id := range f.EditIDs {
		for _, e := range b.ledger {
			if e.ID == id {
				if cause := b.world.FoldEdit(b.ctx(), EditFold{Root: b.root, Actor: "s1", EditID: id, File: e.File}); cause != "" {
					b.t.Fatalf("FoldEdit(%s) = %q", id, cause)
				}
			}
		}
	}
	return b.world.FoldRun(b.ctx(), f)
}

func (b *shadowBox) unit(id string) kernel.Unit {
	b.t.Helper()
	rec, _, err := b.store.Load(b.ctx(), b.lane)
	if err != nil {
		b.t.Fatal(err)
	}
	return rec.Units[id]
}

func TestRedGreen_ACodeEditInAnUntestedUnitIsAWouldBeBlockAgainstAnAllow(t *testing.T) {
	b := newShadowBox(t)
	got := b.askRedGreen("internal/lane/lane.go")
	if got.Rule != RuleRedGreen || got.Trellis != "block" || got.Actual != "allow" || got.Relation != TrellisStricter || got.Unit != "internal/lane" {
		t.Errorf("record = %+v, want red-green: trellis block against an allow, trellis-stricter, unit internal/lane", got)
	}
}

func TestRedGreen_AnOpenRedInTheUnitAllowsTheCodeEditAndAgrees(t *testing.T) {
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{{ID: "e1", File: b.file("internal/lane/lane_test.go"), At: t0}}
	if cause := b.fold("a1", "j1", kernel.VerdictRed, "e1"); cause != "" {
		t.Fatalf("the fold of a test edit and its red run failed: %q", cause)
	}
	got := b.askRedGreen("internal/lane/lane.go")
	if got.Trellis != "allow" || got.Relation != Agree {
		t.Errorf("record = %+v, want the kernel to allow a code edit while the unit's red is open", got)
	}
}

func TestRedGreen_AUnitWithAGreenRunOnTheTreeOfItsNewestEditIsCovered(t *testing.T) {
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{{ID: "e2", File: b.file("internal/lane/lane.go"), At: t0}}
	if cause := b.fold("b2", "j2", kernel.VerdictGreen, "e2"); cause != "" {
		t.Fatalf("fold failed: %q", cause)
	}
	// No verdict file for the tree yet: the run is not in the store, so the unit is
	// unknown and reads as uncovered.
	if got := b.askRedGreen("internal/lane/lane.go"); got.Trellis != "block" {
		t.Errorf("a green the store holds no verdict of covers the unit: %+v", got)
	}
	_, err := b.store.RecordVerdict(b.ctx(), "b2", b.lane, store.Verdict{Runs: []store.RunVerdict{
		{Runner: "go", Unit: ".|go test ./internal/lane/...", Result: kernel.VerdictGreen, MS: 1000, At: t0.Add(10 * time.Second)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := b.askRedGreen("internal/lane/lane.go"); got.Trellis != "allow" || got.Relation != Agree {
		t.Errorf("record = %+v, want the covered unit's code edit allowed", got)
	}
	// A later edit of the unit leaves that green behind it.
	b.ledger = append(b.ledger, LedgerEdit{ID: "e3", File: b.file("internal/lane/lane.go"), At: t0.Add(time.Minute)})
	if got := b.askRedGreen("internal/lane/lane.go"); got.Trellis != "block" {
		t.Errorf("record = %+v, want an edit newer than the green to leave the unit uncovered", got)
	}
}

func TestRedGreen_NamesWhyItCouldNotJudge(t *testing.T) {
	b := newShadowBox(t)
	cases := []struct {
		name  string
		lane  string
		files []string
		cause string
	}{
		{"no lane", "", []string{b.file("internal/lane/lane.go")}, CauseNoLane},
		{"no unit", "lane/a", []string{b.file("scripts/run.py")}, CauseNoUnit},
	}
	for _, c := range cases {
		b.lane = c.lane
		recs := b.world.RedGreen(b.ctx(), Payload{}, c.files)
		if len(recs) != 1 || recs[0].Relation != Unjudged || recs[0].Cause != c.cause {
			t.Errorf("%s: records = %+v, want one unjudged with cause %q", c.name, recs, c.cause)
		}
	}
	// Only code files are asked: a test edit and a document are never refused by the rule.
	b.lane = "lane/a"
	if recs := b.world.RedGreen(b.ctx(), Payload{}, []string{b.file("internal/lane/lane_test.go"), b.file("README.md")}); len(recs) != 0 {
		t.Errorf("a test file and a document were asked: %+v", recs)
	}
}

func TestRedGreen_AnExpiredBudgetIsUnjudgedForTheBudget(t *testing.T) {
	b := newShadowBox(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recs := b.world.RedGreen(ctx, Payload{}, []string{b.file("internal/lane/lane.go")})
	if len(recs) != 1 || recs[0].Relation != Unjudged || recs[0].Cause != CauseBudget {
		t.Errorf("records = %+v, want one unjudged for the budget, never a guess", recs)
	}
}

func TestFoldRun_NamesWhyItCouldNotFold(t *testing.T) {
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{{ID: "e1", File: b.file("internal/lane/lane.go"), At: t0}, {ID: "e2", File: b.file("README.md"), At: t0}}
	lane := b.lane
	cases := []struct {
		name string
		f    Fold
		lane string
		want string
	}{
		{"a run with no tree key", Fold{Root: b.root, EditIDs: []string{"e1"}, Verdict: kernel.VerdictGreen}, lane, CauseNoTree},
		{"a checkout with no branch", Fold{Root: b.root, Tree: "a1", EditIDs: []string{"e1"}, Verdict: kernel.VerdictGreen}, "", CauseNoLane},
		{"edits the ledger does not hold", Fold{Root: b.root, Tree: "a1", EditIDs: []string{"gone"}, Verdict: kernel.VerdictGreen}, lane, CauseNoEdit},
		{"edits of files that are not code or tests", Fold{Root: b.root, Tree: "a1", EditIDs: []string{"e2"}, Verdict: kernel.VerdictGreen}, lane, CauseNoEdit},
		{"a fold that is made", Fold{Root: b.root, Tree: "a1", EditIDs: []string{"e1"}, Verdict: kernel.VerdictGreen}, lane, ""},
	}
	for _, c := range cases {
		b.lane = c.lane
		if got := b.world.FoldRun(b.ctx(), c.f); got != c.want {
			t.Errorf("%s: FoldRun = %q, want %q", c.name, got, c.want)
		}
	}
}

// ratchet: test_removed TestStop_AnOpenRedInTheLaneIsABlockBothSidesAgreeOn: replaced by TestStop_IsAskedAtEveryStopAndComparedToWhatTheLiveCheckDid, which asks every stop with aphrollo's own facts
func TestStop_IsAskedAtEveryStopAndComparedToWhatTheLiveCheckDid(t *testing.T) {
	b := newShadowBox(t)
	p := Payload{SessionID: "s1", Cwd: b.root}
	ask := func(hook string, f StopFacts) Record {
		t.Helper()
		r, ok := b.world.Stop(b.ctx(), hook, p, b.root, f)
		if !ok {
			t.Fatal("a stop on a followed lane made no record")
		}
		return r
	}
	// No red anywhere: both sides allow, and the agreement is a record too.
	if got := ask(HookStop, StopFacts{}); got.Rule != RuleStopRed || got.Trellis != "allow" || got.Relation != Agree || got.Hook != HookStop {
		t.Errorf("a stop with no red: %+v, want an agreeing allow", got)
	}
	// A live red the lane record never folded cannot be judged, and is never softer.
	if got := ask(HookStop, StopFacts{Unseen: true, Trees: []string{"a1"}, Blocked: true}); got.Relation != Unjudged || got.Cause != CauseUnfolded {
		t.Errorf("an unfolded red: %+v, want unjudged %q", got, CauseUnfolded)
	}
	b.ledger = []LedgerEdit{{ID: "e1", File: b.file("internal/lane/lane_test.go"), At: t0}}
	if cause := b.fold("a1", "j1", kernel.VerdictRed, "e1"); cause != "" {
		t.Fatalf("fold failed: %q", cause)
	}
	if got := ask(HookSubagentStop, StopFacts{Unseen: true, Trees: []string{"a1"}, Blocked: true}); got.Trellis != "block" || got.Relation != Agree || got.Hook != HookSubagentStop {
		t.Errorf("a folded red the live check blocked on: %+v, want both sides to block", got)
	}
	// The live check allowed with the red unseen (the session is off): trellis would block.
	if got := ask(HookStop, StopFacts{Unseen: true, Trees: []string{"a1"}}); got.Trellis != "block" || got.Actual != "allow" || got.Relation != TrellisStricter {
		t.Errorf("an unseen red the live check let by: %+v, want a would-be block", got)
	}
	// stop_hook_active: the kernel never blocks twice.
	p.StopHookActive = true
	if got := ask(HookStop, StopFacts{Unseen: true, Trees: []string{"a1"}}); got.Trellis != "allow" {
		t.Errorf("stop_hook_active: %+v, want the kernel to let the turn end", got)
	}
}

func TestRecordFactsAnd_AStepThatOutrunsTheBudgetIsWrittenUnjudgedAndCounted(t *testing.T) {
	got := capture(t)
	old := Budget
	Budget = 30 * time.Millisecond
	t.Cleanup(func() { Budget = old })
	before := Overruns.Load()
	hold, returned := make(chan struct{}), make(chan struct{})
	slow := Step{
		run: func(context.Context) []core.Event {
			select {
			case <-hold:
			case <-time.After(10 * time.Second):
			}
			close(returned)
			return []core.Event{{Kind: core.KindShadow, Detail: map[string]string{"rule": "late"}}}
		},
		skip: func(cause string) core.Event {
			return unjudged(HookPre, RuleRedGreen, "", cause).event(Source{Root: "r"}, "lane/x")
		},
	}
	RecordFactsAnd(Source{Root: t.TempDir()}, func() []Fact { return nil }, []Step{slow})
	if len(*got) != 1 || (*got)[0].Detail["relation"] != string(Unjudged) || (*got)[0].Detail["cause"] != CauseBudget || (*got)[0].Detail["rule"] != RuleRedGreen {
		t.Fatalf("events = %+v, want one unjudged red-green record naming the budget", *got)
	}
	if Overruns.Load() != before+1 {
		t.Errorf("Overruns = %d, want %d: the overrun is counted", Overruns.Load(), before+1)
	}
	close(hold)
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("the step never finished")
	}
}

// Exactly one of the two writers speaks for a step: once the hook has closed the
// window, a step that finishes late has its events refused.
func TestWindow_RefusesWhatALateStepWritesAfterTheHookClosedIt(t *testing.T) {
	got := capture(t)
	w := &window{}
	if !w.commit(0, []core.Event{{Kind: core.KindShadow}}) {
		t.Fatal("an open window refused a step")
	}
	if n := w.close(); n != 1 {
		t.Errorf("close = %d, want 1 step committed", n)
	}
	if w.commit(1, []core.Event{{Kind: core.KindShadow}}) || len(*got) != 1 {
		t.Errorf("a closed window took a late step: %d events", len(*got))
	}
}

func TestRecordFactsAnd_AStepThatFinishesWritesItsEventsAndNoUnjudged(t *testing.T) {
	got := capture(t)
	before := Overruns.Load()
	ok := Step{
		run: func(context.Context) []core.Event {
			return []core.Event{{Kind: core.KindShadow, Detail: map[string]string{"rule": "ran"}}}
		},
		skip: func(string) core.Event {
			return core.Event{Kind: core.KindShadow, Detail: map[string]string{"rule": "skipped"}}
		},
	}
	RecordFactsAnd(Source{Root: t.TempDir()}, func() []Fact { return nil }, []Step{ok})
	if len(*got) != 1 || (*got)[0].Detail["rule"] != "ran" || Overruns.Load() != before {
		t.Errorf("events = %+v overruns +%d, want the step's own event alone", *got, Overruns.Load()-before)
	}
}

// ratchet: test_removed TestFlush_FoldsAFinishedRunIntoItsLaneAndAnUnfoldableRunIsUnjudged: replaced by TestFlush_FoldsEditsThenRunsAndWritesAnUnjudgedRecordForWhatItCouldNotFold, as edits are folded apart from runs now
func TestFlush_FoldsEditsThenRunsAndWritesAnUnjudgedRecordForWhatItCouldNotFold(t *testing.T) {
	got := capture(t)
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{{ID: "e1", File: b.file("internal/lane/lane_test.go"), At: t0}}
	src := Source{Root: b.root, Actor: "s1", Key: "a1"}
	// Queued in the order the hook makes them: the run before the edit it judged is
	// still folded after it, for the edits go first in the flush.
	QueueFold(src, b.world, Fold{Root: b.root, Actor: "s1", Tree: "a1", Job: "j1", EditIDs: []string{"e1"}, Verdict: kernel.VerdictRed})
	QueueEditFold(src, b.world, EditFold{Root: b.root, Actor: "s1", EditID: "e1", File: b.file("internal/lane/lane_test.go")})
	QueueFold(src, b.world, Fold{Root: b.root, Actor: "s1", Tree: "", EditIDs: []string{"e1"}, Verdict: kernel.VerdictGreen})
	QueueFold(src, b.world, Fold{Root: b.root, Actor: "s1", Tree: "a2", EditIDs: []string{"gone"}, Verdict: kernel.VerdictGreen})
	Flush()
	causes := map[string]bool{}
	for _, e := range *got {
		if e.Detail["rule"] != RuleFold || e.Detail["relation"] != string(Unjudged) {
			t.Errorf("event %+v, want only unjudged lane-fold records", e)
		}
		causes[e.Detail["cause"]] = true
	}
	if len(*got) != 2 || !causes[CauseNoTree] || !causes[CauseNoEdit] {
		t.Fatalf("events = %+v, want one unjudged for the run with no tree and one for the edits the ledger does not hold", *got)
	}
	if u := b.unit("internal/lane"); u.Phase != kernel.PhaseOpen {
		t.Errorf("unit phase = %q, want open: the edit, then the queued red run, were folded", u.Phase)
	}
}

// The dominant bias of the data: a test edit whose run is still in flight. The edit
// is folded when it is made, so the unit is pending when the code edit's question
// is asked, and the kernel does not fire.
func TestRedGreen_ACodeEditWhileTheTestEditsRunIsInFlightIsNoFire(t *testing.T) {
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{{ID: "e1", File: b.file("internal/lane/lane_test.go"), At: t0}}
	if cause := b.world.FoldEdit(b.ctx(), EditFold{Root: b.root, Actor: "s1", EditID: "e1", File: b.file("internal/lane/lane_test.go")}); cause != "" {
		t.Fatalf("FoldEdit = %q", cause)
	}
	if got := b.unit("internal/lane").Phase; got != kernel.PhasePending {
		t.Fatalf("unit phase = %q, want pending after the test edit and before any run", got)
	}
	got := b.askRedGreen("internal/lane/lane.go")
	if got.Trellis != "allow" || got.Relation != Agree {
		t.Errorf("record = %+v, want no fire while the test's run is on its way", got)
	}
}

func TestFoldEdit_FoldsOnlyCodeAndTestFilesOfFollowedLanes(t *testing.T) {
	b := newShadowBox(t)
	if cause := b.world.FoldEdit(b.ctx(), EditFold{Root: b.root, EditID: "e", File: b.file("README.md")}); cause != "" {
		t.Errorf("a document: %q, want nothing folded and no cause", cause)
	}
	b.lane = ""
	if cause := b.world.FoldEdit(b.ctx(), EditFold{Root: b.root, EditID: "e", File: b.file("internal/lane/lane.go")}); cause != CauseNoLane {
		t.Errorf("no lane: %q, want %q", cause, CauseNoLane)
	}
}

// A lane the shadow does not follow is no ask, no fold and no record: trunk by
// name, and the primary checkout, whose .git is a directory.
func TestWorld_DoesNotFollowTrunkLanesOrThePrimaryCheckout(t *testing.T) {
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{{ID: "e1", File: b.file("internal/lane/lane_test.go"), At: t0}}
	for _, lane := range []string{"main", "master", kernel.TrunkLane} {
		b.lane = lane
		if recs := b.world.RedGreen(b.ctx(), Payload{}, []string{b.file("internal/lane/lane.go")}); len(recs) != 0 {
			t.Errorf("lane %s: red-green made records %+v", lane, recs)
		}
		if cause := b.world.FoldEdit(b.ctx(), EditFold{Root: b.root, EditID: "e1", File: b.file("internal/lane/lane_test.go")}); cause != "" {
			t.Errorf("lane %s: FoldEdit = %q", lane, cause)
		}
		if cause := b.fold("a1", "j", kernel.VerdictRed, "e1"); cause != "" {
			t.Errorf("lane %s: FoldRun = %q", lane, cause)
		}
		if _, ok := b.world.Stop(b.ctx(), HookStop, Payload{}, b.root, StopFacts{}); ok {
			t.Errorf("lane %s: a stop made a record", lane)
		}
		if _, v, _ := b.store.Load(b.ctx(), lane); v != 0 {
			t.Errorf("lane %s: the record was saved (version %d)", lane, v)
		}
	}
	// The primary checkout of a repo with a linked worktree is the trunk, whatever its
	// branch is called; a plain clone on a feature branch is followed.
	primary := tree(t, ".git/HEAD", ".git/worktrees/wt/HEAD", "go.mod", "internal/lane/lane.go")
	plain := tree(t, ".git/HEAD", "go.mod", "internal/lane/lane.go")
	b.lane = "lane/feature"
	if err := os.MkdirAll(filepath.Join(plain, ".git", "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	if recs := b.world.RedGreen(b.ctx(), Payload{}, []string{filepath.Join(primary, "internal", "lane", "lane.go")}); len(recs) != 0 {
		t.Errorf("the primary checkout: red-green made records %+v", recs)
	}
	if recs := b.world.RedGreen(b.ctx(), Payload{}, []string{filepath.Join(plain, "internal", "lane", "lane.go")}); len(recs) != 1 {
		t.Errorf("a plain clone on a feature branch: %d records, want 1: it is followed", len(recs))
	}
}

// One run, two touched packages, a command that names one: only that unit is
// stamped with the run's verdict.
func TestFoldRun_StampsOnlyTheUnitsTheRunsCommandCovers(t *testing.T) {
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{
		{ID: "e1", File: b.file("internal/lane/lane_test.go"), At: t0},
		{ID: "e2", File: b.file("internal/store/store.go"), At: t0},
	}
	cause := b.foldRun(Fold{Root: b.root, Actor: "s1", Tree: "a1", Job: "j1", EditIDs: []string{"e1", "e2"},
		Argv: []string{"go", "test", "./internal/lane"}, Verdict: kernel.VerdictRed})
	if cause != "" {
		t.Fatalf("FoldRun = %q", cause)
	}
	if got := b.unit("internal/lane"); got.Phase != kernel.PhaseOpen || got.LastReal != kernel.VerdictRed {
		t.Errorf("the covered unit = %+v, want its red stamped", got)
	}
	if got := b.unit("internal/store"); got.LastReal != "" || got.Tree != "" {
		t.Errorf("the unit the command does not name = %+v, want no verdict and no tree", got)
	}
}

// A run that judged an older edit of a unit says nothing of it once a newer edit is
// folded: the run is stale for the unit.
func TestFoldRun_ALaterEditOfTheUnitLeavesTheRunStale(t *testing.T) {
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{
		{ID: "e1", File: b.file("internal/lane/lane_test.go"), At: t0},
		{ID: "e2", File: b.file("internal/lane/lane.go"), At: t0.Add(time.Minute)},
	}
	b.foldRun(Fold{Root: b.root, Actor: "s1", Tree: "a1", Job: "j1", EditIDs: []string{"e1"}, Verdict: kernel.VerdictRed})
	if got := b.unit("internal/lane"); got.LastReal != "" {
		t.Errorf("unit = %+v, want a run of the older edit not stamped over the newer one", got)
	}
}

// The fold is one transaction: a context that ends before it saves nothing, and a
// fold of two units is one commit.
func TestFoldRun_IsOneCommitOrNone(t *testing.T) {
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{
		{ID: "e1", File: b.file("internal/lane/lane_test.go"), At: t0},
		{ID: "e2", File: b.file("internal/store/store.go"), At: t0},
	}
	f := Fold{Root: b.root, Actor: "s1", Tree: "a1", Job: "j1", EditIDs: []string{"e1", "e2"}, Verdict: kernel.VerdictRed}
	for _, id := range f.EditIDs {
		for _, e := range b.ledger {
			if e.ID == id {
				b.world.FoldEdit(b.ctx(), EditFold{Root: b.root, EditID: id, File: e.File})
			}
		}
	}
	_, before, _ := b.store.Load(b.ctx(), b.lane)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if cause := b.world.FoldRun(ctx, f); cause != CauseBudget {
		t.Errorf("FoldRun on an ended context = %q, want %q", cause, CauseBudget)
	}
	if _, after, _ := b.store.Load(b.ctx(), b.lane); after != before {
		t.Errorf("version %d after the overrun, want %d: nothing saved", after, before)
	}
	if cause := b.world.FoldRun(b.ctx(), f); cause != "" {
		t.Fatalf("FoldRun = %q", cause)
	}
	if _, after, _ := b.store.Load(b.ctx(), b.lane); after != before+1 {
		t.Errorf("version %d after folding two units, want %d: one commit", after, before+1)
	}
	if b.unit("internal/lane").LastReal != kernel.VerdictRed || b.unit("internal/store").LastReal != kernel.VerdictRed {
		t.Error("both units are stamped by the one commit")
	}
}

// A new func in a unit with a green run on the tree of its newest edit is still a
// fire: new code is not tested code however covered the unit is.
func TestRedGreen_ANewFuncInACoveredUnitStillFires(t *testing.T) {
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{{ID: "e2", File: b.file("internal/lane/lane.go"), At: t0}}
	if cause := b.fold("b2", "j2", kernel.VerdictGreen, "e2"); cause != "" {
		t.Fatalf("fold failed: %q", cause)
	}
	_, err := b.store.RecordVerdict(b.ctx(), "b2", b.lane, store.Verdict{Runs: []store.RunVerdict{
		{Runner: "go", Unit: ".|go test ./internal/lane/...", Result: kernel.VerdictGreen, MS: 1000, At: t0.Add(10 * time.Second)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ask := func(old, now string) Record {
		recs := b.world.RedGreen(b.ctx(), editPayload(old, now), []string{b.file("internal/lane/lane.go")})
		if len(recs) != 1 {
			t.Fatalf("records = %+v", recs)
		}
		return recs[0]
	}
	if got := ask("return 1", "return 2"); got.Trellis != "allow" {
		t.Errorf("an edit of a covered unit that adds no symbol: %+v, want allow", got)
	}
	if got := ask("return 1", "return 1\n}\n\nfunc Added() int {\n\treturn 2"); got.Trellis != "block" || got.Relation != TrellisStricter {
		t.Errorf("an edit adding a func to a covered unit: %+v, want a would-be block", got)
	}
}

func TestCovered_AnEditAtTheMomentTheRunStartedIsHeldByIt(t *testing.T) {
	u := Unit{ID: "p", Project: ".", Pkg: "p", Kind: unitGoPackage}
	unitOf := func(string) (Unit, bool) { return u, true }
	run := []store.RunVerdict{runOf(kernel.VerdictGreen, ".|go test ./...", t0.Add(4*time.Second), 4000)} // started at t0
	if !Covered(u, run, []LedgerEdit{{File: "f.go", At: t0}}, unitOf) {
		t.Error("an edit recorded at the instant the run started is not held by its tree")
	}
	if Covered(u, run, []LedgerEdit{{File: "f.go", At: t0.Add(time.Millisecond)}}, unitOf) {
		t.Error("an edit a millisecond after the run started is held by its tree")
	}
}

func editPayload(old, now string) Payload {
	var p Payload
	p.ToolName = "Edit"
	p.ToolInput.OldString, p.ToolInput.NewString = old, now
	return p
}

// An edit is itself newer than any run, so the cover its fold carries is the unit's
// as it stood before the edit: a covered unit's new code edit leaves no
// untested-code guidance on the unit, an uncovered one does.
func TestFoldEdit_CarriesTheUnitsCoverAsItStoodBeforeTheEdit(t *testing.T) {
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{{ID: "e2", File: b.file("internal/lane/lane_test.go"), At: t0}}
	if cause := b.fold("b2", "j2", kernel.VerdictGreen, "e2"); cause != "" {
		t.Fatalf("fold failed: %q", cause)
	}
	_, err := b.store.RecordVerdict(b.ctx(), "b2", b.lane, store.Verdict{Runs: []store.RunVerdict{
		{Runner: "go", Unit: ".|go test ./internal/lane/...", Result: kernel.VerdictGreen, MS: 1000, At: t0.Add(10 * time.Second)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	b.ledger = append(b.ledger, LedgerEdit{ID: "e3", File: b.file("internal/lane/lane.go"), At: t0.Add(time.Minute)})
	if cause := b.world.FoldEdit(b.ctx(), EditFold{Root: b.root, Actor: "s1", EditID: "e3", File: b.file("internal/lane/lane.go")}); cause != "" {
		t.Fatalf("FoldEdit = %q", cause)
	}
	if b.unit("internal/lane").GuidedUntested {
		t.Error("a code edit of a covered unit was folded as untested code")
	}
	// The same edit of a unit no run covers is untested code.
	b.ledger = append(b.ledger, LedgerEdit{ID: "e4", File: b.file("internal/store/store.go"), At: t0})
	if cause := b.world.FoldEdit(b.ctx(), EditFold{Root: b.root, Actor: "s1", EditID: "e4", File: b.file("internal/store/store.go")}); cause != "" {
		t.Fatalf("FoldEdit = %q", cause)
	}
	if !b.unit("internal/store").GuidedUntested {
		t.Error("a code edit of an uncovered unit was folded as covered")
	}
}

// A fact the hook has stopped waiting for is dropped, never written later: the
// goroutine that builds it outlives the hook, and what it wrote then would land
// in whichever store is current when it finishes (a later test's, or a later
// session's).
func TestWindow_RefusesAFactWrittenAfterTheHookClosedIt(t *testing.T) {
	got := capture(t)
	w := &window{}
	if !w.write(core.Event{Kind: core.KindShadow}) {
		t.Fatal("an open window refused a fact")
	}
	w.close()
	if w.write(core.Event{Kind: core.KindShadow}) || len(*got) != 1 {
		t.Errorf("a closed window took a late fact: %d events", len(*got))
	}
}

// The same through the hook's own entry: the facts are built after the budget is
// spent, and nothing of them is written. Writing is a refusal the test cannot
// time, so it waits a bounded while for a write that must not come; a failure
// is never a false alarm, only a late catch.
func TestRecordFacts_AFactBuiltAfterTheBudgetIsNotWritten(t *testing.T) {
	oldBudget := Budget
	t.Cleanup(func() { Budget = oldBudget })
	Budget = 20 * time.Millisecond
	wrote := make(chan struct{}, 1)
	oldAppend := appendEvent
	// The drop itself is written (an unjudged record of the facts); the fact is not.
	appendEvent = func(e core.Event) {
		if e.Detail["rule"] != RuleFacts {
			wrote <- struct{}{}
		}
	}
	t.Cleanup(func() { appendEvent = oldAppend })
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	RecordFacts(Source{Root: t.TempDir()}, func() []Fact {
		<-release
		return []Fact{Discard(Block)}
	})
	release <- struct{}{}

	select {
	case <-wrote:
		t.Fatal("a fact built after the hook moved on was written")
	case <-time.After(300 * time.Millisecond):
	}
}
