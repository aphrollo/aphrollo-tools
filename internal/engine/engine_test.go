package engine

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

var t0 = time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)

// bounded is a context no test waits past: a hung store fails the test at the
// deadline instead of hanging the run.
func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// testStore is a Store the tests can also read the log of.
type testStore interface {
	Store
	Events(lane string) []kernel.Event
}

// newTestStore makes the store the tests run over. The in-memory one is the
// default; TestStoreOnDisk swaps in the on-disk one and runs the same tests.
var newTestStore = func(testing.TB, kernel.Config) testStore { return NewMemStore() }

func newEngine(t testing.TB, cfg kernel.Config) (*Engine, testStore) {
	store := newTestStore(t, cfg)
	return &Engine{Store: store, Config: cfg}, store
}

func edit(unit string, file kernel.FileClass, tree string, mods ...func(*kernel.Event)) kernel.Event {
	e := kernel.Event{Kind: kernel.KindEdit, Lane: "fix", Actor: "s1/a", At: t0, Unit: unit, File: file, Tree: tree, Test: "TestA"}
	for _, m := range mods {
		m(&e)
	}
	return e
}

func result(unit string, v kernel.Verdict, tree, job string) kernel.Event {
	return kernel.Event{Kind: kernel.KindRunResult, Lane: "fix", At: t0, Unit: unit, Verdict: v, Tree: tree, Job: job, Test: "TestA"}
}

func untestedWrite(claude bool) kernel.Event {
	return kernel.Event{Kind: kernel.KindPreTool, Lane: "fix", Actor: "s1/a", At: t0, Claude: claude,
		Tool: kernel.ToolWrite, Target: kernel.PathLane, Unit: "pkg/a", File: kernel.ClassCode, Tree: "t1"}
}

func load(t *testing.T, s Store, lane string) (Record, uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rec, ver, err := s.Load(ctx, lane)
	if err != nil {
		t.Fatalf("Load(%q): %v", lane, err)
	}
	return rec, ver
}

func TestHandle_factSequencesMoveLaneAndUnit(t *testing.T) {
	cases := []struct {
		name      string
		events    []kernel.Event
		wantLife  kernel.Life
		wantPhase kernel.Phase
		wantPair  kernel.Pair
	}{
		{"a test edit opens the lane and the unit's pending", []kernel.Event{
			edit("pkg/a", kernel.ClassTest, "t1"),
		}, kernel.LifeOpen, kernel.PhasePending, kernel.Pair{}},
		{"a real red opens the unit", []kernel.Event{
			edit("pkg/a", kernel.ClassTest, "t1"),
			result("pkg/a", kernel.VerdictRed, "t1", "j1"),
		}, kernel.LifeOpen, kernel.PhaseOpen, kernel.Pair{}},
		{"green after a code change closes with the pair", []kernel.Event{
			edit("pkg/a", kernel.ClassTest, "t1"),
			result("pkg/a", kernel.VerdictRed, "t1", "j1"),
			edit("pkg/a", kernel.ClassCode, "t2"),
			result("pkg/a", kernel.VerdictGreen, "t2", "j2"),
		}, kernel.LifeOpen, kernel.PhaseClosed, kernel.Pair{Test: "TestA", Red: "t1", Green: "t2"}},
		{"a green at once closes with no pair", []kernel.Event{
			edit("pkg/a", kernel.ClassTest, "t1"),
			result("pkg/a", kernel.VerdictGreen, "t1", "j1"),
		}, kernel.LifeOpen, kernel.PhaseClosed, kernel.Pair{}},
		{"a bogus red moves nothing", []kernel.Event{
			edit("pkg/a", kernel.ClassTest, "t1"),
			result("pkg/a", kernel.VerdictRedBogus, "t1", "j1"),
		}, kernel.LifeOpen, kernel.PhasePending, kernel.Pair{}},
		{"a retried job is dropped", []kernel.Event{
			edit("pkg/a", kernel.ClassTest, "t1"),
			result("pkg/a", kernel.VerdictRed, "t1", "j1"),
			result("pkg/a", kernel.VerdictGreen, "t1", "j1"),
		}, kernel.LifeOpen, kernel.PhaseOpen, kernel.Pair{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eng, store := newEngine(t, kernel.Config{})
			for i, e := range c.events {
				if _, err := eng.Handle(bounded(t), e); err != nil {
					t.Fatalf("Handle(event %d, %s): %v", i, e.Kind, err)
				}
			}
			rec, _ := load(t, store, "fix")
			if rec.Lane.Life != c.wantLife {
				t.Errorf("life = %q, want %q", rec.Lane.Life, c.wantLife)
			}
			u := rec.Units["pkg/a"]
			if u.Phase != c.wantPhase {
				t.Errorf("phase = %q, want %q", u.Phase, c.wantPhase)
			}
			if u.Pair != c.wantPair {
				t.Errorf("pair = %+v, want %+v", u.Pair, c.wantPair)
			}
		})
	}
}

func TestHandle_returnsTheEffectsAndDoesNotRunThem(t *testing.T) {
	eng, _ := newEngine(t, kernel.Config{})
	d, err := eng.Handle(bounded(t), edit("pkg/a", kernel.ClassTest, "t1"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	var kinds []kernel.EffectKind
	for _, f := range d.Effects {
		kinds = append(kinds, f.Kind)
	}
	for _, want := range []kernel.EffectKind{kernel.EffectInstallDeps, kernel.EffectRequestRun} {
		if !slices.Contains(kinds, want) {
			t.Errorf("effects %v lack %q: the first edit opens the lane and asks for the unit's run", kinds, want)
		}
	}
}

func TestHandle_logsFactsInOrderAndNoQuestion(t *testing.T) {
	eng, store := newEngine(t, kernel.Config{})
	first := edit("pkg/a", kernel.ClassTest, "t1")
	second := result("pkg/a", kernel.VerdictRed, "t1", "j1")
	for _, e := range []kernel.Event{first, {Kind: kernel.KindPreTool, Lane: "fix", Claude: true, Cmds: kernel.CmdBypassGate, Tool: kernel.ToolBash}, second} {
		if _, err := eng.Handle(bounded(t), e); err != nil {
			t.Fatalf("Handle(%s): %v", e.Kind, err)
		}
	}
	got := store.Events("fix")
	if len(got) != 2 || got[0].Kind != kernel.KindEdit || got[1].Kind != kernel.KindRunResult {
		t.Fatalf("log = %+v, want the edit then the run.result and no tool.pre", got)
	}
}

func TestHandle_questionMovesAndSavesNothing(t *testing.T) {
	eng, store := newEngine(t, kernel.Config{})
	q := kernel.Event{Kind: kernel.KindPreTool, Lane: "fix", Claude: true, Tool: kernel.ToolBash, Cmds: kernel.CmdBypassGate}
	d, err := eng.Handle(bounded(t), q)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if d.Outcome != kernel.OutcomeDeny || d.Rule != "bypass-gate" {
		t.Fatalf("decision = %s %q, want deny bypass-gate", d.Outcome, d.Rule)
	}
	if _, ver := load(t, store, "fix"); ver != 0 {
		t.Errorf("version = %d, want 0: a question that guided nothing saves nothing", ver)
	}
}

func TestHandle_untestedCodeEditIsGuidedOnceUnderWarn(t *testing.T) {
	eng, store := newEngine(t, kernel.Config{})
	first, err := eng.Handle(bounded(t), untestedWrite(true))
	if err != nil {
		t.Fatalf("Handle(first): %v", err)
	}
	if first.Outcome != kernel.OutcomeGuide || first.Rule != "red-green" || !first.WouldDeny {
		t.Fatalf("first = %s %q wouldDeny=%v, want a red-green guide that would deny under enforce", first.Outcome, first.Rule, first.WouldDeny)
	}
	second, err := eng.Handle(bounded(t), untestedWrite(true))
	if err != nil {
		t.Fatalf("Handle(second): %v", err)
	}
	if second.Outcome != kernel.OutcomeAllow {
		t.Errorf("second = %s %q, want allow: the line is given once per unit per lane", second.Outcome, second.Rule)
	}
	rec, ver := load(t, store, "fix")
	if g := rec.Guided["pkg/a"]; !g.Untested || g.Held {
		t.Errorf("guided = %+v, want only the untested flag recorded", rec.Guided)
	}
	if len(rec.Units) != 0 {
		t.Errorf("units = %+v, want none: the write has not happened, so no fact knows the unit", rec.Units)
	}
	if ver != 1 {
		t.Errorf("version = %d, want 1: only the first question saved", ver)
	}
	if got := store.Events("fix"); len(got) != 0 {
		t.Errorf("log = %+v, want empty: a question is not a fact", got)
	}
}

func TestHandle_untestedCodeEditDeniesEveryTimeUnderEnforce(t *testing.T) {
	cfg := kernel.Config{Rules: map[string]kernel.Level{"red-green": kernel.LevelEnforce}}
	eng, _ := newEngine(t, cfg)
	for i := range 3 {
		d, err := eng.Handle(bounded(t), untestedWrite(true))
		if err != nil {
			t.Fatalf("Handle(%d): %v", i, err)
		}
		if d.Outcome != kernel.OutcomeDeny || d.Rule != "red-green" {
			t.Fatalf("call %d = %s %q, want deny red-green every time", i, d.Outcome, d.Rule)
		}
	}
}

func TestHandle_guidedFlagSurvivesLaterFacts(t *testing.T) {
	eng, store := newEngine(t, kernel.Config{})
	if _, err := eng.Handle(bounded(t), untestedWrite(true)); err != nil {
		t.Fatalf("Handle(question): %v", err)
	}
	if _, err := eng.Handle(bounded(t), edit("pkg/a", kernel.ClassCode, "t1")); err != nil {
		t.Fatalf("Handle(edit): %v", err)
	}
	rec, _ := load(t, store, "fix")
	if !rec.Units["pkg/a"].GuidedUntested {
		t.Errorf("unit = %+v, want the flag kept through the edit that followed", rec.Units["pkg/a"])
	}
}

func TestHandle_noLaneFactIsRefusedAndQuestionIsStillDecided(t *testing.T) {
	eng, store := newEngine(t, kernel.Config{})
	_, err := eng.Handle(bounded(t), kernel.Event{Kind: kernel.KindEdit, At: t0})
	if !errors.Is(err, ErrNoLane) {
		t.Errorf("fact with no lane: err = %v, want ErrNoLane", err)
	}
	d, err := eng.Handle(bounded(t), kernel.Event{Kind: kernel.KindPreTool, Claude: true, Tool: kernel.ToolBash, Cmds: kernel.CmdDiscard})
	if err != nil {
		t.Fatalf("question with no lane: %v", err)
	}
	if d.Rule != "discard-work" || d.Outcome != kernel.OutcomeDeny {
		t.Errorf("decision = %s %q, want deny discard-work: a wall needs no lane", d.Outcome, d.Rule)
	}
	if got := store.Events(""); len(got) != 0 {
		t.Errorf("log = %+v, want empty", got)
	}
}

// blind is a store that ignores its context, as a simple one may: the engine
// must stop on a cancelled context by itself.
type blind struct{ Store }

func (b blind) Load(_ context.Context, lane string) (Record, uint64, error) {
	return b.Store.Load(context.Background(), lane)
}

func (b blind) Commit(_ context.Context, lane string, expect uint64, rec Record, events []kernel.Event) (uint64, error) {
	return b.Store.Commit(context.Background(), lane, expect, rec, events)
}

func TestHandle_cancelledContextTouchesNothing(t *testing.T) {
	eng, store := newEngine(t, kernel.Config{})
	eng.Store = blind{store}
	ctx, cancel := context.WithCancel(bounded(t))
	cancel()
	if _, err := eng.Handle(ctx, edit("pkg/a", kernel.ClassTest, "t1")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := store.Events("fix"); len(got) != 0 {
		t.Errorf("log = %+v, want empty", got)
	}
}

// faulty wraps a store with the failures a real one has.
type faulty struct {
	Store
	loadErr, commitErr error
	conflicts          int // commits answered ErrConflict before any reaches the store
	commits            int
}

func (f *faulty) Load(ctx context.Context, lane string) (Record, uint64, error) {
	if f.loadErr != nil {
		return Record{}, 0, f.loadErr
	}
	return f.Store.Load(ctx, lane)
}

func (f *faulty) Commit(ctx context.Context, lane string, expect uint64, rec Record, events []kernel.Event) (uint64, error) {
	f.commits++
	if f.conflicts > 0 {
		f.conflicts--
		return 0, ErrConflict
	}
	if f.commitErr != nil {
		return 0, f.commitErr
	}
	return f.Store.Commit(ctx, lane, expect, rec, events)
}

var errDisk = errors.New("disk is gone")

func TestHandle_storeFailuresSurface(t *testing.T) {
	t.Run("a load failure is returned for a fact", func(t *testing.T) {
		eng, _ := newEngine(t, kernel.Config{})
		eng.Store = &faulty{Store: newTestStore(t, kernel.Config{}), loadErr: errDisk}
		if _, err := eng.Handle(bounded(t), edit("pkg/a", kernel.ClassTest, "t1")); !errors.Is(err, errDisk) {
			t.Errorf("err = %v, want the store's", err)
		}
	})
	t.Run("a commit failure returns no decision for a fact", func(t *testing.T) {
		eng, _ := newEngine(t, kernel.Config{})
		eng.Store = &faulty{Store: newTestStore(t, kernel.Config{}), commitErr: errDisk}
		d, err := eng.Handle(bounded(t), edit("pkg/a", kernel.ClassTest, "t1"))
		if !errors.Is(err, errDisk) {
			t.Fatalf("err = %v, want the store's", err)
		}
		if len(d.Effects) != 0 {
			t.Errorf("effects = %v, want none: the fact was not saved, so nothing may run for it", d.Effects)
		}
	})
	t.Run("a bookkeeping failure still returns the question's decision", func(t *testing.T) {
		eng, _ := newEngine(t, kernel.Config{})
		eng.Store = &faulty{Store: newTestStore(t, kernel.Config{}), commitErr: errDisk}
		d, err := eng.Handle(bounded(t), untestedWrite(true))
		if !errors.Is(err, errDisk) {
			t.Fatalf("err = %v, want the store's", err)
		}
		if d.Rule != "red-green" {
			t.Errorf("decision rule = %q, want red-green: the guide stands though its flag was not kept", d.Rule)
		}
	})
}

func TestHandle_lostUpdateIsRetriedAndLoggedOnce(t *testing.T) {
	eng, store := newEngine(t, kernel.Config{})
	f := &faulty{Store: store, conflicts: 2}
	eng.Store, eng.Attempts = f, 3
	if _, err := eng.Handle(bounded(t), edit("pkg/a", kernel.ClassTest, "t1")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if f.commits != 3 {
		t.Errorf("commits = %d, want 3: two lost updates, then the save", f.commits)
	}
	if got := store.Events("fix"); len(got) != 1 {
		t.Errorf("log has %d events, want 1: a retry never logs the fact twice", len(got))
	}
}

func TestHandle_retriesAreBounded(t *testing.T) {
	eng, store := newEngine(t, kernel.Config{})
	f := &faulty{Store: store, conflicts: 100}
	eng.Store, eng.Attempts = f, 2
	d, err := eng.Handle(bounded(t), edit("pkg/a", kernel.ClassTest, "t1"))
	if !errors.Is(err, ErrContended) {
		t.Fatalf("err = %v, want ErrContended", err)
	}
	if f.commits != 2 {
		t.Errorf("commits = %d, want exactly 2 attempts", f.commits)
	}
	if len(d.Effects) != 0 {
		t.Errorf("effects = %v, want none for a fact that was not saved", d.Effects)
	}
}

func TestHandle_contendedQuestionStillAnswers(t *testing.T) {
	eng, store := newEngine(t, kernel.Config{})
	f := &faulty{Store: store, conflicts: 100}
	eng.Store, eng.Attempts = f, 2
	d, err := eng.Handle(bounded(t), untestedWrite(true))
	if err != nil {
		t.Fatalf("err = %v, want nil: a question never fails for want of a flag", err)
	}
	if d.Rule != "red-green" || f.commits != 2 {
		t.Errorf("rule = %q after %d commits, want red-green after 2", d.Rule, f.commits)
	}
}

func TestHandle_lanesAreSeparateRecords(t *testing.T) {
	eng, store := newEngine(t, kernel.Config{})
	a, b := edit("pkg/a", kernel.ClassTest, "t1"), edit("pkg/a", kernel.ClassCode, "t1")
	b.Lane = "other"
	for _, e := range []kernel.Event{a, b} {
		if _, err := eng.Handle(bounded(t), e); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}
	fix, _ := load(t, store, "fix")
	other, _ := load(t, store, "other")
	if fix.Units["pkg/a"].Phase != kernel.PhasePending || other.Units["pkg/a"].Phase == kernel.PhasePending {
		t.Errorf("fix = %q, other = %q: a test edit in one lane must not pend the other's unit",
			fix.Units["pkg/a"].Phase, other.Units["pkg/a"].Phase)
	}
}

func TestHandle_heldUnitIsGuidedOnceNotDenied(t *testing.T) {
	cfg := kernel.Config{Rules: map[string]kernel.Level{"red-green": kernel.LevelEnforce}}
	eng, store := newEngine(t, cfg)
	escape := kernel.Event{Kind: kernel.KindEscape, Lane: "fix", At: t0, Unit: "pkg/a", Stage: kernel.StageTrunk, EscapeID: "esc-1", Test: "TestA", Tree: "t1"}
	if _, err := eng.Handle(bounded(t), escape); err != nil {
		t.Fatalf("Handle(escape): %v", err)
	}
	first, err := eng.Handle(bounded(t), untestedWrite(true))
	if err != nil {
		t.Fatalf("Handle(first): %v", err)
	}
	if first.Outcome != kernel.OutcomeGuide || first.Rule != "escape-hold" {
		t.Fatalf("first = %s %q, want an escape-hold guide: a hold guides at every level", first.Outcome, first.Rule)
	}
	second, err := eng.Handle(bounded(t), untestedWrite(true))
	if err != nil {
		t.Fatalf("Handle(second): %v", err)
	}
	if second.Outcome != kernel.OutcomeAllow {
		t.Errorf("second = %s %q, want allow: the hold line comes once per unit per lane", second.Outcome, second.Rule)
	}
	if rec, _ := load(t, store, "fix"); !rec.Units["pkg/a"].GuidedHeld || rec.Units["pkg/a"].GuidedUntested {
		t.Errorf("unit = %+v, want only the held flag", rec.Units["pkg/a"])
	}
}

func TestHandle_oneAttemptIsOneAttemptNotTheDefault(t *testing.T) {
	eng, store := newEngine(t, kernel.Config{})
	f := &faulty{Store: store, conflicts: 100}
	eng.Store, eng.Attempts = f, 1
	if _, err := eng.Handle(bounded(t), edit("pkg/a", kernel.ClassTest, "t1")); !errors.Is(err, ErrContended) {
		t.Fatalf("err = %v, want ErrContended", err)
	}
	if f.commits != 1 {
		t.Errorf("commits = %d, want 1: Attempts = 1 is one try, the default only stands for an unset field", f.commits)
	}
}

// TestHandle_aQuestionsFlagIsNotAUnitTheNextCommitSeeds is the rapid seed
// 13519419159619137621: a guided write stores an entry only to hold its flag,
// and a unit-less gated commit then seeded that entry's last real verdict, so a
// question had changed what the facts say.
func TestHandle_aQuestionsFlagIsNotAUnitTheNextCommitSeeds(t *testing.T) {
	eng, store := newEngine(t, kernel.Config{})
	if _, err := eng.Handle(bounded(t), untestedWrite(true)); err != nil {
		t.Fatalf("Handle(question): %v", err)
	}
	gated := kernel.Event{Kind: kernel.KindCommitGated, Lane: "fix", At: t0, Worktree: "wt", Base: "main", OS: "linux"}
	if _, err := eng.Handle(bounded(t), gated); err != nil {
		t.Fatalf("Handle(commit.gated): %v", err)
	}
	rec, _ := load(t, store, "fix")
	if len(rec.Units) != 0 || !rec.Guided["pkg/a"].Untested {
		t.Errorf("units = %+v, guided = %+v; want no unit and the flag kept: no fact ever mentioned pkg/a", rec.Units, rec.Guided)
	}
}

func TestHandle_aFactAboutAFlaggedUnitMovesTheFlagIntoTheUnit(t *testing.T) {
	eng, store := newEngine(t, kernel.Config{})
	if _, err := eng.Handle(bounded(t), untestedWrite(true)); err != nil {
		t.Fatalf("Handle(question): %v", err)
	}
	if _, err := eng.Handle(bounded(t), edit("pkg/a", kernel.ClassCode, "t1")); err != nil {
		t.Fatalf("Handle(edit): %v", err)
	}
	rec, _ := load(t, store, "fix")
	if !rec.Units["pkg/a"].GuidedUntested || len(rec.Guided) != 0 {
		t.Errorf("units = %+v, guided = %+v; want the flag on the unit the fact created and none left aside", rec.Units, rec.Guided)
	}
	d, err := eng.Handle(bounded(t), untestedWrite(true))
	if err != nil || d.Outcome != kernel.OutcomeAllow {
		t.Errorf("second question = %s, %v; want allow: the line was already given", d.Outcome, err)
	}
}
