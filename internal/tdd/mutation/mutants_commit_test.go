package mutation

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The commit stage judges what the staged change adds: it mutates only the
// lines the commit adds or changes, refuses one no test notices, and lets a
// box that cannot measure it through with a line saying so, so a slow box
// never holds a commit up.

const commitBaseSource = `package gate

func Kind(n int) string {
	return "small"
}

func Label(s string) string {
	return "#" + s
}
`

// commitStage is a committed Go repository that declares mutants-at-commit
// and has the change of commitGateSource staged: two mutants on line 4, and
// none on lines the commit leaves alone.
func commitStage(t *testing.T, config string) (cfgDir, root string) {
	t.Helper()
	cfgDir = t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	t.Cleanup(setMutantsJobsForTest(2, "pinned"))
	t.Cleanup(SetCommitHeadroomForTest(func(string, time.Duration) string { return "" }))
	root = makeGoRepo(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = true\n"+config)
	write(t, root, "gate/gate.go", commitBaseSource)
	write(t, root, "gate/gate_test.go", "package gate\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "gate/gate.go", commitGateSource)
	gitDo(t, root, "add", "gate/gate.go")
	return cfgDir, root
}

// stubBlockingGo makes every `go test` wait for its context to end, bounded so
// a broken cancel cannot hang the suite.
func stubBlockingGo(t *testing.T) {
	t.Helper()
	prev := resolveExecFn
	resolveExecFn = func(ctx context.Context, _ string, _ []string, _ []string, _ io.Writer) (int, error) {
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(30 * time.Second):
			return 0, nil
		}
	}
	t.Cleanup(func() { resolveExecFn = prev })
}

func killsUnderTheMutant(c goCall) (int, string) {
	if c.Overlay {
		return 1, failedGate
	}
	return 0, "ok\tgate\n"
}

func TestMutantsAtCommitStage_ASurvivorRefusesTheCommitAndIsNamed(t *testing.T) {
	cfgDir, root := commitStage(t, "")
	s := scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })

	res := mutantsAtCommitStage("precommit", root)

	if !res.Blocked {
		t.Fatalf("the stage let a commit through with survivors: %q", res.Message)
	}
	for _, want := range []string{"gate/gate.go:4:7: CONDITIONALS_BOUNDARY", "gate/gate.go:4:7: CONDITIONALS_NEGATION", "REJECTED"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message lacks %q:\n%s", want, res.Message)
		}
	}
	if strings.Contains(res.Message, "gate/gate.go:11") {
		t.Errorf("message names a mutant on a line the commit does not add:\n%s", res.Message)
	}
	if s.count() != 2 {
		t.Errorf("go test ran %d times, want one whole-package run for each of the two mutants", s.count())
	}
	if log := gateLogText(t, cfgDir); !strings.Contains(log, "mutants-refused:tested=2,caught=0,unviable=0,missed=2") {
		t.Errorf("gate.log = %q, want a mutants-refused line with the counts", log)
	}
}

func TestSlowestMutant_IsTheLongestRun(t *testing.T) {
	t.Parallel()
	if got := slowestMutant(nil); got != 0 {
		t.Errorf("no runs = %s, want 0", got)
	}
	if got := slowestMutant([]commitRun{{Took: 3 * time.Second}}); got != 3*time.Second {
		t.Errorf("one run = %s, want 3s", got)
	}
	runs := []commitRun{{Took: time.Second}, {Took: 5 * time.Second}, {Took: 2 * time.Second}}
	if got := slowestMutant(runs); got != 5*time.Second {
		t.Errorf("three runs = %s, want 5s", got)
	}
}

func TestMutantsAtCommitStage_CaughtMutantsPass(t *testing.T) {
	cfgDir, root := commitStage(t, "")
	scriptGo(t, killsUnderTheMutant)

	var res GateResult
	stderr := captureStderr(t, func() { res = mutantsAtCommitStage("precommit", root) })
	if !strings.Contains(stderr, "2 tested, 2 caught") || !strings.Contains(stderr, "slowest mutant") {
		t.Errorf("stderr = %q, want the counts and the slowest mutant", stderr)
	}

	if res.Blocked {
		t.Fatalf("the stage refused a commit whose mutants were all caught:\n%s", res.Message)
	}
	if log := gateLogText(t, cfgDir); !strings.Contains(log, "mutants-passed:tested=2,caught=2") {
		t.Errorf("gate.log = %q, want a mutants-passed line with the counts", log)
	}
}

// A survivor somebody signed off on is admitted exactly as it is at merge.
func TestMutantsAtCommitStage_AnAcceptedSurvivorPasses(t *testing.T) {
	_, root := commitStage(t, "mutation-accept = [\n"+
		"  \"gate/gate.go:4:7 CONDITIONALS_BOUNDARY # kind=equivalent: signed off\",\n"+
		"  \"gate/gate.go:4:7 CONDITIONALS_NEGATION # kind=equivalent: signed off\",\n]\n")
	scriptGo(t, func(goCall) (int, string) { return 0, "ok\tgate\n" })
	if res := mutantsAtCommitStage("precommit", root); res.Blocked {
		t.Errorf("an accepted survivor refused the commit:\n%s", res.Message)
	}
}

func TestMutantsAtCommitStage_UndeclaredIsInert(t *testing.T) {
	cfgDir, root := commitStage(t, "")
	write(t, root, "aphrollo.toml", "[aphrollo]\nundercover = true\n")
	s := scriptGo(t, func(goCall) (int, string) { return 0, "" })

	res := mutantsAtCommitStage("precommit", root)

	if res.Blocked || res.Message != "" || s.count() != 0 {
		t.Errorf("an undeclared repo got %+v after %d runs, want nothing", res, s.count())
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "gate-state", "gate.log")); err == nil {
		if log := gateLogText(t, cfgDir); strings.Contains(log, "mutants") {
			t.Errorf("an undeclared repo logged about mutants:\n%s", log)
		}
	}
}

// A commit that adds no line a mutant sits on measures nothing.
func TestMutantsAtCommitStage_NothingToMeasureRunsNothing(t *testing.T) {
	cfgDir, root := commitStage(t, "")
	gitDo(t, root, "reset", "-q", "gate/gate.go")
	write(t, root, "gate/gate.go", commitBaseSource)
	write(t, root, "gate/gate_test.go", "package gate\n\nimport \"testing\"\n\nfunc TestKind_A(t *testing.T) {}\n")
	gitDo(t, root, "add", ".")
	s := scriptGo(t, func(goCall) (int, string) { return 0, "" })

	res := mutantsAtCommitStage("precommit", root)

	if res.Blocked || s.count() != 0 {
		t.Errorf("stage = %+v after %d runs, want a pass that ran nothing", res, s.count())
	}
	if log := gateLogText(t, cfgDir); !strings.Contains(log, "mutants-skipped:nothing-to-measure") {
		t.Errorf("gate.log = %q, want the skip counted", log)
	}
}

// A file with edits that are not staged is measured as the commit will hold
// it or not at all; the working file is what the copy runs, so it is skipped.
func TestMutantsAtCommitStage_AFileWithUnstagedEditsIsNotMeasured(t *testing.T) {
	_, root := commitStage(t, "")
	write(t, root, "gate/gate.go", commitGateSource+"\n// unstaged\n")
	s := scriptGo(t, func(goCall) (int, string) { return 0, "" })

	var res GateResult
	stderr := captureStderr(t, func() { res = mutantsAtCommitStage("precommit", root) })

	if res.Blocked || s.count() != 0 {
		t.Errorf("stage = %+v after %d runs, want a pass that ran nothing", res, s.count())
	}
	if !strings.Contains(stderr, "gate/gate.go") || !strings.Contains(stderr, "unstaged") {
		t.Errorf("stderr = %q, want the unmeasured file named with its reason", stderr)
	}
}

func TestMutantsAtCommitStage_ANonGoRepoStandsDown(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	root := makeCargoRepoForCommit(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = true\n")
	res := mutantsAtCommitStage("precommit", root)
	if res.Blocked {
		t.Errorf("a Cargo repo was refused: %s", res.Message)
	}
	if log := gateLogText(t, cfgDir); !strings.Contains(log, "mutants-skipped:not-go") {
		t.Errorf("gate.log = %q, want the stand-down counted", log)
	}
}

func makeCargoRepoForCommit(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	if err := os.Remove(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\n")
	return root
}

// A box with no memory to spare measures nothing and refuses nothing.
func TestMutantsAtCommitStage_NoHeadroomIsNotMeasured(t *testing.T) {
	cfgDir, root := commitStage(t, "")
	t.Cleanup(SetCommitHeadroomForTest(func(string, time.Duration) string { return "memory headroom: 0.5 GB available" }))
	s := scriptGo(t, func(goCall) (int, string) { return 0, "" })

	var res GateResult
	stderr := captureStderr(t, func() { res = mutantsAtCommitStage("precommit", root) })

	if res.Blocked || s.count() != 0 {
		t.Errorf("stage = %+v after %d runs, want an unblocked stage that started nothing", res, s.count())
	}
	if !strings.Contains(stderr, "NOT MEASURED") || !strings.Contains(stderr, "memory headroom") {
		t.Errorf("stderr = %q, want NOT MEASURED with the reason", stderr)
	}
	if log := gateLogText(t, cfgDir); !strings.Contains(log, "mutants-unmeasured:commit-headroom") {
		t.Errorf("gate.log = %q, want the gap counted", log)
	}
}

// The box-wide mutation lock is held by a run that can take hours: a commit
// waits for it only as long as its own budget, then measures nothing.
func TestMutantsAtCommitStage_ABusyBoxIsNotMeasuredAfterTheBudget(t *testing.T) {
	cfgDir, root := commitStage(t, "mutants-commit-budget = 1\n")
	release := acquireMutantsRunLock("a long run", root)
	defer release()
	s := scriptGo(t, func(goCall) (int, string) { return 0, "" })

	var res GateResult
	start := time.Now()
	stderr := captureStderr(t, func() { res = mutantsAtCommitStage("precommit", root) })

	if res.Blocked || s.count() != 0 {
		t.Errorf("stage = %+v after %d runs, want an unblocked stage that ran nothing", res, s.count())
	}
	if waited := time.Since(start); waited > 30*time.Second {
		t.Errorf("the commit waited %s on the lock, want about its 1s budget", waited)
	}
	if !strings.Contains(stderr, "NOT MEASURED") {
		t.Errorf("stderr = %q, want NOT MEASURED", stderr)
	}
	if log := gateLogText(t, cfgDir); !strings.Contains(log, "mutants-unmeasured:commit-lock") {
		t.Errorf("gate.log = %q, want the gap counted", log)
	}
}

// Mutants the wall-clock does not reach are reported NOT MEASURED, and a
// stage that measured none of them refuses nothing.
func TestMutantsAtCommitStage_APastTheBudgetRunIsNotMeasured(t *testing.T) {
	cfgDir, root := commitStage(t, "mutants-commit-budget = 1\n")
	stubBlockingGo(t)

	var res GateResult
	stderr := captureStderr(t, func() { res = mutantsAtCommitStage("precommit", root) })

	if res.Blocked {
		t.Fatalf("a run cut by the budget refused the commit:\n%s", res.Message)
	}
	if !strings.Contains(stderr, "NOT MEASURED") {
		t.Errorf("stderr = %q, want NOT MEASURED", stderr)
	}
	if log := gateLogText(t, cfgDir); !strings.Contains(log, "mutants-unmeasured:commit-budget") {
		t.Errorf("gate.log = %q, want the gap counted", log)
	}
}
