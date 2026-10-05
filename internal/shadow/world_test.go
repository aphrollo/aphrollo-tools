package shadow

import (
	"context"
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
	b := &shadowBox{t: t, root: tree(t, ".git/HEAD", "go.mod", "internal/lane/lane.go", "internal/lane/lane_test.go", "README.md")}
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

func (b *shadowBox) fold(tree, job string, v kernel.Verdict, ids ...string) string {
	b.t.Helper()
	return b.world.FoldRun(b.ctx(), Fold{Root: b.root, Actor: "s1", Tree: tree, Job: job, EditIDs: ids, Verdict: v})
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
		{"edits the ledger does not hold", Fold{Root: b.root, Tree: "a1", EditIDs: []string{"gone"}, Verdict: kernel.VerdictGreen}, lane, causeNoEdit},
		{"edits of files that are not code or tests", Fold{Root: b.root, Tree: "a1", EditIDs: []string{"e2"}, Verdict: kernel.VerdictGreen}, lane, causeNoEdit},
		{"a fold that is made", Fold{Root: b.root, Tree: "a1", EditIDs: []string{"e1"}, Verdict: kernel.VerdictGreen}, lane, ""},
	}
	for _, c := range cases {
		b.lane = c.lane
		if got := b.world.FoldRun(b.ctx(), c.f); got != c.want {
			t.Errorf("%s: FoldRun = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestStop_AnOpenRedInTheLaneIsABlockBothSidesAgreeOn(t *testing.T) {
	b := newShadowBox(t)
	p := Payload{SessionID: "s1", Cwd: b.root}
	// No red recorded in the lane: aphrollo blocked on an unseen red the lane
	// record does not hold, so the kernel would not block.
	if got := b.world.Stop(b.ctx(), HookStop, p, b.root); got.Rule != RuleStopRed || got.Trellis != "allow" || got.Relation != TrellisSofter || got.Hook != HookStop {
		t.Errorf("without a red in the lane record: %+v, want trellis allow against a block (softer)", got)
	}
	b.ledger = []LedgerEdit{{ID: "e1", File: b.file("internal/lane/lane_test.go"), At: t0}}
	if cause := b.fold("a1", "j1", kernel.VerdictRed, "e1"); cause != "" {
		t.Fatalf("fold failed: %q", cause)
	}
	if got := b.world.Stop(b.ctx(), HookSubagentStop, p, b.root); got.Trellis != "block" || got.Relation != Agree || got.Hook != HookSubagentStop {
		t.Errorf("with an open red: %+v, want both sides to block", got)
	}
	// stop_hook_active: the kernel never blocks twice.
	p.StopHookActive = true
	if got := b.world.Stop(b.ctx(), HookStop, p, b.root); got.Trellis != "allow" {
		t.Errorf("stop_hook_active: %+v, want the kernel to let the turn end", got)
	}
}

func TestRecordFactsAnd_AStepThatOutrunsTheBudgetIsWrittenUnjudgedAndCounted(t *testing.T) {
	got := capture(t)
	old := Budget
	Budget = 30 * time.Millisecond
	t.Cleanup(func() { Budget = old })
	before := Overruns.Load()
	released := make(chan struct{})
	slow := Step{
		run: func(ctx context.Context) []core.Event {
			select {
			case <-ctx.Done():
			case <-time.After(10 * time.Second):
			}
			close(released)
			return []core.Event{{Kind: core.KindShadow, Detail: map[string]string{"rule": "late"}}}
		},
		skip: func(cause string) core.Event {
			return unjudged(HookPre, RuleRedGreen, "", cause).event(Source{Root: "r"}, "lane/x")
		},
	}
	RecordFactsAnd(Source{Root: t.TempDir()}, func() []Fact { return nil }, []Step{slow})
	select {
	case <-released:
	case <-time.After(10 * time.Second):
		t.Fatal("the step was not told the budget was spent")
	}
	if len(*got) != 1 || (*got)[0].Detail["relation"] != string(Unjudged) || (*got)[0].Detail["cause"] != CauseBudget || (*got)[0].Detail["rule"] != RuleRedGreen {
		t.Errorf("events = %+v, want one unjudged red-green record naming the budget", *got)
	}
	if Overruns.Load() != before+1 {
		t.Errorf("Overruns = %d, want %d: the overrun is counted", Overruns.Load(), before+1)
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

func TestFlush_FoldsAFinishedRunIntoItsLaneAndAnUnfoldableRunIsUnjudged(t *testing.T) {
	got := capture(t)
	b := newShadowBox(t)
	b.ledger = []LedgerEdit{{ID: "e1", File: b.file("internal/lane/lane_test.go"), At: t0}}
	src := Source{Root: b.root, Actor: "s1", Key: "a1"}
	QueueFold(src, b.world, Fold{Root: b.root, Actor: "s1", Tree: "a1", Job: "j1", EditIDs: []string{"e1"}, Verdict: kernel.VerdictRed})
	QueueFold(src, b.world, Fold{Root: b.root, Actor: "s1", Tree: "", EditIDs: []string{"e1"}, Verdict: kernel.VerdictGreen})
	Flush()
	if len(*got) != 1 || (*got)[0].Detail["rule"] != RuleFold || (*got)[0].Detail["cause"] != CauseNoTree || (*got)[0].Detail["relation"] != string(Unjudged) {
		t.Fatalf("events = %+v, want one unjudged lane-fold for the run with no tree, and nothing for the fold made", *got)
	}
	rec, _, err := b.store.Load(b.ctx(), b.lane)
	if err != nil {
		t.Fatal(err)
	}
	if u := rec.Units["internal/lane"]; u.Phase != kernel.PhaseOpen {
		t.Errorf("unit phase = %q, want open: the queued red run was folded", u.Phase)
	}
}
