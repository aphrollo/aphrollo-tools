package shadow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	RuleFacts    = "facts" // the facts of a PreToolUse call that did not finish inside the budget: which they were is not known

	HookStop         = "stop"
	HookSubagentStop = "subagentstop"
	HookFold         = "posttooluse-fold"
)

// The causes an unjudged record carries when a step could not be made.
const (
	CauseBudget   = "budget"   // the record did not finish inside Budget
	CauseNoLane   = "no-lane"  // the checkout has no branch to key a lane record by
	CauseNoUnit   = "no-unit"  // the file is in no project a unit can be named for
	CauseNoTree   = "no-tree"  // the run recorded no tree key to fold its verdict under
	CauseStore    = "store"    // the lane record could not be read or saved
	CauseNoEdit   = "no-edit"  // the edits the run judged are not in the ledger, or none is code
	CauseUnfolded = "unfolded" // the live red is one no run of the lane record was folded for
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

// skipLane reports whether a lane is one the shadow does not follow: a trunk
// branch (main, master, @trunk), and the primary checkout of a repo that has linked
// worktrees (its .git is a directory with entries under worktrees/), whose branch is
// the trunk the lanes are merged into whatever it is called. A trunk lane's outcomes
// cannot be joined to a change; a call there is no ask, no fold and no record. A
// plain clone with no linked worktree, on a feature branch, is followed.
func skipLane(root, lane string) bool {
	switch lane {
	case "main", "master", kernel.TrunkLane:
		return true
	}
	repo := findUp(root, ".git")
	if repo == "" {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(repo, ".git", "worktrees"))
	return err == nil && len(entries) > 0
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
	return Record{Hook: hook, Rule: rule, Relation: Unjudged, Cause: cause, Unit: unit, Lang: LangOfUnit(unit)}
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
		if skipLane(root, lane) {
			continue
		}
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
		ev := PreEvent(p, lane, Edit{File: f, Class: kernel.ClassCode, Unit: u, Covered: w.covered(ctx, st, lane, u, ledgers[pr], unitOf), AddsSymbol: AddsSymbol(p, f)})
		d, err := eng.Handle(ctx, ev)
		if err != nil && d.Outcome == "" {
			out = append(out, withRoot(unjudged(HookPre, RuleRedGreen, u.ID, causeOf(err)), root))
			continue
		}
		r := recordOf(HookPre, RuleRedGreen, "commit-proof", d, Allow)
		r.Unit, r.UnitPkg, r.Lang = u.ID, u.Pkg, LangOfUnit(u.ID)
		if u.Kind == unitProjectRoot {
			r.UnitRoot = filepath.ToSlash(pr)
		}
		out = append(out, withRoot(r, root))
	}
	return out
}

func withRoot(r Record, root string) Record {
	r.Root = root
	return r
}

// StopFacts are aphrollo's own findings at a Stop or SubagentStop: whether the actor
// has an unseen red outstanding (whatever the live check then did about it), the
// trees of the lane's reds it is about, and whether the live check blocked.
type StopFacts struct {
	Unseen  bool
	Trees   []string
	Blocked bool
}

// Stop asks the kernel the question a Stop or SubagentStop puts to the stop-red rule
// at every stop, and compares the answer to aphrollo's (a block, or an allow), so a
// would-be block (aphrollo allowed with an unseen red outstanding) is as observable
// as a block. The kernel reads the lane's units for a real red; the unseen red is
// aphrollo's own finding, the one fact the kernel cannot hold. A red no run of the
// lane record was folded for (no unit of it measured on the red's tree) is unjudged:
// the record cannot say whether the kernel would have blocked. It reports false for
// a stop on a lane the shadow does not follow.
func (w World) Stop(ctx context.Context, hook string, p Payload, root string, f StopFacts) (Record, bool) {
	lane := w.Lane(root)
	if skipLane(root, lane) {
		return Record{}, false
	}
	if lane == "" {
		return unjudged(hook, RuleStopRed, "", CauseNoLane), true
	}
	eng, st, err := w.engine(root)
	if err != nil {
		return unjudged(hook, RuleStopRed, "", CauseStore), true
	}
	if f.Unseen {
		rec, _, err := st.Load(ctx, lane)
		if err != nil {
			return unjudged(hook, RuleStopRed, "", causeOf(err)), true
		}
		if !foldedRed(rec.Units, f.Trees) {
			return unjudged(hook, RuleStopRed, "", CauseUnfolded), true
		}
	}
	actual := Allow
	if f.Blocked {
		actual = Block
	}
	d, err := eng.Handle(ctx, StopEvent(p, lane, f.Unseen))
	if err != nil && d.Outcome == "" {
		return unjudged(hook, RuleStopRed, "", causeOf(err)), true
	}
	return recordOf(hook, RuleStopRed, "unseen-red", d, actual), true
}

// foldedRed reports whether some unit of the record last measured one of the trees.
func foldedRed(units kernel.Units, trees []string) bool {
	for _, u := range units {
		if u.LastRealTree != "" && slices.Contains(trees, u.LastRealTree) {
			return true
		}
	}
	return false
}

// EditFold is a finished edit to fold into its lane's record: the ledger id the
// edit hook gave it and its file.
type EditFold struct {
	Root, Actor, EditID, File string
}

// FoldEdit folds an edit into the lane's record the moment the hook has made it,
// with no run needed: a test edit moves its unit to pending before any run
// finishes, which is what the next code edit's question has to read. The edit's
// tree is unknown here (only a run reads the tree key), so it carries none; the run
// that later judges it brings the unit's tree up to date (FoldRun) without folding
// the edit a second time. Covered is the unit's cover as it stood before this edit,
// for the edit is itself newer than any run. It returns the cause when the fold
// could not be made, "" when it was or the lane is not followed.
func (w World) FoldEdit(ctx context.Context, f EditFold) string {
	lane := w.Lane(f.Root)
	if skipLane(f.Root, lane) {
		return ""
	}
	if lane == "" {
		return CauseNoLane
	}
	class := FileClassOf(f.File)
	if class == kernel.ClassOther {
		return ""
	}
	unitOf := w.unitOf()
	u, ok := unitOf(f.File)
	if !ok {
		return CauseNoUnit
	}
	eng, st, err := w.engine(f.Root)
	if err != nil {
		return CauseStore
	}
	var before []LedgerEdit
	for _, e := range w.Edits(f.Root) {
		if !slices.Contains(strings.Split(f.EditID, ","), e.ID) { // a shell call's edits share one joined id
			before = append(before, e)
		}
	}
	ev := EditEvent(f.Actor, lane, Edit{File: f.File, Class: class, Unit: u, Covered: w.covered(ctx, st, lane, u, before, unitOf)})
	if err := eng.HandleAll(ctx, lane, []kernel.Event{ev}); err != nil {
		return causeOf(err)
	}
	return ""
}

// Fold is a finished run to fold into the lane record: the edits it judged (their
// ledger ids), the command it ran, the tree it measured and its verdict.
type Fold struct {
	Root, Actor, Tree, Job string
	EditIDs                []string
	Argv                   []string // the run's command; nil when unknown, which covers every unit
	Verdict                kernel.Verdict
	Cause                  string
}

// FoldRun folds a finished run's result into the lane record, in one transaction.
// The edits it judged are folded already (FoldEdit); for each unit they touched
// that the run's command covers (runCovers, so a run of one of two touched packages
// stamps only that one) the run brings the unit's tree up to the one it measured
// (the tree-only edit event TreeEvent, no state moves) and then gives its result.
// A unit with a newer edit than the run judged is left alone: the run is stale for
// it. All of it is saved or none of it is: a context that ends mid-fold leaves the
// record as it was. It returns the cause when the fold could not be made, "" when
// it was or there was nothing for it to do.
func (w World) FoldRun(ctx context.Context, f Fold) string {
	lane := w.Lane(f.Root)
	if skipLane(f.Root, lane) {
		return ""
	}
	switch {
	case lane == "":
		return CauseNoLane
	case f.Tree == "":
		return CauseNoTree
	}
	unitOf := w.unitOf()
	ledger := w.Edits(f.Root)
	newest := map[string]string{} // unit id to the id of its newest ledger edit
	for _, e := range ledger {
		if u, ok := unitOf(e.File); ok && FileClassOf(e.File) != kernel.ClassOther {
			newest[u.ID] = e.ID
		}
	}
	var order []string
	units := map[string]Unit{}
	for _, e := range ledger {
		if !slices.Contains(f.EditIDs, e.ID) || FileClassOf(e.File) == kernel.ClassOther {
			continue
		}
		if u, ok := unitOf(e.File); ok && !slices.Contains(order, u.ID) {
			order = append(order, u.ID)
			units[u.ID] = u
		}
	}
	if len(order) == 0 {
		return CauseNoEdit
	}
	runUnit := ""
	if f.Argv != nil {
		runUnit = relOrDot(findUp(f.Root, ".git"), f.Root) + "|" + strings.Join(f.Argv, " ")
	}
	var evs []kernel.Event
	for _, id := range order {
		u := units[id]
		if runUnit != "" && !runCovers(runUnit, u) {
			continue
		}
		if !slices.Contains(f.EditIDs, newest[id]) {
			continue
		}
		evs = append(evs, TreeEvent(f.Actor, lane, id, f.Tree), RunEvent(f.Actor, lane, id, f.Tree, f.Job, f.Verdict, f.Cause))
	}
	if len(evs) == 0 {
		return ""
	}
	eng, _, err := w.engine(f.Root)
	if err != nil {
		return CauseStore
	}
	if err := eng.HandleAll(ctx, lane, evs); err != nil {
		return causeOf(err)
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
// of the two writers: the step's goroutine, if it commits first, or the hook. It
// also holds the budget's deadline on the clock: a write after it is refused,
// whether or not the hook has yet got round to closing the window.
type window struct {
	mu       sync.Mutex
	closed   bool
	done     int
	clk      Clock
	deadline time.Time
}

// setDeadline fixes when the window's budget ends on clk.
func (w *window) setDeadline(clk Clock, at time.Time) {
	w.mu.Lock()
	w.clk, w.deadline = clk, at
	w.mu.Unlock()
}

func (w *window) expiredLocked() bool {
	return w.clk != nil && !w.clk.Now().Before(w.deadline)
}

// expired reports whether the budget is spent.
func (w *window) expired() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.expiredLocked()
}

func (w *window) commit(i int, evs []core.Event) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.expiredLocked() {
		return false
	}
	for _, e := range evs {
		appendEvent(e)
	}
	w.done = i + 1
	return true
}

// write records one fact's event unless the hook has closed the window or its
// budget is spent: a fact still being built when the budget ran out is dropped,
// never written into whatever store is current when its goroutine gets there. The
// write itself is outside the lock: a writer that is slow is what the budget is
// for, and the hook closing the window must never wait on it.
func (w *window) write(e core.Event) bool {
	w.mu.Lock()
	closed := w.closed || w.expiredLocked()
	w.mu.Unlock()
	if closed {
		return false
	}
	appendEvent(e)
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
		appendDropped(st.skip(CauseBudget))
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
