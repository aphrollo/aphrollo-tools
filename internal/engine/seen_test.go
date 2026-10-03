package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/render"
)

const (
	parent = "s1/"
	child  = "s1/a26c"
)

func greenFor(unit, tree string) render.Line {
	return render.Green(render.Run{Unit: unit, Tree: tree, Job: "j-" + tree, Verdict: kernel.VerdictGreen, Passed: 3})
}

func redFor(unit, tree string) render.Line {
	return render.Red(render.Run{Unit: unit, Test: "TestX", Tree: tree, Job: "j-" + tree, Verdict: kernel.VerdictRed, Assertion: "want 1, got 2"})
}

func ids(ls []render.Line) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.ID)
	}
	return out
}

func sameLines(got, want []render.Line) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].ID != want[i].ID {
			return false
		}
	}
	return true
}

func TestDeliver_recordsOnlyADeliveryThroughAHookThatReachesTheAgent(t *testing.T) {
	line := greenFor("pkg/a", "t1")
	for _, h := range []render.Hook{render.HookSetup, render.HookCwdChanged, render.HookSessionEnd, "FromTheFuture", ""} {
		eng, store := newEngine(kernel.Config{})
		if err := eng.Deliver(bounded(t), "fix", parent, h, []render.Line{line}); err != nil {
			t.Fatalf("Deliver(%q): %v", h, err)
		}
		if _, ver := load(t, store, "fix"); ver != 0 {
			t.Errorf("Deliver through %q saved the lane (version %d): a delivery nobody read must leave no trace", h, ver)
		}
		got, err := eng.Unseen(bounded(t), "fix", parent, []render.Line{line})
		if err != nil || !sameLines(got, []render.Line{line}) {
			t.Errorf("after a delivery through %q Unseen = %v, %v; want the line still due", h, ids(got), err)
		}
	}
	eng, _ := newEngine(kernel.Config{})
	if err := eng.Deliver(bounded(t), "fix", parent, render.HookPostToolBatch, []render.Line{line}); err != nil {
		t.Fatalf("Deliver(PostToolBatch): %v", err)
	}
	got, err := eng.Unseen(bounded(t), "fix", parent, []render.Line{line})
	if err != nil || len(got) != 0 {
		t.Errorf("after a PostToolBatch delivery Unseen = %v, %v; want nothing due", ids(got), err)
	}
}

func TestDeliver_neverRecordsALineThatSaysNothingOrTwice(t *testing.T) {
	eng, store := newEngine(kernel.Config{})
	line := greenFor("pkg/a", "t1")
	for range 3 {
		if err := eng.Deliver(bounded(t), "fix", parent, render.HookPostToolBatch, []render.Line{{}, line, line}); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
	}
	rec, ver := load(t, store, "fix")
	if len(rec.Delivered) != 1 {
		t.Errorf("Delivered = %+v, want the one line once: an empty line would be recorded for ever, a repeat would crowd the bound", rec.Delivered)
	}
	if ver != 1 {
		t.Errorf("version = %d, want 1: delivering what is already recorded saves nothing", ver)
	}
}

func TestDeliver_needsALaneAndALiveContext(t *testing.T) {
	eng, _ := newEngine(kernel.Config{})
	if err := eng.Deliver(bounded(t), "", parent, render.HookPostToolBatch, []render.Line{greenFor("a", "t")}); !errors.Is(err, ErrNoLane) {
		t.Errorf("Deliver with no lane = %v, want ErrNoLane", err)
	}
	ctx, cancel := context.WithCancel(bounded(t))
	cancel()
	if err := eng.Deliver(ctx, "fix", parent, render.HookPostToolBatch, []render.Line{greenFor("a", "t")}); err == nil {
		t.Error("Deliver on a cancelled context returned nil: the caller would think the delivery was kept")
	}
}

func TestUnseen_anUnseenRedIsNeverHiddenBehindALaterSeenGreen(t *testing.T) {
	eng, _ := newEngine(kernel.Config{})
	red := redFor("pkg/a", "t1")
	green := greenFor("pkg/b", "t2")
	// The green of another unit is delivered after the red was produced; a
	// high-water mark of "delivered up to here" would now hide the red.
	if err := eng.Deliver(bounded(t), "fix", parent, render.HookPostToolBatch, []render.Line{green}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	got, err := eng.Unseen(bounded(t), "fix", parent, []render.Line{red, green})
	if err != nil {
		t.Fatalf("Unseen: %v", err)
	}
	if !sameLines(got, []render.Line{red}) {
		t.Errorf("Unseen = %v, want only the red %v", ids(got), red.ID)
	}
}

func TestUnseen_isPerActor(t *testing.T) {
	eng, _ := newEngine(kernel.Config{})
	line := greenFor("pkg/a", "t1")
	if err := eng.Deliver(bounded(t), "fix", child, render.HookSubagentStart, []render.Line{line}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	for who, want := range map[string]int{child: 0, parent: 1} {
		got, err := eng.Unseen(bounded(t), "fix", who, []render.Line{line})
		if err != nil || len(got) != want {
			t.Errorf("Unseen for %q = %v, %v; want %d lines: a subagent's context is not its parent's", who, ids(got), err, want)
		}
	}
}

func TestUnseen_dropsARepeatInsideOneCallAndKeepsOrder(t *testing.T) {
	eng, _ := newEngine(kernel.Config{})
	a, b := redFor("pkg/a", "t1"), greenFor("pkg/b", "t2")
	got, err := eng.Unseen(bounded(t), "fix", parent, []render.Line{a, b, a, {}})
	if err != nil || !sameLines(got, []render.Line{a, b}) {
		t.Errorf("Unseen = %v, %v; want %v then %v once each", ids(got), err, a.ID, b.ID)
	}
}

func TestHandle_keepsDeliveriesAcrossFactsAndQuestions(t *testing.T) {
	eng, store := newEngine(kernel.Config{})
	line := greenFor("pkg/a", "t1")
	if err := eng.Deliver(bounded(t), "fix", parent, render.HookPostToolBatch, []render.Line{line}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if _, err := eng.Handle(bounded(t), edit("pkg/a", kernel.ClassCode, "t2")); err != nil {
		t.Fatalf("Handle(fact): %v", err)
	}
	if _, err := eng.Handle(bounded(t), untestedWrite(true)); err != nil {
		t.Fatalf("Handle(question): %v", err)
	}
	rec, _ := load(t, store, "fix")
	if len(rec.Delivered) != 1 || rec.Delivered[0].ID != line.ID {
		t.Errorf("Delivered = %+v after a fact and a flag-setting question, want the delivery kept: saving the lane must not erase what the agent has seen", rec.Delivered)
	}
}

func TestDeliver_keepsTheNewestPerActorAndTheNewestActors(t *testing.T) {
	eng, store := newEngine(kernel.Config{})
	var pool []render.Line
	for i := range MaxSeenPerActor * 3 {
		pool = append(pool, greenFor("pkg/a", fmt.Sprintf("t%d", i)))
	}
	for _, l := range pool {
		if err := eng.Deliver(bounded(t), "fix", parent, render.HookPostToolBatch, []render.Line{l}); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
	}
	rec, _ := load(t, store, "fix")
	if len(rec.Delivered) != MaxSeenPerActor {
		t.Fatalf("kept %d deliveries for one actor, want %d", len(rec.Delivered), MaxSeenPerActor)
	}
	oldest, newest := pool[len(pool)-MaxSeenPerActor], pool[len(pool)-1]
	if rec.Delivered[0].ID != oldest.ID || rec.Delivered[len(rec.Delivered)-1].ID != newest.ID {
		t.Errorf("kept %v..%v, want the newest %d lines %v..%v", rec.Delivered[0].ID, rec.Delivered[len(rec.Delivered)-1].ID, MaxSeenPerActor, oldest.ID, newest.ID)
	}
	if got, _ := eng.Unseen(bounded(t), "fix", parent, pool[:1]); len(got) != 1 {
		t.Error("the evicted oldest line reads as seen: eviction must make a line due again, never hide it")
	}

	// One actor per delivery, more actors than the bound keeps.
	eng, store = newEngine(kernel.Config{})
	line := greenFor("pkg/b", "t1")
	for i := range MaxSeenActors * 2 {
		if err := eng.Deliver(bounded(t), "fix", fmt.Sprintf("s%d/a", i), render.HookSubagentStart, []render.Line{line}); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
	}
	rec, _ = load(t, store, "fix")
	actors := map[string]bool{}
	for _, d := range rec.Delivered {
		actors[d.Actor] = true
	}
	if len(actors) != MaxSeenActors {
		t.Errorf("deliveries kept for %d actors, want %d", len(actors), MaxSeenActors)
	}
	last := fmt.Sprintf("s%d/a", MaxSeenActors*2-1)
	if !actors[last] {
		t.Errorf("the newest actor %q was dropped", last)
	}
	if got, _ := eng.Unseen(bounded(t), "fix", "s0/a", []render.Line{line}); len(got) != 1 {
		t.Error("the oldest actor's lines read as seen after it was evicted")
	}
}

func TestGuidance_aGuideSeenByThisActorIsNotRepeatedButADenyAlwaysIs(t *testing.T) {
	eng, _ := newEngine(kernel.Config{})
	d, line, err := eng.Guidance(bounded(t), untestedWrite(true), "d-1")
	if err != nil || d.Outcome != kernel.OutcomeGuide || line.Kind != render.KindGuide || line.Text == "" {
		t.Fatalf("first Guidance = %s %q %v, want a guide line", d.Outcome, line.Kind, err)
	}
	// The kernel's own guided-once flag is cleared by a fresh lane, so the same
	// decision comes again: only the seen record can stop the repeat.
	fresh, _ := newEngine(kernel.Config{})
	ev := untestedWrite(true)
	if err := fresh.Deliver(bounded(t), "fix", ev.Actor, render.HookPostToolBatch, []render.Line{line}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	d, again, err := fresh.Guidance(bounded(t), ev, "d-1")
	if err != nil || d.Outcome != kernel.OutcomeGuide {
		t.Fatalf("Guidance on a seen guide: decision %s, err %v; the decision itself is still the kernel's", d.Outcome, err)
	}
	if again.Text != "" {
		t.Errorf("a guide this actor already saw was returned again: %q", again.Text)
	}
	bystander, _ := newEngine(kernel.Config{})
	if err := bystander.Deliver(bounded(t), "fix", child, render.HookPostToolBatch, []render.Line{line}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if _, l, err := bystander.Guidance(bounded(t), ev, "d-1"); err != nil || l.Text == "" {
		t.Errorf("a guide delivered to another actor was withheld: line %q, %v", l.Text, err)
	}

	enforce, _ := newEngine(kernel.Config{Rules: map[string]kernel.Level{"red-green": kernel.LevelEnforce}})
	for i := range 2 {
		d, l, err := enforce.Guidance(bounded(t), untestedWrite(true), "d-1")
		if err != nil || d.Outcome != kernel.OutcomeDeny || l.Kind != render.KindDeny {
			t.Fatalf("deny %d = %s %q, %v; want the deny line every time", i, d.Outcome, l.Kind, err)
		}
		if err := enforce.Deliver(bounded(t), "fix", parent, render.HookPostToolBatch, []render.Line{l}); err != nil {
			t.Fatalf("Deliver(deny): %v", err)
		}
	}
}

func TestDeliver_racingDeliveriesAndFactsLoseNoUpdate(t *testing.T) {
	store := NewMemStore()
	gate := newRendezvous(store, 3)
	eng := &Engine{Store: gate}
	a, b := greenFor("pkg/a", "t1"), greenFor("pkg/b", "t2")
	wait(t, []func() error{
		func() error {
			return eng.Deliver(bounded(t), "fix", parent, render.HookPostToolBatch, []render.Line{a})
		},
		func() error {
			return eng.Deliver(bounded(t), "fix", child, render.HookSubagentStart, []render.Line{b})
		},
		func() error { _, err := eng.Handle(bounded(t), edit("pkg/a", kernel.ClassCode, "t2")); return err },
	})
	rec, ver := load(t, store, "fix")
	if len(rec.Delivered) != 2 {
		t.Errorf("Delivered = %+v, want both deliveries: the loser of the race must reload and add its own to the winner's", rec.Delivered)
	}
	if rec.Units["pkg/a"].Tree != "t2" {
		t.Errorf("unit = %+v, want the racing edit kept beside the deliveries", rec.Units["pkg/a"])
	}
	if ver != 3 {
		t.Errorf("version = %d, want 3: three commits, each on the one before", ver)
	}
}

func TestDeliver_givesUpOnALaneThatKeepsChanging(t *testing.T) {
	store := NewMemStore()
	eng := &Engine{Store: &alwaysConflicts{Store: store}, Attempts: 3}
	err := eng.Deliver(bounded(t), "fix", parent, render.HookPostToolBatch, []render.Line{greenFor("pkg/a", "t1")})
	if !errors.Is(err, ErrContended) {
		t.Errorf("Deliver = %v, want ErrContended: the caller must know the line was not recorded", err)
	}
}

// alwaysConflicts loses every commit the way a lane someone else keeps saving does.
type alwaysConflicts struct{ Store }

func (alwaysConflicts) Commit(context.Context, string, uint64, Record, []kernel.Event) (uint64, error) {
	return 0, ErrConflict
}
