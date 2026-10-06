package shadow

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// bjClock is a clock that moves only when a test says so: a slow probe is a call
// that advances it, so the budget is judged on the time the work cost and never on
// the time the box took to run the test.
type bjClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*bjTimer
}

type bjTimer struct {
	at time.Time
	ch chan time.Time
}

func (c *bjClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *bjClock) Timer(d time.Duration) (<-chan time.Time, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &bjTimer{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	return t.ch, func() {}
}

// Advance moves the clock and fires the timers it passes.
func (c *bjClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, t := range c.timers {
		if !t.at.After(c.now) {
			select {
			case t.ch <- c.now:
			default:
			}
		}
	}
}

// bjSetup puts the budget on a fake clock for the test, with the hook's budget.
func bjSetup(t *testing.T) (*bjClock, *[]core.Event) {
	t.Helper()
	got := capture(t)
	clk := &bjClock{now: t0}
	oldClock, oldBudget := clock, Budget
	clock, Budget = clk, 150*time.Millisecond
	t.Cleanup(func() { clock, Budget = oldClock, oldBudget })
	return clk, got
}

func bjRelations(evs []core.Event) map[string]int {
	out := map[string]int{}
	for _, e := range evs {
		out[e.Detail["rule"]+"/"+e.Detail["relation"]+"/"+e.Detail["cause"]]++
	}
	return out
}

// A record whose facts need probes that cost more than the budget is dropped; the
// drop is written, never silent, so the dropped share can be read from the log.
func TestRecordFacts_FactsThatOutrunTheBudgetAreWrittenAsOneUnjudgedBudgetRecord(t *testing.T) {
	clk, got := bjSetup(t)
	before := Overruns.Load()
	slowProbe := func() { clk.Advance(100 * time.Millisecond) }
	RecordFacts(Source{Root: t.TempDir()}, func() []Fact {
		slowProbe()
		slowProbe()
		return []Fact{Discard(Block)}
	})
	want := map[string]int{RuleFacts + "/unjudged/" + CauseBudget: 1}
	if rel := bjRelations(*got); len(rel) != 1 || rel[RuleFacts+"/unjudged/"+CauseBudget] != 1 {
		t.Errorf("events = %v, want %v: the fact is not written and the drop is", rel, want)
	}
	if Overruns.Load() != before+1 {
		t.Errorf("Overruns = %d, want %d", Overruns.Load(), before+1)
	}
}

// The same probe started when the hook has the facts, and waited for in the
// window, costs the window nothing: the hook's own work overlapped it.
func TestRecordFacts_AProbeStartedBeforeTheWindowIsNotPaidInsideIt(t *testing.T) {
	clk, got := bjSetup(t)
	pre := StartPrefetch(func() string {
		clk.Advance(100 * time.Millisecond) // a probe that costs two thirds of the budget
		clk.Advance(100 * time.Millisecond) // and a second one
		return "primary-root"
	})
	<-pre.Done() // the hook went on with its own work meanwhile
	RecordFacts(Source{Root: t.TempDir()}, func() []Fact {
		root, ok := pre.Wait(context.Background())
		if !ok || root != "primary-root" {
			t.Errorf("prefetch = %q, %v", root, ok)
		}
		return []Fact{Discard(Block)}
	})
	if rel := bjRelations(*got); len(rel) != 1 || rel["discard-work/agree/"] != 1 {
		t.Errorf("events = %v, want the one discard-work record, written", rel)
	}
}

func TestPrefetch_WaitEndsWithTheWindowAndNeverBlocksPastIt(t *testing.T) {
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	pre := StartPrefetch(func() int { <-hold; return 1 })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if v, ok := pre.Wait(ctx); ok || v != 0 {
		t.Errorf("Wait on an ended window = %d, %v, want the zero value and false", v, ok)
	}
}

// A run the window closed on is written as an unjudged record of the budget, not
// lost, and the late write of the run that outran it is refused.
func TestFlush_RunsThatOutrunTheBudgetAreWrittenUnjudgedForTheBudget(t *testing.T) {
	clk, got := bjSetup(t)
	src := Source{Root: t.TempDir(), Key: "k"}
	slow := func() (RunFact, bool) {
		clk.Advance(200 * time.Millisecond)
		return RunFact{Word: "green (1 passed)", Verdict: kernel.VerdictGreen}, true
	}
	QueueRun(src, slow)
	QueueRun(src, func() (RunFact, bool) { return RunFact{Word: "red", Verdict: kernel.VerdictRed}, true })
	Flush()
	rel := bjRelations(*got)
	if rel["run-verdict/unjudged/"+CauseBudget] != 2 || len(rel) != 1 {
		t.Errorf("events = %v, want both runs written unjudged for the budget", rel)
	}
}

// The share of records dropped for the budget over 20 hook runs, each with the
// probe costs a loaded box gives it: before the probes are taken off the window,
// and after. The costs are fixed, so the numbers are the model's and not a box's.
func TestRecordFacts_TheDroppedShareOverTwentyLoadedRunsIsUnderTwoPercentOnceProbesAreOffTheWindow(t *testing.T) {
	costs := []time.Duration{30, 45, 60, 80, 120, 200, 40, 55, 90, 160, 35, 70, 50, 65, 74, 45, 85, 45, 35, 60} // ms, two probes each
	share := func(prefetched bool) (dropped, total int) {
		clk, got := bjSetup(t)
		for _, c := range costs {
			cost := c * time.Millisecond
			if prefetched {
				pre := StartPrefetch(func() bool { clk.Advance(cost); clk.Advance(cost); return true })
				<-pre.Done()
				RecordFacts(Source{Root: t.TempDir()}, func() []Fact {
					pre.Wait(context.Background())
					return []Fact{Discard(Block)}
				})
				continue
			}
			RecordFacts(Source{Root: t.TempDir()}, func() []Fact {
				clk.Advance(cost)
				clk.Advance(cost)
				return []Fact{Discard(Block)}
			})
		}
		for _, e := range *got {
			total++
			if e.Detail["cause"] == CauseBudget {
				dropped++
			}
		}
		return dropped, total
	}
	before, total := share(false)
	after, total2 := share(true)
	t.Logf("budget drops over 20 runs: before %d of %d (%.0f%%), after %d of %d (%.0f%%)",
		before, total, 100*float64(before)/float64(total), after, total2, 100*float64(after)/float64(total2))
	if before != 6 {
		t.Errorf("before: %d of %d records dropped, want the 6 of 20 the load gave", before, total)
	}
	if after*50 > total2 { // over 2%
		t.Errorf("after: %d of %d records dropped, want under 2%%", after, total2)
	}
}

// The merge gate's flake: a window already closed when its steps start left no
// record at all when the hook happened to read "finished" from the select, for
// runSteps had returned at the ended context and the call was thought done. A call
// that outruns the budget is always one unjudged record naming it, however the two
// ready channels are read.
func TestRecordFactsAnd_AnOverrunLeavesTheUnjudgedRecordWhicheverWayTheHookReadsIt(t *testing.T) {
	for range 50 {
		clk, got := bjSetup(t)
		step := Step{
			run: func(context.Context) []core.Event {
				clk.Advance(Budget)
				return []core.Event{{Kind: core.KindShadow, Detail: map[string]string{"rule": "late"}}}
			},
			skip: func(cause string) core.Event {
				return unjudged(HookPre, RuleRedGreen, "", cause).event(Source{Root: "r"}, "lane/x")
			},
		}
		RecordFactsAnd(Source{Root: t.TempDir()}, func() []Fact { return nil }, []Step{step})
		if rel := bjRelations(*got); len(rel) != 1 || rel[RuleRedGreen+"/unjudged/"+CauseBudget] != 1 {
			t.Fatalf("events = %v, want the one unjudged red-green record for the budget", rel)
		}
	}
}

// Three steps the window did not commit are three records written under one grace:
// a stuck writer costs the hook DropGrace once, not once per record.
func TestSettle_AStuckWriterCostsTheHookOneGraceForAllTheSkippedSteps(t *testing.T) {
	clk, _ := bjSetup(t)
	hold := make(chan struct{})
	started := make(chan struct{}, 8)
	oldAppend := appendEvent
	appendEvent = func(core.Event) { started <- struct{}{}; <-hold }
	t.Cleanup(func() { appendEvent = oldAppend; close(hold) })
	step := Step{
		run: func(context.Context) []core.Event { return nil },
		skip: func(cause string) core.Event {
			return unjudged(HookPre, RuleRedGreen, "", cause).event(Source{Root: "r"}, "lane/x")
		},
	}
	returned := make(chan struct{})
	go func() {
		settle(&window{}, []Step{step, step, step})
		close(returned)
	}()
	<-started // the writer is stuck on the first record
	clk.Advance(DropGrace)
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("settle waited for a second grace after the first one ended")
	}
}

// A writer stuck in a step's append must not hold the window's lock: the hook
// closing the window at the end of its budget returns at once, and the step it
// claimed is not written a second time as unjudged.
func TestWindow_ACommitStuckInItsAppendDoesNotBlockTheHooksClose(t *testing.T) {
	hold := make(chan struct{})
	started := make(chan struct{}, 1)
	oldAppend := appendEvent
	appendEvent = func(core.Event) { started <- struct{}{}; <-hold }
	t.Cleanup(func() { appendEvent = oldAppend })
	w := &window{}
	committed := make(chan bool, 1)
	go func() { committed <- w.commit(0, []core.Event{{Kind: core.KindShadow}}) }()
	<-started
	closed := make(chan int, 1)
	go func() { closed <- w.close() }()
	select {
	case n := <-closed:
		if n != 1 {
			t.Errorf("close = %d steps committed, want the 1 the stuck commit claimed", n)
		}
	case <-time.After(10 * time.Second):
		t.Error("close waited for a commit stuck in its append")
	}
	close(hold)
	if !<-committed {
		t.Error("the commit that claimed its step reported a refusal")
	}
}
