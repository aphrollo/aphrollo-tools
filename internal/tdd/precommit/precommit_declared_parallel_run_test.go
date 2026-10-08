package precommit

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/rootseam"
)

// A command that times out is killed as ever, and does not cancel its
// siblings: the other runs to its end and the refusal names the timeout.
func TestDeclaredParallel_ATimeoutInAGroupStillLetsTheOthersFinish(t *testing.T) {
	f := newDparFake("a", "b")
	f.timeout["a"] = true
	d := dparStart(t, dparToml(2, dparCmd("a", true, 0), dparCmd("b", true, 0)), f)
	f.waitEntered(t, 2)
	f.free("a")
	f.waitExited(t, "a")
	f.free("b")
	res := d.wait(t)
	if !res.Blocked {
		t.Fatalf("a timed-out command did not refuse: %+v", res)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Equal(f.finished, []string{"a", "b"}) {
		t.Fatalf("finished = %v, want [a b]", f.finished)
	}
}

// A barrier is judged where it stands: the first red among barriers refuses
// the commit and nothing after it runs.
func TestDeclaredParallel_ABarrierStopsAtTheFirstRed(t *testing.T) {
	f := newDparFake()
	f.fail["a"] = true
	f.weights["b"] = 1
	d := dparStart(t, dparToml(2, dparCmd("a", false, 0), dparCmd("b", false, 0)), f)
	res := d.wait(t)
	if !res.Blocked || !strings.Contains(res.Message, "command: a") {
		t.Fatalf("the red barrier did not refuse: %+v", res)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ran := f.runningAtStart["b"]; ran {
		t.Fatal("a command after a red barrier ran")
	}
}

// The block's wall time is what the clock says the block took, to the second.
func TestDeclaredParallel_WallSecsIsWhatTheClockSaysTheBlockTook(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ticks := 0
	defer setDeclaredClock(func() time.Time {
		ticks++
		return t0.Add(time.Duration(ticks-1) * 90 * time.Second)
	})()
	f := newDparFake("a", "b")
	f.dur["a"], f.dur["b"] = 60*time.Second, 90*time.Second
	d := dparStart(t, dparToml(2, dparCmd("a", true, 0), dparCmd("b", true, 0)), f)
	f.waitEntered(t, 2)
	f.free("a")
	f.free("b")
	d.wait(t)
	for _, e := range ReadEvents(d.root) {
		if e.Verdict == "declared-parallel" {
			if e.Detail["wall_secs"] != "90" || e.Detail["sum_secs"] != "150" {
				t.Fatalf("detail = %v, want wall_secs 90 and sum_secs 150", e.Detail)
			}
			return
		}
	}
	t.Fatal("no declared-parallel event")
}

// A command judged against HEAD runs twice when it fails; the block's sum is
// both runs, not the last one.
func TestDeclaredParallel_SumSecsAddsTheRunAndTheRunAtHead(t *testing.T) {
	f := newDparFake("b")
	f.seq["a"] = []SuiteResult{
		{Passed: false, Output: "x.go:1: finding\n", Duration: 10 * time.Second},
		{Passed: false, Output: "x.go:1: finding\n", Duration: 20 * time.Second},
	}
	f.dur["b"] = 5 * time.Second
	lines := `{ argv = ["a"], parallel = true, baseline = "lines" }`
	d := dparStart(t, dparToml(2, lines, dparCmd("b", true, 0)), f)
	f.waitEntered(t, 2)
	f.free("b")
	if res := d.wait(t); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	for _, e := range ReadEvents(d.root) {
		if e.Verdict == "declared-parallel" {
			if e.Detail["sum_secs"] != "35" {
				t.Fatalf("sum_secs = %q, want 35 (10 + 20 + 5)", e.Detail["sum_secs"])
			}
			return
		}
	}
	t.Fatal("no declared-parallel event")
}

// A command's verdict is stored the moment it finishes, before the slow one
// beside it has: a gate killed during the slow one still leaves the fast
// one's green for the merge to reuse.
func TestDeclaredParallel_AVerdictIsStoredAsEachCommandFinishes(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "web/x.ts", "export const x = 1\n")
	toml := "[aphrollo.precommit]\nparallel-budget = 2\n\".\" = [\n" +
		"  { argv = [\"git\", \"--version\"], inputs = [\"web/**\"], parallel = true },\n" +
		"  { argv = [\"slow\"], parallel = true },\n]\n"
	write(t, root, "aphrollo.toml", toml)
	gitDo(t, root, "add", "-A")
	f := newDparFake("slow")
	sink := &dparSink{changed: make(chan struct{}, 1)}
	t.Cleanup(rootseam.SetStderr(root, sink))
	t.Cleanup(func() { f.free("slow") })
	done := make(chan GateResult, 1)
	go func() { done <- Precommit(root, f.run) }()
	f.waitEntered(t, 1)
	sink.waitFor(t, "[git --version] (finished 1 of 2)")
	cmds, _, err := declaredPrecommit(root, root)
	if err != nil {
		t.Fatal(err)
	}
	k := declaredKeying(root, cmds[0])
	if v, found := declaredVerdictFor(k.key); !found || !v.Green {
		t.Fatalf("no green stored for the finished command while the slow one runs: %+v found=%v", v, found)
	}
	f.free("slow")
	<-done
}

// A reused command in a group takes no budget and says so before the group
// starts: the two others, each weight 1, still run together under a budget of
// 2 although the reused one is declared with weight 2.
func TestDeclaredParallel_AReusedCommandTakesNoBudgetAndSpeaksFirst(t *testing.T) {
	toml := "[aphrollo.precommit]\nparallel-budget = 2\n\".\" = [\n" +
		"  { argv = [\"git\", \"--version\"], inputs = [\"web/**\"], parallel = true, weight = 2 },\n" +
		"  { argv = [\"x\"], parallel = true },\n  { argv = [\"y\"], parallel = true },\n]\n"
	root := dreuseLane(t, toml, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} },
		map[string]string{"docs/a.md": "# a, moved\n"})
	f := newDparFake("x", "y")
	sink := &dparSink{changed: make(chan struct{}, 1)}
	t.Cleanup(rootseam.SetStderr(root, sink))
	t.Cleanup(func() { f.free("x"); f.free("y") })
	done := make(chan GateResult, 1)
	go func() { done <- Mechanical(root, f.run) }()
	if got := f.waitEntered(t, 2); !slices.Equal(got, []string{"x", "y"}) {
		t.Fatalf("began %v, want x and y together", got)
	}
	out := sink.String()
	if i := strings.Index(out, "[reuse] git --version"); i < 0 || strings.Contains(out[:i], "(finished") {
		t.Fatalf("the reuse line is missing or came after a block:\n%s", out)
	}
	f.free("x")
	f.free("y")
	if res := <-done; res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
}

// Two baseline commands at once must not both add or remove a worktree on the
// same repo: every git call that does so is made with the lock held.
func TestAtHead_WorktreeAddAndRemoveRunUnderTheLock(t *testing.T) {
	root := makeGoRepo(t)
	var calls, unlocked []string
	defer setHeadGit(func(repoRoot string, args ...string) (string, error) {
		calls = append(calls, args[1])
		if headWorktreeMu.TryLock() {
			headWorktreeMu.Unlock()
			unlocked = append(unlocked, args[1])
		}
		return git(repoRoot, args...)
	})()
	if err := atHead(root, ".", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{"add", "remove"}) || len(unlocked) > 0 {
		t.Fatalf("worktree calls %v, made without the lock: %v", calls, unlocked)
	}
}
