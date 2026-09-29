package lock

import (
	"path/filepath"
	"testing"
)

// TestResolveCargoTargetDir_DefaultsToWorkspaceTarget pins the exported
// wrapper internal/cli's cargo shim calls: with no CARGO_TARGET_DIR and no
// project config overriding it, a plain workspace root resolves to
// <root>/target, identically to what resolveTargetDir itself answers, so a
// direct `cargo build` and a gate's build key the SAME lock.
func TestResolveCargoTargetDir_DefaultsToWorkspaceTarget(t *testing.T) {
	t.Setenv("CARGO_TARGET_DIR", "")
	ws := t.TempDir()

	want := filepath.Join(ws, "target")
	if got := ResolveCargoTargetDir(ws); got != want {
		t.Fatalf("ResolveCargoTargetDir(%q) = %q, want %q", ws, got, want)
	}
}

// TestRunnerTargetDir_UsesRunnerDirWhenSet pins runnerTargetDir's own rule:
// a Runner resolved to run from its OWN directory (r.Dir) writes artifacts
// under that directory's workspace, not under whatever crate root the gate
// happens to key its state on.
func TestRunnerTargetDir_UsesRunnerDirWhenSet(t *testing.T) {
	t.Setenv("CARGO_TARGET_DIR", "")
	runnerDir := t.TempDir()
	stateRoot := t.TempDir()

	want := filepath.Join(runnerDir, "target")
	if got := runnerTargetDir(Runner{Dir: runnerDir}, stateRoot); got != want {
		t.Fatalf("runnerTargetDir with Runner.Dir set = %q, want %q (Runner.Dir's own target, not the state root's)", got, want)
	}
}

// TestRunnerTargetDir_FallsBackToRootWhenRunnerDirEmpty is the other half:
// an unresolved Runner (no Dir) falls back to root, exactly as a plain
// resolveTargetDir(root) would.
func TestRunnerTargetDir_FallsBackToRootWhenRunnerDirEmpty(t *testing.T) {
	t.Setenv("CARGO_TARGET_DIR", "")
	root := t.TempDir()

	want := filepath.Join(root, "target")
	if got := runnerTargetDir(Runner{}, root); got != want {
		t.Fatalf("runnerTargetDir with no Runner.Dir = %q, want %q", got, want)
	}
}

// TestRunnerTargetDir_TheRunnersOwnTargetBindingWins pins that a Runner naming
// its own CARGO_TARGET_DIR is locked under THAT directory, not the process
// environment's: the binding is what the child build will see (Runner.Env
// comes last in its environment), so a lock keyed on any other directory would
// let two builds into one target through.
func TestRunnerTargetDir_TheRunnersOwnTargetBindingWins(t *testing.T) {
	t.Setenv("CARGO_TARGET_DIR", filepath.Join(t.TempDir(), "process-target"))
	root := t.TempDir()
	bound := filepath.Join(t.TempDir(), "bound-target")

	r := Runner{Env: []string{"FOO=1", "CARGO_TARGET_DIR=" + bound}}
	if got := runnerTargetDir(r, root); got != bound {
		t.Fatalf("runnerTargetDir = %q, want the runner's own binding %q", got, bound)
	}
}

// TestRunnerTargetDir_TheLastBindingOfTheRunnerWins pins the order the child
// sees: with two CARGO_TARGET_DIR bindings on one runner the later one is the
// directory it builds into, so it is the one the lock keys on.
func TestRunnerTargetDir_TheLastBindingOfTheRunnerWins(t *testing.T) {
	t.Setenv("CARGO_TARGET_DIR", "")
	first := filepath.Join(t.TempDir(), "first")
	last := filepath.Join(t.TempDir(), "last")
	r := Runner{Env: []string{"CARGO_TARGET_DIR=" + first, "CARGO_TARGET_DIR=" + last}}
	if got := runnerTargetDir(r, t.TempDir()); got != last {
		t.Fatalf("runnerTargetDir = %q, want the later binding %q", got, last)
	}
}

// TestRunnerTargetDir_ARunnerWithOtherBindingsFallsBackToTheProcessValue pins
// the fallback: a runner that binds other variables but not CARGO_TARGET_DIR
// is keyed on the process environment's, as before.
func TestRunnerTargetDir_ARunnerWithOtherBindingsFallsBackToTheProcessValue(t *testing.T) {
	process := filepath.Join(t.TempDir(), "process-target")
	t.Setenv("CARGO_TARGET_DIR", process)
	r := Runner{Env: []string{"RUSTFLAGS=-Dwarnings", "CARGO_TARGET_DIRECTORY=/not/it"}}
	if got := runnerTargetDir(r, t.TempDir()); got != process {
		t.Fatalf("runnerTargetDir = %q, want the process value %q", got, process)
	}
}
