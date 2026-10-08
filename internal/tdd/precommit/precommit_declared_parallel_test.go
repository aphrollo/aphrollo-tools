package precommit

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/rootseam"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/lock"
)

// dparFake is a runner for declared commands named by their program ("a",
// "b"). It holds a command until the test releases it, counts what runs at
// once, and records what had finished and what was running when each began,
// so concurrency is shown by a blocked fake and never by a sleep.
type dparFake struct {
	mu              sync.Mutex
	weights         map[string]int
	hold            map[string]chan struct{}
	fail            map[string]bool
	dur             map[string]time.Duration
	seq             map[string][]SuiteResult
	timeout         map[string]bool
	running         map[string]bool
	finished        []string
	finishedAtStart map[string][]string
	runningAtStart  map[string][]string
	peakRun         int
	peakWeight      int
	entered         chan string
	exited          chan string
}

func newDparFake(names ...string) *dparFake {
	f := &dparFake{
		weights: map[string]int{}, hold: map[string]chan struct{}{}, fail: map[string]bool{},
		dur: map[string]time.Duration{}, seq: map[string][]SuiteResult{}, timeout: map[string]bool{}, running: map[string]bool{},
		finishedAtStart: map[string][]string{}, runningAtStart: map[string][]string{},
		entered: make(chan string, 64), exited: make(chan string, 64),
	}
	for _, n := range names {
		f.hold[n] = make(chan struct{})
	}
	return f
}

// free lets name's run end; the cleanup frees whatever a failed test left
// held, so no goroutine outlives it.
func (f *dparFake) free(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch, ok := f.hold[name]; ok {
		close(ch)
		delete(f.hold, name)
	}
}

// ours is whether name is a declared command of this test; the gate's other
// runs pass through untouched.
func (f *dparFake) ours(name string) bool {
	_, held := f.hold[name]
	_, weighed := f.weights[name]
	_, timed := f.dur[name]
	return held || weighed || timed || f.fail[name] || f.timeout[name] || len(f.seq[name]) > 0
}

func (f *dparFake) run(r Runner, _ string) SuiteResult {
	name := r.Cmd
	f.mu.Lock()
	ch, holds := f.hold[name]
	if !f.ours(name) {
		f.mu.Unlock()
		return SuiteResult{Passed: true}
	}
	f.finishedAtStart[name] = slices.Clone(f.finished)
	var now []string
	w := 0
	for n := range f.running {
		now = append(now, n)
		w += max(f.weights[n], 1)
	}
	sort.Strings(now)
	f.runningAtStart[name] = now
	f.running[name] = true
	w += max(f.weights[name], 1)
	f.peakRun = max(f.peakRun, len(f.running))
	f.peakWeight = max(f.peakWeight, w)
	f.mu.Unlock()

	f.entered <- name
	if holds {
		<-ch
	}

	f.mu.Lock()
	delete(f.running, name)
	f.finished = append(f.finished, name)
	res := SuiteResult{Passed: !f.fail[name] && !f.timeout[name], Duration: f.dur[name], TimedOut: f.timeout[name]}
	if f.fail[name] {
		res.Output = "err " + name + "\n"
	}
	if q := f.seq[name]; len(q) > 0 {
		res, f.seq[name] = q[0], q[1:]
	}
	f.mu.Unlock()
	f.exited <- name
	return res
}

// waitEntered is the n names that began next, sorted; a run that never
// reaches them fails the test instead of hanging it.
func (f *dparFake) waitEntered(t *testing.T, n int) []string {
	t.Helper()
	var got []string
	for len(got) < n {
		select {
		case name := <-f.entered:
			got = append(got, name)
		case <-time.After(10 * time.Second):
			t.Fatalf("only %v began, want %d commands running", got, n)
		}
	}
	sort.Strings(got)
	return got
}

func (f *dparFake) waitExited(t *testing.T, name string) {
	t.Helper()
	select {
	case got := <-f.exited:
		if got != name {
			t.Fatalf("%s ended, want %s", got, name)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never ended", name)
	}
}

// dparCmd is one inline-table command of the declaration.
func dparCmd(name string, parallel bool, weight int) string {
	s := fmt.Sprintf(`{ argv = ["%s"]`, name)
	if parallel {
		s += ", parallel = true"
	}
	if weight > 0 {
		s += ", weight = " + strconv.Itoa(weight)
	}
	return s + " }"
}

func dparToml(budget int, cmds ...string) string {
	s := "[aphrollo.precommit]\n"
	if budget > 0 {
		s += "parallel-budget = " + strconv.Itoa(budget) + "\n"
	}
	return s + "\".\" = [\n  " + strings.Join(cmds, ",\n  ") + ",\n]\n"
}

// dparSink is a root's stderr that keeps what it is given and lets a test
// wait for a line to appear.
type dparSink struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	changed chan struct{}
}

func (s *dparSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	n, err := s.buf.Write(p)
	s.mu.Unlock()
	select {
	case s.changed <- struct{}{}:
	default:
	}
	return n, err
}

func (s *dparSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// waitFor returns once the text holds want; a line that never comes fails the
// test instead of hanging it.
func (s *dparSink) waitFor(t *testing.T, want string) {
	t.Helper()
	// real-time: a failure bound only; the wait ends on the sink's signal, never on the clock
	deadline := time.After(10 * time.Second)
	for !strings.Contains(s.String(), want) {
		select {
		case <-s.changed:
		case <-deadline:
			t.Fatalf("%q never written; got:\n%s", want, s.String())
		}
	}
}

type dparRun struct {
	res  chan GateResult
	root string
	sink *dparSink
}

// dparStart judges a staged change in a repo declaring toml, on its own
// goroutine, so the test can hold and release the commands.
func dparStart(t *testing.T, toml string, f *dparFake) *dparRun {
	t.Helper()
	root := makeGoRepo(t)
	write(t, root, "aphrollo.toml", toml)
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", "-A")
	d := &dparRun{res: make(chan GateResult, 1), root: root, sink: &dparSink{changed: make(chan struct{}, 1)}}
	t.Cleanup(rootseam.SetStderr(root, d.sink))
	t.Cleanup(func() {
		f.mu.Lock()
		var left []string
		for n := range f.hold {
			left = append(left, n)
		}
		f.mu.Unlock()
		for _, n := range left {
			f.free(n)
		}
	})
	go func() { d.res <- Precommit(root, f.run) }()
	return d
}

func (d *dparRun) wait(t *testing.T) GateResult {
	t.Helper()
	select {
	case r := <-d.res:
		return r
	case <-time.After(20 * time.Second):
		t.Fatal("the gate never finished")
		return GateResult{}
	}
}

// Commands marked parallel run at the same time: both are inside the runner
// before either is let go, which a one-after-another loop can never show.
func TestDeclaredParallel_MarkedCommandsRunAtTheSameTime(t *testing.T) {
	f := newDparFake("a", "b")
	d := dparStart(t, dparToml(2, dparCmd("a", true, 0), dparCmd("b", true, 0)), f)
	if got := f.waitEntered(t, 2); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("running together = %v, want [a b]", got)
	}
	f.free("a")
	f.free("b")
	if res := d.wait(t); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
}

// ratchet: test_removed TestDeclaredParallel_OutputIsInDeclaredOrderWhateverTheFinishOrder: blocks now stream in finish order; restated as TestDeclaredParallel_AFinishedCommandsBlockIsWrittenWholeAtOnce.

// A command's block is written whole the moment it finishes, under a header
// that says how many have finished, so a slow command ahead of it hides
// nothing: b ends before a is let go, and its block is already out.
func TestDeclaredParallel_AFinishedCommandsBlockIsWrittenWholeAtOnce(t *testing.T) {
	f := newDparFake("a", "b")
	d := dparStart(t, dparToml(2, dparCmd("a", true, 0), dparCmd("b", true, 0)), f)
	f.waitEntered(t, 2)
	f.free("b")
	f.waitExited(t, "b")
	d.sink.waitFor(t, "clean (b,")
	if strings.Contains(d.sink.String(), "(a,") {
		t.Fatalf("a's lines are out while a still runs:\n%s", d.sink.String())
	}
	f.free("a")
	if res := d.wait(t); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	nl := "\n"
	out := d.sink.String()
	hb, ha := "[b] (finished 1 of 2)"+nl, "[a] (finished 2 of 2)"+nl
	ib, ia := strings.Index(out, hb), strings.Index(out, ha)
	if ib < 0 || ia < 0 || ib > ia {
		t.Fatalf("headers at %d and %d, want b's then a's, in:\n%s", ib, ia, out)
	}
	if !strings.HasPrefix(out[ib+len(hb):], "gate ") || !strings.Contains(out[ib:ia], "clean (b,") || !strings.Contains(out[ia:], "clean (a,") {
		t.Fatalf("a block is not whole after its header:\n%s", out)
	}
}

// The notice that a command is still running is written as it happens, not
// held behind the blocks ahead of it.
func TestDeclaredParallel_TheStillRunningNoticeIsLive(t *testing.T) {
	defer setStartNoticeAfter(time.Millisecond)()
	f := newDparFake("a", "b")
	d := dparStart(t, dparToml(2, dparCmd("a", true, 0), dparCmd("b", true, 0)), f)
	f.waitEntered(t, 2)
	d.sink.waitFor(t, "running a")
	d.sink.waitFor(t, "running b")
	if strings.Contains(d.sink.String(), "finished") {
		t.Fatalf("a block is out before any command finished:\n%s", d.sink.String())
	}
	f.free("a")
	f.free("b")
	if res := d.wait(t); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
}

// A red does not cancel its sibling: both run to their end and both are in
// the verdict, in declared order, and the barrier after the group never runs.
func TestDeclaredParallel_EveryRedIsListedAndNothingAfterTheGroupRuns(t *testing.T) {
	f := newDparFake("a", "b")
	f.fail["a"], f.fail["b"] = true, true
	f.weights["c"] = 1
	d := dparStart(t, dparToml(2, dparCmd("a", true, 0), dparCmd("b", true, 0), dparCmd("c", false, 0)), f)
	f.waitEntered(t, 2)
	f.free("b")
	f.waitExited(t, "b")
	f.free("a")
	res := d.wait(t)
	ia, ib := strings.Index(res.Message, "command: a"), strings.Index(res.Message, "command: b")
	if !res.Blocked || ia < 0 || ib < 0 || ia > ib || !strings.Contains(res.Message, "err b") {
		t.Fatalf("verdict does not list both reds in declared order: %+v", res)
	}
	f.mu.Lock()
	_, ran := f.runningAtStart["c"]
	f.mu.Unlock()
	if ran {
		t.Fatal("a command after a red group ran")
	}
}

// A sibling already running when another goes red is let finish: its run
// ends, not abandoned.
func TestDeclaredParallel_ARedLeavesTheSiblingToFinish(t *testing.T) {
	f := newDparFake("a", "b")
	f.fail["a"] = true
	d := dparStart(t, dparToml(2, dparCmd("a", true, 0), dparCmd("b", true, 0)), f)
	f.waitEntered(t, 2)
	f.free("a")
	f.waitExited(t, "a")
	f.free("b")
	res := d.wait(t)
	if !res.Blocked || !strings.Contains(res.Message, "command: a") {
		t.Fatalf("the red was not reported: %+v", res)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Equal(f.finished, []string{"a", "b"}) {
		t.Fatalf("finished = %v, want [a b]", f.finished)
	}
}

// A command without parallel runs alone: everything before it has finished,
// and nothing after it starts until it has.
func TestDeclaredParallel_ABarrierWaitsForTheGroupBeforeAndBlocksTheOneAfter(t *testing.T) {
	f := newDparFake("a", "b")
	for _, n := range []string{"gen", "lint", "c"} {
		f.weights[n] = 1
	}
	d := dparStart(t, dparToml(4,
		dparCmd("gen", false, 0), dparCmd("a", true, 0), dparCmd("b", true, 0),
		dparCmd("lint", false, 0), dparCmd("c", true, 0)), f)
	if got := f.waitEntered(t, 3); !slices.Equal(got, []string{"a", "b", "gen"}) {
		t.Fatalf("began %v, want gen then the group a b", got)
	}
	f.free("a")
	f.free("b")
	if res := d.wait(t); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	mustFinish := map[string][]string{
		"gen":  nil,
		"a":    {"gen"},
		"b":    {"gen"},
		"lint": {"gen", "a", "b"},
		"c":    {"gen", "a", "b", "lint"},
	}
	for name, want := range mustFinish {
		for _, w := range want {
			if !slices.Contains(f.finishedAtStart[name], w) {
				t.Errorf("%s began with only %v finished, want %s among them", name, f.finishedAtStart[name], w)
			}
		}
	}
	for _, n := range []string{"gen", "lint", "a", "b", "c"} {
		if len(f.runningAtStart[n]) > 0 && n != "b" && n != "a" {
			t.Errorf("%s began while %v was running", n, f.runningAtStart[n])
		}
	}
}

// Running weights never pass the budget, and what fits runs together.
func TestDeclaredParallel_TheBudgetIsNeverExceeded(t *testing.T) {
	cases := []struct {
		name       string
		budget     int
		weights    []int
		batches    [][]string
		peakWeight int
	}{
		{"unit weights two at a time", 2, []int{1, 1, 1, 1}, [][]string{{"a", "b"}, {"c", "d"}}, 2},
		{"a heavy one holds its place in line", 3, []int{2, 2, 1}, [][]string{{"a"}, {"b", "c"}}, 3},
		{"a weight above the budget runs alone", 2, []int{5, 1}, [][]string{{"a"}, {"b"}}, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			names := []string{"a", "b", "c", "d"}[:len(c.weights)]
			f := newDparFake(names...)
			var cmds []string
			for i, n := range names {
				f.weights[n] = c.weights[i]
				cmds = append(cmds, dparCmd(n, true, c.weights[i]))
			}
			d := dparStart(t, dparToml(c.budget, cmds...), f)
			for _, batch := range c.batches {
				if got := f.waitEntered(t, len(batch)); !slices.Equal(got, batch) {
					t.Fatalf("began %v, want %v", got, batch)
				}
				for _, n := range batch {
					f.free(n)
				}
			}
			if res := d.wait(t); res.Blocked {
				t.Fatalf("unexpected block: %s", res.Message)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.peakWeight != c.peakWeight {
				t.Fatalf("peak running weight = %d, want %d", f.peakWeight, c.peakWeight)
			}
		})
	}
}

// The admission rule alone: a command starts while it fits, or when nothing
// else runs.
func TestParallelAdmits_FitsTheBudgetOrRunsAlone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		used, running, weight, budget int
		want                          bool
	}{
		{0, 0, 1, 2, true},
		{1, 1, 1, 2, true},
		{2, 1, 1, 2, false},
		{0, 0, 5, 2, true},
		{2, 1, 5, 2, false},
		{3, 2, 2, 5, true},
		{3, 2, 3, 5, false},
	}
	for _, c := range cases {
		if got := parallelAdmits(c.used, c.running, c.weight, c.budget); got != c.want {
			t.Errorf("parallelAdmits(used %d, running %d, weight %d, budget %d) = %v, want %v",
				c.used, c.running, c.weight, c.budget, got, c.want)
		}
	}
}

// With no parallel key a declaration is the one-after-another loop it always
// was: one command at a time, in order.
func TestDeclaredParallel_NoKeysRunsOneAtATimeInOrder(t *testing.T) {
	f := newDparFake("a", "b")
	d := dparStart(t, dparToml(0, dparCmd("a", false, 0), dparCmd("b", false, 0)), f)
	if got := f.waitEntered(t, 1); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("began %v, want only a", got)
	}
	f.free("a")
	f.waitEntered(t, 1)
	f.free("b")
	if res := d.wait(t); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.peakRun != 1 {
		t.Fatalf("peak concurrent = %d, want 1", f.peakRun)
	}
}

// The speed event of a parallel block carries the wall time of the block and
// the sum of its parts, so the report can show the saving.
func TestDeclaredParallel_TheBlockRecordsItsWallTimeAndTheSumOfItsParts(t *testing.T) {
	f := newDparFake("a", "b")
	f.dur["a"], f.dur["b"] = 60*time.Second, 90*time.Second
	d := dparStart(t, dparToml(2, dparCmd("a", true, 0), dparCmd("b", true, 0)), f)
	f.waitEntered(t, 2)
	f.free("a")
	f.free("b")
	if res := d.wait(t); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	var found int
	for _, e := range ReadEvents(d.root) {
		if e.Verdict != "declared-parallel" {
			continue
		}
		found++
		if e.Detail["sum_secs"] != "150" || e.Detail["commands"] != "2" {
			t.Errorf("detail = %v, want sum_secs 150 and commands 2", e.Detail)
		}
		if _, err := strconv.ParseFloat(e.Detail["wall_secs"], 64); err != nil {
			t.Errorf("wall_secs = %q, want a number", e.Detail["wall_secs"])
		}
	}
	if found != 1 {
		t.Fatalf("declared-parallel events = %d, want 1", found)
	}
}

// parallel and weight are keys of the inline table; anything else about them
// is refused, not guessed.
func TestDeclaredParallel_ParallelAndWeightAreKeysOfTheInlineTable(t *testing.T) {
	t.Parallel()
	cmds, err := parseDeclaredCommands(`[{ argv = ["a"], parallel = true, weight = 3 }, { argv = ["b"], parallel = false }, ["c"]]`)
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if !cmds[0].Parallel || cmds[0].Weight != 3 || cmds[1].Parallel || cmds[2].Parallel {
		t.Fatalf("read %+v", cmds)
	}
	for _, bad := range []string{
		`[{ argv = ["a"], parallel = "yes" }]`,
		`[{ argv = ["a"], parallel = true, weight = -1 }]`,
	} {
		if _, err := parseDeclaredCommands(bad); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}

// parallel-budget is a key of the same table; an unset one is the box's job
// count, and one that is not a positive whole number is refused.
func TestDeclaredParallel_BudgetComesFromTheTableOrTheBox(t *testing.T) {
	t.Parallel()
	cases := []struct {
		toml    string
		want    int
		wantErr bool
	}{
		{"[aphrollo.precommit]\nparallel-budget = 4\n\".\" = [[\"a\"]]\n", 4, false},
		{"[aphrollo.precommit]\n\".\" = [[\"a\"]]\n", 7, false},
		{"[aphrollo.precommit]\nparallel-budget = 0\n", 0, true},
		{"[aphrollo.precommit]\nparallel-budget = \"x\"\n", 0, true},
	}
	for _, c := range cases {
		repo := t.TempDir()
		write(t, repo, "aphrollo.toml", c.toml)
		got, err := declaredParallelBudget(repo, 7)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%q: budget %d err %v, want %d err=%v", c.toml, got, err, c.want, c.wantErr)
		}
	}
}

// With no parallel-budget the repo has not priced its commands, so the box
// does: one at a time per 8 threads and 8 GB free, as for a race run. A group
// of four on a 16-thread box with 16 GB free goes two at a time, not four.
func TestDeclaredParallel_TheDefaultBudgetIsWhatTheBoxCarries(t *testing.T) {
	defer lock.SetRaceMachineForTest(16, 16*1024)()
	f := newDparFake("a", "b", "c", "d")
	d := dparStart(t, dparToml(0, dparCmd("a", true, 0), dparCmd("b", true, 0), dparCmd("c", true, 0), dparCmd("d", true, 0)), f)
	for _, batch := range [][]string{{"a", "b"}, {"c", "d"}} {
		if got := f.waitEntered(t, 2); !slices.Equal(got, batch) {
			t.Fatalf("began %v, want %v", got, batch)
		}
		for _, n := range batch {
			f.free(n)
		}
	}
	if res := d.wait(t); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.peakRun != 2 {
		t.Fatalf("peak concurrent = %d, want 2", f.peakRun)
	}
}
