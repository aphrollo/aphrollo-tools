package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPinMechCargoTarget_UsesTheReposOwnDevTarget pins the user's decision:
// ONE target dir per repo. The gate used to build into a private
// <stateDir>/cargo-target/<hash>, which meant every gate run cold-compiled
// what the developer had already built next door — a second copy of a
// hundred-gigabyte tree to prove the same thing twice.
func TestPinMechCargoTarget_UsesTheReposOwnDevTarget(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	os.Unsetenv("CARGO_TARGET_DIR")
	repo := t.TempDir()

	restore := pinMechCargoTarget(Runner{Cmd: "cargo"}, repo)
	got := os.Getenv("CARGO_TARGET_DIR")
	restore()

	if want := filepath.Join(repo, "target"); filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("CARGO_TARGET_DIR = %q, want the repo's own %q", got, want)
	}
	if strings.Contains(got, "cargo-target") {
		t.Fatalf("CARGO_TARGET_DIR = %q, want no gate-owned cache", got)
	}
}

// TestPinMechCargoTarget_HonoursAnOperatorsTargetDir pins the other half:
// when the environment already says where builds go, the gate builds THERE —
// that is what "the repo's own target" means for a session that shares one.
func TestPinMechCargoTarget_HonoursAnOperatorsTargetDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	shared := t.TempDir()
	t.Setenv("CARGO_TARGET_DIR", shared)
	repo := t.TempDir()

	restore := pinMechCargoTarget(Runner{Cmd: "cargo"}, repo)
	got := os.Getenv("CARGO_TARGET_DIR")
	restore()

	if filepath.Clean(got) != filepath.Clean(shared) {
		t.Fatalf("CARGO_TARGET_DIR = %q, want the operator's own %q", got, shared)
	}
	if os.Getenv("CARGO_TARGET_DIR") != shared {
		t.Fatal("the operator's value must be restored")
	}
}

// TestFailFirstRun_ExportsTheResolvedTarget pins the trap in (b): the
// fail-first worktree lives OUTSIDE the repo, so cargo's default would put a
// brand-new target/ inside it and cold-build the world on every commit. The
// run must name the same resolved target explicitly.
func TestFailFirstRun_ExportsTheResolvedTarget(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	os.Unsetenv("CARGO_TARGET_DIR")
	repo := t.TempDir()
	gitInit(t, repo)
	write(t, repo, "Cargo.toml", "[package]"+"\n"+`name = "a"`+"\n")
	write(t, repo, "src/lib.rs", "pub fn a() {}\n")
	write(t, repo, "tests/a.rs", "#[test]\nfn t() {}\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-q", "-m", "init")
	write(t, repo, "tests/b.rs", "#[test]\nfn u() {}\n")
	gitDo(t, repo, "add", ".")

	var seen string
	failFirstViolatedAt(repo, repo, []string{"tests/b.rs"}, nil, func(r Runner, root string) SuiteResult {
		seen = os.Getenv("CARGO_TARGET_DIR")
		return SuiteResult{Passed: false}
	})
	if want := filepath.Join(repo, "target"); filepath.Clean(seen) != filepath.Clean(want) {
		t.Fatalf("fail-first ran with CARGO_TARGET_DIR=%q, want the repo's resolved target %q", seen, want)
	}
}
