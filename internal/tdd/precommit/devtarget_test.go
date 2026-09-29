package precommit

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
// Serial: edits the process-wide environment.
func TestPinMechCargoTarget_UsesTheReposOwnDevTarget(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	os.Unsetenv("CARGO_TARGET_DIR")
	repo := t.TempDir()

	pinned := pinMechCargoTarget(Runner{Cmd: "cargo"}, repo)
	got := envBinding(pinned, "CARGO_TARGET_DIR")

	if want := filepath.Join(repo, "target"); filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("CARGO_TARGET_DIR = %q, want the repo's own %q", got, want)
	}
	if strings.Contains(got, "cargo-target") {
		t.Fatalf("CARGO_TARGET_DIR = %q, want no gate-owned cache", got)
	}
	if _, set := os.LookupEnv("CARGO_TARGET_DIR"); set {
		t.Fatal("pinning the target must not write the process environment")
	}
}

// TestPinMechCargoTarget_LeavesANonCargoRunnerAlone pins that the target dir
// is a cargo concept: a go or npm runner comes back with no binding at all.
func TestPinMechCargoTarget_LeavesANonCargoRunnerAlone(t *testing.T) {
	t.Parallel()
	got := pinMechCargoTarget(Runner{Cmd: "go", Args: []string{"test"}}, t.TempDir())
	if len(got.Env) != 0 {
		t.Fatalf("a go runner came back with Env %v, want none", got.Env)
	}
}

// TestPinMechCargoTarget_KeepsTheRunnersOtherBindings pins that the target
// binding is added beside a runner's own Env, not over it.
func TestPinMechCargoTarget_KeepsTheRunnersOtherBindings(t *testing.T) {
	t.Parallel()
	got := pinMechCargoTarget(Runner{Cmd: "cargo", Env: []string{"RUSTFLAGS=-Dwarnings"}}, t.TempDir())
	if envBinding(got, "RUSTFLAGS") != "-Dwarnings" {
		t.Fatalf("Env = %v, want RUSTFLAGS kept", got.Env)
	}
	if envBinding(got, "CARGO_TARGET_DIR") == "" {
		t.Fatalf("Env = %v, want a CARGO_TARGET_DIR binding", got.Env)
	}
}

// envBinding is the value a runner's Env binds key to (the last binding wins,
// as it does in the child), "" when it binds none.
func envBinding(r Runner, key string) string {
	val := ""
	for _, kv := range r.Env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			val = v
		}
	}
	return val
}

// TestPinMechCargoTarget_HonoursAnOperatorsTargetDir pins the other half:
// when the environment already says where builds go, the gate builds THERE —
// that is what "the repo's own target" means for a session that shares one.
// Serial: sets the process-wide env var CARGO_TARGET_DIR.
func TestPinMechCargoTarget_HonoursAnOperatorsTargetDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	shared := t.TempDir()
	t.Setenv("CARGO_TARGET_DIR", shared)
	repo := t.TempDir()

	pinned := pinMechCargoTarget(Runner{Cmd: "cargo"}, repo)
	got := envBinding(pinned, "CARGO_TARGET_DIR")

	if filepath.Clean(got) != filepath.Clean(shared) {
		t.Fatalf("CARGO_TARGET_DIR = %q, want the operator's own %q", got, shared)
	}
	if os.Getenv("CARGO_TARGET_DIR") != shared {
		t.Fatal("the operator's value must be left as it was")
	}
}

// TestFailFirstRun_ExportsTheResolvedTarget pins the trap in (b): the
// fail-first worktree lives OUTSIDE the repo, so cargo's default would put a
// brand-new target/ inside it and cold-build the world on every commit. The
// run must name the same resolved target explicitly.
// Serial: edits the process-wide environment.
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
		seen = envBinding(r, "CARGO_TARGET_DIR")
		return SuiteResult{Passed: false}
	})
	if want := filepath.Join(repo, "target"); filepath.Clean(seen) != filepath.Clean(want) {
		t.Fatalf("fail-first ran with CARGO_TARGET_DIR=%q, want the repo's resolved target %q", seen, want)
	}
}
