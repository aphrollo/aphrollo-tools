package tdd

import (
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// makeNestedCargoWorkspace builds a two-member cargo workspace like
// makeCargoWorkspaceRepo, but git-uninitialized (these tests call
// NarrowToRelatedTests/cargoWorkspaceRoot directly, not through Precommit,
// so no git state is needed) and with a checked-in .config/nextest.toml at
// the WORKSPACE root only — never inside a member crate's own directory,
// matching how a real repo like borld is laid out.
func makeNestedCargoWorkspace(t *testing.T, withNextestConfig bool) (wsRoot, memberRoot string) {
	t.Helper()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\"]\n")
	write(t, root, "crates/alpha/Cargo.toml", "[package]\nname = \"alpha\"\nversion = \"0.1.0\"\n")
	write(t, root, "crates/alpha/src/lib.rs", "pub fn base() -> i32 { 0 }\n")
	if withNextestConfig {
		write(t, root, ".config/nextest.toml", "[profile.default]\n")
	}
	return root, filepath.Join(root, "crates", "alpha")
}

// TestCargoWorkspaceRoot pins the resolution rule (task A4): walk up from a
// cargo project root to the nearest ancestor (root inclusive) whose
// Cargo.toml declares a [workspace] table; a crate with no encompassing
// workspace falls back to being its own "workspace root".
func TestCargoWorkspaceRoot(t *testing.T) {
	t.Run("member crate resolves to the workspace root above it", func(t *testing.T) {
		ws, member := makeNestedCargoWorkspace(t, false)
		if got := cargoWorkspaceRoot(member); got != ws {
			t.Fatalf("cargoWorkspaceRoot(%s) = %s, want %s", member, got, ws)
		}
	})

	t.Run("a crate that IS the workspace root resolves to itself", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "Cargo.toml", "[workspace]\nmembers = [\".\"]\n\n[package]\nname = \"solo\"\n")
		if got := cargoWorkspaceRoot(root); got != root {
			t.Fatalf("cargoWorkspaceRoot(%s) = %s, want %s (itself)", root, got, root)
		}
	})

	t.Run("a standalone crate with no encompassing workspace falls back to itself", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "Cargo.toml", "[package]\nname = \"lonely\"\nversion = \"0.1.0\"\n")
		if got := cargoWorkspaceRoot(root); got != root {
			t.Fatalf("cargoWorkspaceRoot(%s) = %s, want %s (itself, no workspace found)", root, got, root)
		}
	})

	// Found in review 2026-08-15: cargoTomlHasWorkspaceTable matched only an
	// EXACT "[workspace]" line, so a real-world manifest with a trailing
	// comment or trailing whitespace on the table header silently fell back
	// to "no workspace found" -- the member crate below it would then run
	// unscoped from its own directory instead of the real workspace root.
	t.Run("a [workspace] header with trailing whitespace/comment is still recognized", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "Cargo.toml", "[workspace]  # root\nmembers = [\"crates/a\"]\n")
		write(t, root, "crates/a/Cargo.toml", "[package]\nname = \"a\"\n")
		member := filepath.Join(root, "crates", "a")
		if got := cargoWorkspaceRoot(member); got != root {
			t.Fatalf("cargoWorkspaceRoot(%s) = %s, want %s ([workspace] with trailing comment must still be recognized)", member, got, root)
		}
	})
}

// TestNarrowToRelatedTests_CargoMember_RunsFromWorkspaceRoot pins the core
// A4 behavior for PostEdit's per-edit narrowing: editing a test file inside
// a cargo workspace MEMBER (which has its own resolvable [package] name)
// must produce a command scoped by `-p <pkg>`, executed from the WORKSPACE
// root (Dir field) — not the member's own directory — because that is where
// a checked-in .config/nextest.toml and the workspace's Cargo.lock actually
// live. No .config/nextest.toml exists here, so the verb is deterministically
// plain `test` regardless of whether cargo-nextest happens to be installed on
// the box running this test.
func TestNarrowToRelatedTests_CargoMember_RunsFromWorkspaceRoot(t *testing.T) {
	ws, member := makeNestedCargoWorkspace(t, false)
	write(t, member, "tests/movement.rs", "#[test]\nfn moves() {}\n")

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	got := NarrowToRelatedTests(cargo, filepath.Join(member, "tests", "movement.rs"), member)
	want := Runner{Cmd: "cargo", Args: []string{"test", "-p", "alpha", "--test", "movement"}, Dir: ws}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NarrowToRelatedTests = %+v, want %+v", got, want)
	}
}

// TestNarrowToRelatedTests_CargoMember_NextestWhenConfiguredAtWorkspaceRoot
// pins the nextest-selection half: a member crate whose OWN directory has no
// .config/nextest.toml would (before this task) always fall back to plain
// `cargo test`, silently losing nextest even in a repo that has it
// configured — because DetectRunner/cargoRunArgs only ever checked the
// crate's own directory. Resolving the real workspace root fixes that: the
// nextest.toml at the WORKSPACE root is what must decide the verb. The
// expected verb is derived from nextestInstalled() — an independent
// environmental fact, not a re-implementation of the classification logic
// under test — so this passes deterministically whether or not this box has
// cargo-nextest installed.
func TestNarrowToRelatedTests_CargoMember_NextestWhenConfiguredAtWorkspaceRoot(t *testing.T) {
	ws, member := makeNestedCargoWorkspace(t, true)
	write(t, member, "src/foo.rs", "pub fn foo() -> i32 { 1 }\n")

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	got := NarrowToRelatedTests(cargo, filepath.Join(member, "src", "foo.rs"), member)

	wantVerb := []string{"test"}
	wantFilter := []string{"foo::"}
	if nextestInstalled() {
		wantVerb = []string{"nextest", "run"}
		wantFilter = []string{"-E", "test(/^foo::/)"}
	}
	wantArgs := append(append([]string{}, wantVerb...), "-p", "alpha", "--lib")
	want := Runner{Cmd: "cargo", Args: append(wantArgs, wantFilter...), Dir: ws}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NarrowToRelatedTests = %+v, want %+v", got, want)
	}
}

// TestNarrowToRelatedTests_CargoMember_SourceEdit_RunsFromWorkspaceRoot
// mirrors the test-file case above for a SOURCE edit (narrowSourceEdit's
// cargo branch): `--lib`, scoped by `-p <pkg>`, executed from the workspace
// root.
func TestNarrowToRelatedTests_CargoMember_SourceEdit_RunsFromWorkspaceRoot(t *testing.T) {
	ws, member := makeNestedCargoWorkspace(t, false)
	write(t, member, "src/foo.rs", "pub fn foo() -> i32 { 1 }\n")

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	got := NarrowToRelatedTests(cargo, filepath.Join(member, "src", "foo.rs"), member)
	want := Runner{Cmd: "cargo", Args: []string{"test", "-p", "alpha", "--lib", "foo::"}, Dir: ws}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NarrowToRelatedTests = %+v, want %+v", got, want)
	}
}

// TestNarrowToRelatedTests_CargoNoResolvablePackage_FallsBackToOldBehavior
// guards the fallback path: when the project root has no readable Cargo.toml
// at all (a synthetic fixture, or a real edge case), the workspace-root
// resolution must never SWALLOW the narrowing entirely — it falls back to
// the pre-A4 behavior (no -p, no Dir, cwd implicitly the given root) rather
// than losing --test/--lib scoping altogether.
func TestNarrowToRelatedTests_CargoNoResolvablePackage_FallsBackToOldBehavior(t *testing.T) {
	root := t.TempDir()
	write(t, root, "tests/movement.rs", "#[test]\nfn moves() {}\n") // no Cargo.toml anywhere

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	got := NarrowToRelatedTests(cargo, filepath.Join(root, "tests", "movement.rs"), root)
	want := Runner{Cmd: "cargo", Args: []string{"test", "--test", "movement"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NarrowToRelatedTests = %+v, want %+v (old behavior preserved)", got, want)
	}
}

// TestPrecommit_Mechanical_CargoMember_RunsFromWorkspaceRoot pins the
// Precommit side: the mechanical stage for a cargo workspace member also
// runs from the workspace root (Dir), never the member's own directory.
// State/mech-cache keys still use the member's own root internally
// (unchanged from task A1/A2 — mechKey is keyed on `root`, not `Dir`); this
// test pins the OBSERVABLE half, that the actual command executes with
// Dir set to the workspace root.
func TestPrecommit_Mechanical_CargoMember_RunsFromWorkspaceRoot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoWorkspaceRepo(t)
	write(t, root, "crates/alpha/src/lib.rs", "pub fn alpha() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, filepath.Join(root, "crates", "alpha")))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := Runner{Cmd: "cargo", Args: []string{"test", "-p", "alpha"}, Dir: root}
	if len(seen) != 1 || !reflect.DeepEqual(seen[0], want) {
		t.Fatalf("mechanical run = %+v, want one %+v", seen, want)
	}
}

// TestRunSuite_UsesRunnerDirOverRoot pins Runner.Dir's actual execution
// contract (task A4 point 2): when set, RunSuite must run the command IN
// Dir, not in the root parameter it was called with — proven by reading a
// marker file that exists ONLY in Dir, never in the (different) root passed
// alongside it.
func TestRunSuite_UsesRunnerDirOverRoot(t *testing.T) {
	dirWithFile := t.TempDir()
	write(t, dirWithFile, "marker.txt", "present\n")
	otherDir := t.TempDir() // deliberately does NOT contain marker.txt

	r := readMarkerRunner(dirWithFile)
	res := RunSuite(5*time.Second)(r, otherDir)
	if !res.Passed {
		t.Fatalf("expected the command to succeed reading marker.txt from Runner.Dir, got: passed=%v output=%q err=%q",
			res.Passed, res.Output, res.Err)
	}
	if !strings.Contains(res.Output, "present") {
		t.Fatalf("expected output to contain the marker file's content, got: %q", res.Output)
	}
}

// TestRunSuite_FallsBackToRootWhenDirUnset guards the default: a Runner with
// no Dir set (every runner except a resolved cargo one) must still run in
// the root parameter, exactly as before Runner.Dir existed.
func TestRunSuite_FallsBackToRootWhenDirUnset(t *testing.T) {
	dirWithFile := t.TempDir()
	write(t, dirWithFile, "marker.txt", "present\n")

	r := readMarkerRunner("")
	res := RunSuite(5*time.Second)(r, dirWithFile)
	if !res.Passed {
		t.Fatalf("expected the command to succeed reading marker.txt from root, got: passed=%v output=%q err=%q",
			res.Passed, res.Output, res.Err)
	}
}

// readMarkerRunner builds a portable "print marker.txt" Runner. dir, if
// non-empty, is set as Runner.Dir; "" leaves Dir unset (RunSuite must then
// fall back to whatever root it's called with).
func readMarkerRunner(dir string) Runner {
	if runtime.GOOS == "windows" {
		return Runner{Cmd: "cmd", Args: []string{"/C", "type marker.txt"}, Dir: dir}
	}
	return Runner{Cmd: "cat", Args: []string{"marker.txt"}, Dir: dir}
}
