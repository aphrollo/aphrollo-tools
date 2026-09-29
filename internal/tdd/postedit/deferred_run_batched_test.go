package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func goModuleWithPackages(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/m\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n, n+".go"), []byte("package "+n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runPhaseWithBudget(t *testing.T, dir string, argv []string, budget int) (log string, out PhaseOutcome) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	defer func(prev func(string) int) { phaseArgvBudgetFn = prev }(phaseArgvBudgetFn)
	phaseArgvBudgetFn = func(string) int { return budget }
	j := DeferredJob{Project: dir, Phase: "run", Dir: dir, Runner: argv,
		Log: filepath.Join(dir, "p.log"), Result: filepath.Join(dir, "p.result.json")}

	RunPhase(writeJob(t, j))

	out, done := deferredResult(j)
	if !done {
		t.Fatal("the phase wrote no result")
	}
	data, err := os.ReadFile(j.Log)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), out
}

// TestRunPhase_ALineTooLongForThePlatformRunsAsSeveralCommands pins the
// detached phase's spawn: a package list past the budget runs as commands
// that each stay within it, and the one log holds every command's output.
func TestRunPhase_ALineTooLongForThePlatformRunsAsSeveralCommands(t *testing.T) {
	dir := goModuleWithPackages(t, "a", "b")

	log, out := runPhaseWithBudget(t, dir, []string{"go", "list", "./a", "./b"}, len("go list ./a ./b")-1)

	if out.ExitCode != 0 || log != "example.test/m/a\nexample.test/m/b\n" {
		t.Fatalf("exit %d log %q, want both packages listed and a clean exit", out.ExitCode, log)
	}
}

// TestRunPhase_TheFirstFailingCommandEndsThePhase pins that a later batch
// never runs after a red one, and its exit code is the phase's.
func TestRunPhase_TheFirstFailingCommandEndsThePhase(t *testing.T) {
	dir := goModuleWithPackages(t, "b")

	log, out := runPhaseWithBudget(t, dir, []string{"go", "list", "./missing", "./b"}, len("go list ./missing ./b")-1)

	if out.ExitCode == 0 {
		t.Fatalf("exit 0, want the failing first batch's code; log %q", log)
	}
	if strings.Contains(log, "example.test/m/b") {
		t.Fatalf("log %q lists b, want no batch after the failing one", log)
	}
}
