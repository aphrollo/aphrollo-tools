package shadow

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/aphrollo/aphrollo-tools/internal/engine"
	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/store"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// The rules these adapters shadow, and the hooks their records come from.
const (
	RuleRedGreen = "red-green"
	RuleStopRed  = "stop-red"
	RuleFold     = "lane-fold"

	HookStop         = "stop"
	HookSubagentStop = "subagentstop"
	HookFold         = "posttooluse-fold"
)

// The causes an unjudged record carries when a step could not be made.
const (
	CauseBudget = "budget"   // the record did not finish inside Budget
	CauseNoLane = "no-lane"  // the checkout has no branch to key a lane record by
	CauseNoUnit = "no-unit"  // the file is in no project a unit can be named for
	CauseNoTree = "no-tree"  // the run recorded no tree key to fold its verdict under
	CauseStore  = "store"    // the lane record could not be read or saved
	causeNoEdit = "no-edits" // the run's edits are not in the ledger
)

// Config is what the kernel decides these two rules under: tdd = enforce, the
// level the document gives both red→green (§5) and Stop (§4: "blocks once on an
// unseen red under enforce"). Aphrollo's live Stop blocks, and its commit gate
// refuses a missing proof, so comparing either to the kernel's default (warn for
// red-green, off for stop-red, which then never fires) would record disagreement
// that is only a difference of level.
var Config = kernel.Config{TDD: kernel.ModeEnforce}

// World is what the adapters read of the box. Every read is a file read: none
// spawns git, so a record's cost is a few small reads and the writes of the
// lane record. The seams are the live ones (internal/tdd) in production.
type World struct {
	Lane        func(root string) string                // the branch checked out at root, "" for none
	ProjectRoot func(file string) string                // the edit hook's project root of a file
	Edits       func(root string) []LedgerEdit          // the edit ledger of the checkout, oldest first
	Open        func(root string) (*store.Store, error) // the repo's store
}

func (w World) unitOf() func(string) (Unit, bool) {
	seen := map[string]Unit{}
	known := map[string]bool{}
	return func(file string) (Unit, bool) {
		key := filepath.Dir(file) + "|" + filepath.Ext(file)
		if known[key] {
			u, ok := seen[key]
			return u, ok
		}
		u, ok := UnitOf(file, w.ProjectRoot)
		known[key] = true
		if ok {
			seen[key] = u
		}
		return u, ok
	}
}

func (w World) engine(root string) (*engine.Engine, *store.Store, error) {
	st, err := w.Open(root)
	if err != nil {
		return nil, nil, err
	}
	return &engine.Engine{Store: st, Config: Config}, st, nil
}

// covered is Covered for the unit as the lane record and the store hold it: the
// unit's last green run's tree, its verdict file, and the ledger's edits.
func (w World) covered(ctx context.Context, st *store.Store, lane string, u Unit, edits []LedgerEdit, unitOf func(string) (Unit, bool)) bool {
	rec, _, err := st.Load(ctx, lane)
	if err != nil {
		return false
	}
	last := rec.Units[u.ID]
	if last.LastReal != kernel.VerdictGreen || last.LastRealTree == "" {
		return false
	}
	v, found, err := st.ReadVerdict(last.LastRealTree)
	if err != nil || !found {
		return false
	}
	return Covered(u, v.Runs, edits, unitOf)
}

// unjudged is the record of a step that could not be made, and why.
func unjudged(hook, rule, unit, cause string) Record {
	return Record{Hook: hook, Rule: rule, Relation: Unjudged, Cause: cause, Unit: unit}
}

// causeOf is the unjudged cause of an error of the engine or the store: the
// window closing is the budget, anything else the store.
func causeOf(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return CauseBudget
	}
	return CauseStore
}

// RedGreen asks the kernel the question each code file a PreToolUse call is about
// to write puts to the red→green rule, and compares the answer to aphrollo's: the
// live hook allows every one of them, for red→green is held at the commit, so any
// guide or block of the kernel's is a would-be block (trellis-stricter).
//
// The question is put to the lane's real record through the engine, so it reads
// the unit's phase as the runs and edits folded so far have left it (FoldRun). The
// engine takes no lock to answer, and takes the lane's lock only to keep its
// guided-once flag; that lock is the lane record's own, which the live run recorder
// (it writes the verdict files, under their own lock) never takes, so the question
// cannot hold a live write up. Every wait is bound to ctx, the hook's budget. Only
// code files are asked: a test edit is never refused by this rule.
func (w World) RedGreen(ctx context.Context, p Payload, files []string) []Record {
	var out []Record
	unitOf := w.unitOf()
	ledgers := map[string][]LedgerEdit{}
	asked := map[string]bool{}
	for _, f := range files {
		if FileClassOf(f) != kernel.ClassCode {
			continue
		}
		root := filepath.Dir(f) // the checkout the write lands in, which a shell command's need not be the cwd's
		u, ok := unitOf(f)
		if !ok {
			out = append(out, withRoot(unjudged(HookPre, RuleRedGreen, "", CauseNoUnit), root))
			continue
		}
		lane := w.Lane(root)
		key := lane + "|" + u.ID
		if asked[key] {
			continue
		}
		asked[key] = true
		if lane == "" {
			out = append(out, withRoot(unjudged(HookPre, RuleRedGreen, u.ID, CauseNoLane), root))
			continue
		}
		eng, st, err := w.engine(root)
		if err != nil {
			out = append(out, withRoot(unjudged(HookPre, RuleRedGreen, u.ID, CauseStore), root))
			continue
		}
		pr := w.ProjectRoot(f)
		if _, ok := ledgers[pr]; !ok {
			ledgers[pr] = w.Edits(pr)
		}
		ev := PreEvent(p, lane, Edit{File: f, Class: kernel.ClassCode, Unit: u, Covered: w.covered(ctx, st, lane, u, ledgers[pr], unitOf)})
		d, err := eng.Handle(ctx, ev)
		if err != nil && d.Outcome == "" {
			out = append(out, withRoot(unjudged(HookPre, RuleRedGreen, u.ID, causeOf(err)), root))
			continue
		}
		r := recordOf(HookPre, RuleRedGreen, "commit-proof", d, Allow)
		r.Unit = u.ID
		out = append(out, withRoot(r, root))
	}
	return out
}

func withRoot(r Record, root string) Record {
	r.Root = root
	return r
}

// Stop asks the kernel the question a Stop or SubagentStop puts to the stop-red
// rule when aphrollo's live check blocked on a red the actor had not seen, and
// compares the answers. The kernel reads the lane's units for a real red; the
// unseen red is aphrollo's own finding, the one fact the kernel cannot hold.
func (w World) Stop(ctx context.Context, hook string, p Payload, root string) Record {
	lane := w.Lane(root)
	if lane == "" {
		return unjudged(hook, RuleStopRed, "", CauseNoLane)
	}
	eng, _, err := w.engine(root)
	if err != nil {
		return unjudged(hook, RuleStopRed, "", CauseStore)
	}
	d, err := eng.Handle(ctx, StopEvent(p, lane, true))
	if err != nil && d.Outcome == "" {
		return unjudged(hook, RuleStopRed, "", causeOf(err))
	}
	return recordOf(hook, RuleStopRed, "unseen-red", d, Block)
}

// Fold is a finished run to fold into the lane record: the edits it judged (their
// ledger ids), the tree it measured and its verdict.
type Fold struct {
	Root, Actor, Tree, Job string
	EditIDs                []string
	Verdict                kernel.Verdict
	Cause                  string
}

// FoldRun folds a finished run into the lane record: for each unit the run's
// edits touched, those edits (with the tree the run measured, which is the tree
// they produced: only the run reads the tree key, and no git process is spawned
// for it) and then the run's result. An edit's Covered is the definition of
// Covered evaluated with this run in hand: the run is the unit's run on a tree
// holding the edit, so it covers it exactly when it is green. It returns the
// cause when the fold could not be made, "" when it was.
func (w World) FoldRun(ctx context.Context, f Fold) string {
	lane := w.Lane(f.Root)
	switch {
	case lane == "":
		return CauseNoLane
	case f.Tree == "":
		return CauseNoTree
	}
	unitOf := w.unitOf()
	type unitEdits struct {
		unit  Unit
		edits []Edit
	}
	var order []string
	byUnit := map[string]*unitEdits{}
	for _, e := range w.Edits(f.Root) {
		if !slices.Contains(f.EditIDs, e.ID) {
			continue
		}
		class := FileClassOf(e.File)
		u, ok := unitOf(e.File)
		if class == kernel.ClassOther || !ok {
			continue
		}
		if byUnit[u.ID] == nil {
			byUnit[u.ID] = &unitEdits{unit: u}
			order = append(order, u.ID)
		}
		byUnit[u.ID].edits = append(byUnit[u.ID].edits, Edit{File: e.File, Class: class, Unit: u, Tree: f.Tree, Covered: f.Verdict == kernel.VerdictGreen})
	}
	if len(order) == 0 {
		return causeNoEdit
	}
	eng, _, err := w.engine(f.Root)
	if err != nil {
		return CauseStore
	}
	for _, id := range order {
		ue := byUnit[id]
		for _, e := range ue.edits {
			if _, err := eng.Handle(ctx, EditEvent(f.Actor, lane, e)); err != nil {
				return causeOf(err)
			}
		}
		if _, err := eng.Handle(ctx, RunEvent(f.Actor, lane, id, f.Tree, f.Job, f.Verdict, f.Cause)); err != nil {
			return causeOf(err)
		}
	}
	return ""
}

// A Step is one record of a hook, made inside the hook's one window: run makes the
// events to write, skip is the unjudged record written for it when the window
// closed before it was committed.
type Step struct {
	run  func(ctx context.Context) []core.Event
	skip func(cause string) core.Event
}

// Overruns counts the steps that did not finish inside the budget, since the
// process began. Each also left an unjudged record carrying CauseBudget.
var Overruns atomic.Int64

// window commits a hook's steps one by one under a lock, so that after the hook
// moves on, the steps that did not commit are written as unjudged by exactly one
// of the two writers: the step's goroutine, if it commits first, or the hook.
type window struct {
	mu     sync.Mutex
	closed bool
	done   int
}

func (w *window) commit(i int, evs []core.Event) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	for _, e := range evs {
		appendEvent(e)
	}
	w.done = i + 1
	return true
}

// close ends the window and returns how many steps committed.
func (w *window) close() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return w.done
}

// runSteps runs steps in order until ctx ends or the window closes.
func runSteps(ctx context.Context, w *window, steps []Step) {
	for i, st := range steps {
		if ctx.Err() != nil {
			return
		}
		if !w.commit(i, st.run(ctx)) {
			return
		}
	}
}

// settle writes the unjudged record of every step the window did not commit.
func settle(w *window, steps []Step) {
	for _, st := range w.skippedFrom(steps) {
		Overruns.Add(1)
		appendEvent(st.skip(CauseBudget))
	}
}

func (w *window) skippedFrom(steps []Step) []Step {
	n := w.close()
	if n >= len(steps) {
		return nil
	}
	return steps[n:]
}

// The store serves the engine.
var _ engine.Store = (*store.Store)(nil)
