package mutation

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func moduleWithPackages(t *testing.T, names ...string) string {
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

func withToolBudget(t *testing.T, n int) {
	t.Helper()
	prev := mutantsArgvBudgetFn
	mutantsArgvBudgetFn = func(string) int { return n }
	t.Cleanup(func() { mutantsArgvBudgetFn = prev })
}

// TestRunMutantsTool_APackageListPastTheBudgetRunsAsSeveralCommands pins the
// spawn every kill run and cargo clean goes through: a package list past the
// platform's command-line budget runs as commands that each stay within it,
// one log holds their output, and the exit code is 0 only when every one is.
func TestRunMutantsTool_APackageListPastTheBudgetRunsAsSeveralCommands(t *testing.T) {
	dir := moduleWithPackages(t, "a", "b")
	withToolBudget(t, len("go list ./a ./b")-1)
	var log bytes.Buffer

	code, err := runMutantsTool(context.Background(), dir, os.Environ(), []string{"go", "list", "./a", "./b"}, &log)

	if err != nil || code != 0 || log.String() != "example.test/m/a\nexample.test/m/b\n" {
		t.Fatalf("code %d err %v log %q, want both packages listed and exit 0", code, err, log.String())
	}
}

// TestRunMutantsTool_TheFirstFailingCommandEndsTheRun pins that a red batch's
// exit code is the answer and no later batch runs: a kill is a kill however
// many commands the packages needed.
func TestRunMutantsTool_TheFirstFailingCommandEndsTheRun(t *testing.T) {
	dir := moduleWithPackages(t, "b")
	withToolBudget(t, len("go list ./missing ./b")-1)
	var log bytes.Buffer

	code, err := runMutantsTool(context.Background(), dir, os.Environ(), []string{"go", "list", "./missing", "./b"}, &log)

	if err != nil || code == 0 {
		t.Fatalf("code %d err %v, want the failing batch's nonzero code", code, err)
	}
	if strings.Contains(log.String(), "example.test/m/b") {
		t.Fatalf("log %q lists b, want no batch after the failing one", log.String())
	}
}

// TestRunMutantsTool_CargoMutantsIsNeverSplit pins that the measurement
// itself keeps one command: cargo-mutants must see every package at once,
// and it is a launcher whose one run is the whole measurement.
func TestRunMutantsTool_CargoMutantsIsNeverSplit(t *testing.T) {
	if got := mutantsToolBatches([]string{"cargo", "mutants", "--package", "a", "--package", "b"}, 10); len(got) != 1 {
		t.Fatalf("cargo mutants split into %d commands, want 1", len(got))
	}
	if got := mutantsToolBatches([]string{"cargo", "clean", "-p", "a", "-p", "b"}, 10); len(got) != 2 {
		t.Fatalf("cargo clean split into %d commands, want one per package at a 10-char budget", len(got))
	}
}
