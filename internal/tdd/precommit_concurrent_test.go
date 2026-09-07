package tdd

// Issue #535: fail-first and mechanical run concurrently.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestPrecommit_ConcurrentPair_BothStagesRunEvenWhenFailFirstRejects pins a
// deliberate choice from issue #535: making fail-first and mechanical run
// concurrently must not collapse into a short-circuit that saves nothing on
// the failing path. A commit whose staged test passes without the staged
// impl (a fail-first violation) must still see the MECHANICAL run start —
// the sequential version never got there once fail-first blocked.
func TestPrecommit_ConcurrentPair_BothStagesRunEvenWhenFailFirstRejects(t *testing.T) {
	withLinter(t, false)
	root := makeGoRepo(t)
	// A test that asserts nothing about new code — it passes against HEAD,
	// so fail-first must find a violation.
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) { _ = 1 }\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []loggedRun
	res := Precommit(root, recordAllRuns(&seen, func(string) bool { return true }))
	if !res.Blocked || !strings.Contains(res.Message, "fail-first") {
		t.Fatalf("expected a fail-first block, got %+v", res)
	}
	var ranMechanical bool
	for _, r := range seen {
		if r.dir == root {
			ranMechanical = true
		}
	}
	if !ranMechanical {
		t.Fatalf("mechanical must still run concurrently even though fail-first rejects, runs=%+v", seen)
	}
}

// TestPrecommit_ConcurrentPair_CapsGoTestParallelism pins the mitigation for
// hazard #1's resource-contention half (cold review, fix round 1): when the
// pair actually launches, BOTH `go test` invocations must carry -p/-parallel
// capped to concurrentGoTestJobs(), so two CPU-heavy processes running at
// once do not each try to claim every core. The uncapped case (fail-first
// never fires — a source-only commit) already has its own regression guard:
// TestPrecommit_Mechanical_ScopedToStagedGoPackages asserts the mechanical
// runner's argv verbatim with no -p/-parallel present at all.
func TestPrecommit_ConcurrentPair_CapsGoTestParallelism(t *testing.T) {
	withLinter(t, false)
	root := makeGoRepo(t)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	n := concurrentGoTestJobs()
	pFlag := fmt.Sprintf("-p=%d", n)
	parallelFlag := fmt.Sprintf("-parallel=%d", n)

	var seen []loggedRun
	Precommit(root, recordAllRuns(&seen, func(string) bool { return true }))

	var sawFailFirst, sawMechanical bool
	for _, r := range seen {
		if !slices.Contains(r.runner.Args, "test") {
			continue
		}
		if !slices.Contains(r.runner.Args, pFlag) || !slices.Contains(r.runner.Args, parallelFlag) {
			t.Fatalf("go test invocation in %s missing the parallelism cap, args=%v, want %s and %s present",
				r.dir, r.runner.Args, pFlag, parallelFlag)
		}
		if r.dir == root {
			sawMechanical = true
		} else {
			sawFailFirst = true
		}
	}
	if !sawFailFirst || !sawMechanical {
		t.Fatalf("expected both a fail-first and a mechanical go test run, got %+v", seen)
	}
}

// TestGoTestJobsFor_matches_closed_form pins the formula itself against
// literals, never against runtime.NumCPU() or the production function that
// calls it: an expectation computed by calling concurrentGoTestJobs() (the
// wiring test above does this deliberately, to prove the cap is THREADED
// through correctly) would still pass if the formula changed underneath it
// — e.g. NumCPU/3 instead of NumCPU/2, or a dropped floor-at-1 — because the
// test would recompute the same, now-wrong, N and watch it flow through.
// These five cases are the closed form: floor(cpuCount/2), floored at 1.
func TestGoTestJobsFor_matches_closed_form(t *testing.T) {
	cases := []struct {
		cpuCount int
		want     int
	}{
		{24, 12},
		{4, 2},
		{2, 1},
		{1, 1},
		{0, 1},
	}
	for _, c := range cases {
		if got := goTestJobsFor(c.cpuCount); got != c.want {
			t.Errorf("goTestJobsFor(%d) = %d, want %d", c.cpuCount, got, c.want)
		}
	}
}

// TestPrecommit_ConcurrentPair_RejectionsAreDistinguishable pins the other
// half of issue #535: running the two stages concurrently must not blur
// which one rejected. A fail-first violation and a mechanical failure must
// each name themselves in GateResult.Message, must NOT carry the other
// stage's name, and must leave their own — and only their own — verdict
// token in gate.log.
func TestPrecommit_ConcurrentPair_RejectionsAreDistinguishable(t *testing.T) {
	t.Run("fail-first violation", func(t *testing.T) {
		cfg := t.TempDir()
		t.Setenv("CLAUDE_CONFIG_DIR", cfg)
		withLinter(t, false)
		root := makeGoRepo(t)
		write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) { _ = 1 }\n")
		write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
		gitDo(t, root, "add", ".")

		res := Precommit(root, RunSuite(precommitTestTimeout))
		if !res.Blocked || !strings.Contains(res.Message, "fail-first") {
			t.Fatalf("expected a fail-first block naming itself, got %+v", res)
		}
		if strings.Contains(res.Message, "mechanical") {
			t.Fatalf("a fail-first rejection must not read as a mechanical one: %s", res.Message)
		}
		data, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
		if err != nil {
			t.Fatalf("gate.log not written: %v", err)
		}
		if !strings.Contains(string(data), "violated") {
			t.Fatalf("gate.log must carry fail-first's own token (violated), got:\n%s", data)
		}
		if strings.Contains(string(data), "mechanical-blocked") {
			t.Fatalf("gate.log must not carry a mechanical rejection token here, got:\n%s", data)
		}
	})

	t.Run("mechanical failure", func(t *testing.T) {
		cfg := t.TempDir()
		t.Setenv("CLAUDE_CONFIG_DIR", cfg)
		withLinter(t, false)
		root := makeGoRepo(t)
		// A committed test that passes, then a source-only change that
		// breaks it: the code still compiles, so fail-first's no-staged-test
		// guard skips it (no worktree run) and only the SUITE rejects.
		write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
		write(t, root, "widget_test.go",
			"package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"boom\")\n\t}\n}\n")
		gitDo(t, root, "add", ".")
		gitDo(t, root, "commit", "-qm", "widget")
		write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 2 }\n")
		gitDo(t, root, "add", ".")

		res := Precommit(root, RunSuite(precommitTestTimeout))
		if !res.Blocked || !strings.Contains(res.Message, "mechanical") {
			t.Fatalf("expected a mechanical block naming itself, got %+v", res)
		}
		if strings.Contains(res.Message, "fail-first") {
			t.Fatalf("a mechanical rejection must not read as a fail-first one: %s", res.Message)
		}
		data, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
		if err != nil {
			t.Fatalf("gate.log not written: %v", err)
		}
		if !strings.Contains(string(data), "mechanical-blocked") {
			t.Fatalf("gate.log must carry mechanical's own token (mechanical-blocked), got:\n%s", data)
		}
		if strings.Contains(string(data), "violated") {
			t.Fatalf("gate.log must not carry a fail-first violation token here, got:\n%s", data)
		}
	})
}

// TestPrecommit_ConcurrentPair_ActuallyOverlapsInWallClock is the closest
// this package can come to proving hazard #1 (the build-slot pool) does not
// turn the pair into a hidden serialization: it proves the two suite RUNS
// are genuinely in flight at the same time, which is the property any
// accidental mutex or lock reintroduced around them would break first. It
// does NOT exercise runCargoLocked's own machine-wide slot pool — gateRoot's
// non-cargo branch (the only caller of runFailFirstAndMechanicalConcurrently)
// never reaches it, by construction: a plain `go test` at precommit carries
// no -race, so both runs bypass the pool entirely (see
// runFailFirstAndMechanicalConcurrently's doc comment). Proving the pool
// itself stays deadlock-free under this pair would need a cargo fixture,
// which this change deliberately does not route through the pair (see
// gateRootCargo's own comment) — so that half of hazard #1 is reported, not
// tested here.
//
// The two channels below (entered, release) are the actual synchronization
// the assertion depends on; every time.After is a backstop against a hang in
// a broken implementation, never the mechanism under test.
func TestPrecommit_ConcurrentPair_ActuallyOverlapsInWallClock(t *testing.T) {
	withLinter(t, false)
	root := makeGoRepo(t)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	entered := make(chan string, 2)
	release := make(chan struct{})
	run := func(r Runner, dir string) SuiteResult {
		// goQualityStage's `go vet` runs at dir==root BEFORE the pair even
		// starts (it is not gated by withLinter(t, false), which only turns
		// the LINTER off) — it must pass straight through, or synchronizing
		// on it here wedges the whole gate before it ever reaches fail-first
		// or the suite.
		if r.Cmd != "go" || len(r.Args) == 0 || r.Args[0] != "test" {
			return SuiteResult{Passed: true}
		}
		who := "mechanical"
		if dir != root {
			who = "fail-first"
		}
		entered <- who
		<-release
		if dir != root {
			// The applied test cannot compile without the staged impl in a
			// worktree that never received it — a conclusive, non-violating
			// (red-proven) fail-first verdict.
			return SuiteResult{Passed: false, Output: "undefined: Widget"}
		}
		return SuiteResult{Passed: true}
	}

	done := make(chan GateResult, 1)
	go func() { done <- Precommit(root, run) }()

	var first, second string
	select {
	case first = <-entered:
	case <-time.After(precommitTestTimeout): // real-time: backstop against a hang; `entered` is the real synchronization
		t.Fatal("neither stage entered its suite run in time")
	}
	select {
	case second = <-entered:
	case <-time.After(precommitTestTimeout): // real-time: backstop against a hang; `entered` is the real synchronization
		t.Fatalf("only %q entered its suite run before the other blocked — the pair is serialized, not concurrent", first)
	}
	if first == second {
		t.Fatalf("both entries came from the same stage (%q); want one fail-first and one mechanical", first)
	}
	close(release)

	select {
	case <-done:
	case <-time.After(precommitTestTimeout): // real-time: backstop against a hang; `done` is the real synchronization
		t.Fatal("Precommit did not return after both stages were released")
	}
}
