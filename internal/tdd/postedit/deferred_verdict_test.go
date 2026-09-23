package postedit

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestWaitDeferredEditJob_FindsAJobRecordedForACrateInsideTheCheckout pins
// the exact symptom issue #571 reports: an edit answers BUILDING (deferred),
// and `gate status --wait` immediately answers "no deferred edit job recorded
// for this checkout". The job WAS recorded — under the project root the edit
// hook derived, which is the nearest marker directory (a Cargo workspace
// member's own crate dir, e.g. .../powertrain-e/crates/sim), while `gate
// status` looks the job up by the git top-level of the checkout it is run
// from. An exact-identity match misses every job recorded for a directory
// BELOW the queried root, so five deferred edits in a row could report
// nothing to wait on.
func TestWaitDeferredEditJob_FindsAJobRecordedForACrateInsideTheCheckout(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir() // not a git repo -- headSHAFor(root) resolves to ""
	crate := filepath.Join(root, "crates", "sim")
	if err := os.MkdirAll(crate, 0o755); err != nil {
		t.Fatal(err)
	}
	saveDeferredJob(DeferredJob{
		Project: crate, Phase: "run", Dir: crate, PID: 99,
		Runner: []string{"cargo", "nextest", "run", "-p", "sim"}, Started: time.Now(),
		HeadSHA: "", FileHash: "hash1", Session: "s1",
	})
	loaded, ok := loadDeferredJob("s1", crate)
	if !ok {
		t.Fatal("setup: expected the job to load back")
	}
	writePhaseResult(loaded.Result, PhaseOutcome{ExitCode: 0, Seconds: 1})

	advisory, ok := WaitDeferredEditJob(root)

	if !ok {
		t.Fatalf("a job recorded for %s must be found from the checkout root %s, not reported as nothing recorded", crate, root)
	}
	if !strings.Contains(advisory, "gate:") {
		t.Errorf("advisory = %q, want the same gate: verdict line a hook harvesting it would print", advisory)
	}
}

// TestPostEdit_AbandonedDeferredJobIsReportedNotSwallowed pins the other half
// of issue #571: a deferred job that outlived the ceiling is killed, its
// record dropped and "deferred-abandoned" written to the gate log — and the
// session is told NOTHING. It sees only the fresh run's BUILDING line, which
// reads as "work in progress" when what actually happened is "the previous
// run died without ever testing your code". A job that cannot finish must
// report an inconclusive verdict, in the one line the hook gets, alongside
// whatever it starts next.
func TestPostEdit_AbandonedDeferredJobIsReportedNotSwallowed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "0")
	root := mkProject(t, "Cargo.toml")
	target := filepath.Join(root, "src", "widget.rs")
	fakePhases(t) // the fresh phase never finishes either

	// A job started well past the deferral ceiling, with no result: exactly
	// what harvestDeferred abandons.
	saveDeferredJob(DeferredJob{
		Project: root, Session: "sess-post", Phase: "run", Dir: root,
		Started: time.Now().Add(-2 * deferredMax()),
		HeadSHA: headSHAFor(root), FileHash: sourceIdentity(root, target),
		Runner: []string{"cargo", "test"},
	})

	got := PostEdit(postPayload("Edit", target), fakeRun(true, "ok"))

	if !strings.Contains(got, DeferredAbandoned) {
		t.Fatalf("advisory = %q, want it to name the %s verdict for the job that died without testing anything", got, DeferredAbandoned)
	}
	if !strings.Contains(got, "BUILDING") {
		t.Errorf("advisory = %q, want the fresh run's own notice too: the abandonment must not hide what is running now", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("advisory = %q, want ONE line -- the hook prints exactly one gate: line per edit", got)
	}
}

// TestPhaseArgv_GoTestCarriesATimeoutAboveTheDeferralCeiling pins the
// measured cause behind "deferred-abandoned 600.4s / 601.8s" in this box's
// own gate log: a deferred `go test` inherits go's DEFAULT 10-minute test
// timeout, which lands on the same 600s the deferral ceiling abandons the job
// at. The two clocks race, and whichever wins the session learns nothing --
// the gate kills the run just as go's own panic dump is being written, or the
// panic dump arrives and reads as a red the code never earned. The gate's
// ceiling must be the only clock that ends a deferred run, so the phase names
// a timeout of its own, above that ceiling.
func TestPhaseArgv_GoTestCarriesATimeoutAboveTheDeferralCeiling(t *testing.T) {
	t.Setenv(deferredMaxEnv, "600")

	argv := phaseArgv(Runner{Cmd: "go", Args: []string{"test", "./internal/tdd"}}, "run")

	value, found := goTimeoutArg(argv)
	if !found {
		t.Fatalf("argv = %v, want an explicit -timeout: go's default 10m collides with the %s deferral ceiling", argv, deferredMax())
	}
	got, err := time.ParseDuration(value)
	if err != nil {
		t.Fatalf("-timeout=%q does not parse as a duration: %v", value, err)
	}
	if got <= deferredMax() {
		t.Errorf("-timeout = %s, want strictly more than the deferral ceiling %s so the ceiling is the only clock that ends the run", got, deferredMax())
	}
}

// TestPhaseArgv_KeepsACallerSuppliedGoTimeout: a runner that already names
// its own -timeout is left exactly as it is. Doubling the flag is a `go test`
// error, and a caller that tuned the value meant it.
func TestPhaseArgv_KeepsACallerSuppliedGoTimeout(t *testing.T) {
	t.Setenv(deferredMaxEnv, "600")

	argv := phaseArgv(Runner{Cmd: "go", Args: []string{"test", "-timeout=5s", "./internal/tdd"}}, "run")

	value, found := goTimeoutArg(argv)
	if !found || value != "5s" {
		t.Fatalf("argv = %v, want the caller's own -timeout=5s kept", argv)
	}
	count := 0
	for _, a := range argv {
		if strings.HasPrefix(a, "-timeout") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("argv = %v, want exactly one -timeout flag, got %d", argv, count)
	}
}

// TestPhaseArgv_LeavesACargoRunAlone: -timeout is a `go test` flag and
// nothing else. A cargo (or nextest) phase must reach the wrapper with the
// argv the detector built, unchanged.
func TestPhaseArgv_LeavesACargoRunAlone(t *testing.T) {
	t.Setenv(deferredMaxEnv, "600")

	argv := phaseArgv(Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "sim"}}, "run")

	if _, found := goTimeoutArg(argv); found {
		t.Fatalf("argv = %v, want no -timeout on a cargo phase", argv)
	}
}

// TestPhaseArgv_BenchEditNeverDoublesNoRun pins issue #711: cargoTargetRunner
// already appends --no-run for a benches/ edit (a bench RUN costs minutes and
// answers a question nobody asked — only "does it compile" is owed), so the
// argv phaseArgv's build phase receives already carries --no-run.
// Unconditionally appending a second one produced
// "error: the argument '--no-run' cannot be used multiple times", which
// nextest reports as outcome=red on every benches/ edit though nothing was
// even compiled.
func TestPhaseArgv_BenchEditNeverDoublesNoRun(t *testing.T) {
	argv := phaseArgv(Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "sim", "--bench", "apply_movement", "--no-run"}}, "build")

	count := 0
	for _, a := range argv {
		if a == "--no-run" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("argv = %v, want exactly one --no-run, got %d", argv, count)
	}
}

// goTimeoutArg reads back the -timeout value from an argv, in both spellings
// `go test` accepts, so a test asserts on the VALUE rather than on one exact
// spelling of the flag.
func goTimeoutArg(argv []string) (string, bool) {
	for i, a := range argv {
		if v, ok := strings.CutPrefix(a, "-timeout="); ok {
			return v, true
		}
		if a == "-timeout" && i+1 < len(argv) {
			return argv[i+1], true
		}
	}
	return "", false
}

// A stale result is labelled with what its run actually found, never with a
// verdict it did not reach: a build that compiled ran no test, a setup that
// failed tested nothing, and nextest's "no tests to run" is an empty pass
// rather than a red — and, under the classification floor, never a green
// either: it tested nothing.
func TestStaleVerdictLabel_SaysWhatTheEarlierRunActuallyFound(t *testing.T) {
	dir := t.TempDir()
	job := func(phase, log string) DeferredJob {
		path := filepath.Join(dir, phase+strconv.Itoa(len(log))+".log")
		if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
			t.Fatal(err)
		}
		return DeferredJob{Phase: phase, Log: path, Runner: []string{"cargo", "test"}}
	}
	cases := []struct {
		name    string
		j       DeferredJob
		out     PhaseOutcome
		want    string
		mustNot string
	}{
		{"setup failed", job("run", "no slot"), PhaseOutcome{ExitCode: 1, SetupFailed: true}, InfraFailed, "red"},
		{"build compiled", job("build", "Finished"), PhaseOutcome{ExitCode: 0}, "build ok, no test ran", "green"},
		{"build failed", job("build", "error: could not compile `a`"), PhaseOutcome{ExitCode: 101}, "red", "build ok"},
		{"nothing to run", job("run", "error: no tests to run"), PhaseOutcome{ExitCode: 4}, "nothing was tested", "red"},
		{"red, no name", job("run", "boom"), PhaseOutcome{ExitCode: 1}, "red", "first failure"},
	}
	for _, c := range cases {
		got := staleVerdictLabel(c.j, c.out)
		if !strings.Contains(got, c.want) || strings.Contains(got, c.mustNot) {
			t.Errorf("%s: label = %q, want %q and never %q", c.name, got, c.want, c.mustNot)
		}
	}
}
