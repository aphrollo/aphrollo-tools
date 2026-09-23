package precommit

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// --- cargo classification -----------------------------------------------------

// TestClassifyFile_RustTestsDir pins cargo's integration-test convention: any
// .rs file under a `tests/` directory segment is a test, whatever its basename
// (cargo compiles each tests/*.rs — and each tests/<dir>/ with a main — as a
// test binary). The existing filename rules (`foo_test.rs`, `test_foo.rs`)
// keep working, and ordinary src files stay Source.
func TestClassifyFile_RustTestsDir(t *testing.T) {
	cases := []struct {
		path string
		want Kind
	}{
		// cargo integration-test layout: role lives in the directory
		{"crates/server/tests/integration/chat.rs", Test},
		{"crates/shared/tests/movement.rs", Test},
		// unchanged: src files are Source
		{"crates/shared/src/movement.rs", Source},
		// unchanged: the existing filename conventions still classify as Test
		{"crates/shared/src/movement_test.rs", Test},
		{"crates/shared/src/test_movement.rs", Test},
	}
	for _, c := range cases {
		if got := ClassifyFile(c.path); got != c.want {
			t.Errorf("ClassifyFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// --- cargo related-test narrowing ----------------------------------------------

// TestNarrowToRelatedTests_CargoTestFiles pins the cargo test-file narrowing:
// a top-level tests/*.rs integration test runs only its own test binary via
// `cargo test --test <name>`, where <name> is the file's stem -- cargo's own
// 1:1 convention, so no metadata check is needed. A Rust test file NOT under
// tests/ (a `#[cfg(test)]` unit-test module file in src/) runs the lib tests
// via `cargo test --lib`. The nested tests/<dir>/ case (a folded binary,
// where <dir> is only a GUESS until cargo metadata confirms it) is pinned
// separately below, since it needs a real [package] Cargo.toml to resolve
// against.
func TestNarrowToRelatedTests_CargoTestFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "src/lib.rs", "pub fn base() -> i32 { 0 }\n")
	write(t, root, "src/thing_test.rs", "#[test]\nfn thing() {}\n")
	write(t, root, "tests/movement.rs", "#[test]\nfn moves() {}\n")

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	cases := []struct {
		name   string
		target string
		want   Runner
	}{
		{
			name:   "top-level tests/ file → --test <stem>",
			target: filepath.Join(root, "tests", "movement.rs"),
			want:   Runner{Cmd: "cargo", Args: []string{"test", "--test", "movement"}, Dir: "", Deadline: time.Time{}},
		},
		{
			name:   "rust test module under src/ → --lib, filtered to that module",
			target: filepath.Join(root, "src", "thing_test.rs"),
			want:   Runner{Cmd: "cargo", Args: []string{"test", "--lib", "thing_test::"}, Dir: "", Deadline: time.Time{}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NarrowToRelatedTests(cargo, c.target, root); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("NarrowToRelatedTests = %+v, want %+v", got, c.want)
			}
		})
	}
}

// stubCargoTestTargets states a workspace's real `--test` target names
// (package name -> its target names) without a cargo metadata run.
func stubCargoTestTargets(t *testing.T, targets map[string]map[string]bool) {
	t.Helper()
	t.Cleanup(SetCargoTestTargetsForTest(func(string) map[string]map[string]bool { return targets }))
}

// TestNarrowToRelatedTests_CargoNestedDirConfirmedByMetadataRunsThatBinary
// pins the fix for issue #250: a file inside a folded test binary
// (tests/integration/affixes.rs, the exact borld shape that produced
// `--test affixes` naming a target that does not exist) must resolve the
// candidate directory name against cargo metadata and, once confirmed, scope
// the run to that real target rather than the file's own stem.
func TestNarrowToRelatedTests_CargoNestedDirConfirmedByMetadataRunsThatBinary(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"item\"\nversion = \"0.1.0\"\n")
	write(t, root, "tests/integration/affixes.rs", "#[test]\nfn affixes() {}\n")
	stubCargoTestTargets(t, map[string]map[string]bool{
		"item": {"integration": true},
	})

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	got := NarrowToRelatedTests(cargo, filepath.Join(root, "tests", "integration", "affixes.rs"), root)
	want := Runner{Cmd: "cargo", Args: []string{"test", "-p", "item", "--test", "integration"}, Dir: root, Deadline: time.Time{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NarrowToRelatedTests = %+v, want %+v", got, want)
	}
}

// TestNarrowToRelatedTests_CargoNestedDirUnconfirmedFallsBackToPackageRun
// pins the other half: a nested tests/<dir>/ file whose directory metadata
// does NOT report as a target (a shared helper directory folded into some
// OTHER binary, not one of its own) must never guess `--test <dir>` -- that
// names a target that does not exist, a red on green code. It falls back to
// the whole package run instead, which stays correct, only broader.
func TestNarrowToRelatedTests_CargoNestedDirUnconfirmedFallsBackToPackageRun(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"item\"\nversion = \"0.1.0\"\n")
	write(t, root, "tests/common/fixtures.rs", "pub fn seed() {}\n")
	stubCargoTestTargets(t, map[string]map[string]bool{
		"item": {"integration": true}, // "common" is a helper dir, not a target
	})

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	got := NarrowToRelatedTests(cargo, filepath.Join(root, "tests", "common", "fixtures.rs"), root)
	want := Runner{Cmd: "cargo", Args: []string{"test", "-p", "item"}, Dir: root, Deadline: time.Time{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NarrowToRelatedTests = %+v, want %+v (never a guessed --test common)", got, want)
	}
}

// TestNarrowToRelatedTests_CargoNestedDirNoPackageNeverGuessesTarget covers
// the crate-with-no-determinable-target case: no Cargo.toml at all means the
// candidate cannot be confirmed either way, and the safe direction is the
// unnarrowed runner, never a guessed --test name.
func TestNarrowToRelatedTests_CargoNestedDirNoPackageNeverGuessesTarget(t *testing.T) {
	root := t.TempDir()
	write(t, root, "tests/integration/affixes.rs", "#[test]\nfn affixes() {}\n")

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	got := NarrowToRelatedTests(cargo, filepath.Join(root, "tests", "integration", "affixes.rs"), root)
	want := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NarrowToRelatedTests = %+v, want %+v (no package to confirm a target against)", got, want)
	}
}

// TestNarrowToRelatedTests_CargoSourceEdits pins the cargo source-edit
// narrowing: a src edit in a LIB crate (src/lib.rs exists on disk) runs the
// unit tests via `cargo test --lib`; a bin-only crate (no src/lib.rs) keeps
// the full `cargo test` unchanged, because `--lib` on a crate with no lib
// target is an error, not a narrower run.
func TestNarrowToRelatedTests_CargoSourceEdits(t *testing.T) {
	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}

	t.Run("lib crate source edit → --lib", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "src/lib.rs", "pub fn base() -> i32 { 0 }\n")
		write(t, root, "src/foo.rs", "pub fn foo() -> i32 { 1 }\n")
		got := NarrowToRelatedTests(cargo, filepath.Join(root, "src", "foo.rs"), root)
		want := Runner{Cmd: "cargo", Args: []string{"test", "--lib", "foo::"}, Dir: "", Deadline: time.Time{}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("lib-crate source narrow = %+v, want %+v", got, want)
		}
	})

	t.Run("bin-only crate source edit → unchanged full suite", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "src/main.rs", "fn main() {}\n")
		write(t, root, "src/foo.rs", "pub fn foo() -> i32 { 1 }\n")
		got := NarrowToRelatedTests(cargo, filepath.Join(root, "src", "foo.rs"), root)
		if !reflect.DeepEqual(got, cargo) {
			t.Fatalf("bin-only source narrow = %+v, want unchanged %+v", got, cargo)
		}
	})
}

// --- precommit mechanical narrowing for cargo workspaces ------------------------

// makeCargoWorkspaceRepo creates a committed two-member cargo workspace
// (crates/alpha, crates/beta) whose root Cargo.toml is workspace-only. Like
// makeCargoRepo, the suite is always faked — only DetectRunner's marker read,
// the member Cargo.tomls, and the git state matter.
func makeCargoWorkspaceRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\", \"crates/beta\"]\n")
	write(t, root, "crates/alpha/Cargo.toml", "[package]\nname = \"alpha\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	write(t, root, "crates/beta/Cargo.toml", "[package]\nname = \"beta\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root
}

// --- fail-first narrowing to staged test targets --------------------------

// TestNarrowFailFirstTests_CargoSinglePackageMixed pins the common case: every
// staged test file owned by the SAME package builds exact `--test <name>`
// scoping (deduped, sorted), with `--lib` appended because one of the staged
// files (src/thing_test.rs) is an inline #[cfg(test)] module rather than a
// tests/*.rs integration binary.
func TestNarrowFailFirstTests_CargoSinglePackageMixed(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"pkg1\"\nversion = \"0.1.0\"\n")
	write(t, root, "tests/foo.rs", "#[test]\nfn foo() {}\n")
	write(t, root, "src/thing_test.rs", "#[test]\nfn thing() {}\n")

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	got := narrowFailFirstTests(cargo, root, []string{"tests/foo.rs", "src/thing_test.rs"})
	want := Runner{Cmd: "cargo", Args: []string{"test", "-p", "pkg1", "--test", "foo", "--lib"}, Dir: "", Deadline: time.Time{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("narrowFailFirstTests = %+v, want %+v", got, want)
	}
}

// TestNarrowFailFirstTests_CargoNextestPreserved guards that narrowing keeps
// the `nextest run` verb (not plain `test`) when the detected runner is
// nextest — the same cargoRunArgs contract narrowToStaged already honours.
func TestNarrowFailFirstTests_CargoNextestPreserved(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"pkg1\"\nversion = \"0.1.0\"\n")
	write(t, root, "tests/foo.rs", "#[test]\nfn foo() {}\n")

	nextest := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}, Dir: "", Deadline: time.Time{}}
	got := narrowFailFirstTests(nextest, root, []string{"tests/foo.rs"})
	want := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "pkg1", "--test", "foo"}, Dir: "", Deadline: time.Time{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("narrowFailFirstTests (nextest) = %+v, want %+v", got, want)
	}
}

// TestNarrowFailFirstTests_CargoNestedDirUnconfirmedDropsTestScoping pins the
// fail-first half of issue #250: a staged test inside a folded binary whose
// directory name cargo metadata does NOT confirm as a target must never
// widen into a guessed `--test <dir>` -- the whole run drops --test scoping
// for the package rather than silently excluding the file it could not
// verify.
func TestNarrowFailFirstTests_CargoNestedDirUnconfirmedDropsTestScoping(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"pkg1\"\nversion = \"0.1.0\"\n")
	write(t, root, "tests/common/fixtures.rs", "pub fn seed() {}\n")
	stubCargoTestTargets(t, map[string]map[string]bool{
		"pkg1": {"integration": true}, // "common" is a helper dir, not a target
	})

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	got := narrowFailFirstTests(cargo, root, []string{"tests/common/fixtures.rs"})
	want := Runner{Cmd: "cargo", Args: []string{"test", "-p", "pkg1"}, Dir: "", Deadline: time.Time{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("narrowFailFirstTests = %+v, want %+v (never a guessed --test common)", got, want)
	}
}

// TestNarrowFailFirstTests_CargoMultiPackageFallback pins the multi-package
// case: staged test files owned by DIFFERENT packages fall back to package
// granularity (`-p a -p b`, no --test scoping) via narrowToStaged.
func TestNarrowFailFirstTests_CargoMultiPackageFallback(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\", \"crates/beta\"]\n")
	write(t, root, "crates/alpha/Cargo.toml", "[package]\nname = \"alpha\"\nversion = \"0.1.0\"\n")
	write(t, root, "crates/beta/Cargo.toml", "[package]\nname = \"beta\"\nversion = \"0.1.0\"\n")
	write(t, root, "crates/alpha/tests/a.rs", "#[test]\nfn a() {}\n")
	write(t, root, "crates/beta/tests/b.rs", "#[test]\nfn b() {}\n")

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	got := narrowFailFirstTests(cargo, root, []string{"crates/beta/tests/b.rs", "crates/alpha/tests/a.rs"})
	want := Runner{Cmd: "cargo", Args: []string{"test", "-p", "alpha", "-p", "beta"}, Dir: "", Deadline: time.Time{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("narrowFailFirstTests (multi-package) = %+v, want %+v", got, want)
	}
}

// TestNarrowFailFirstTests_CargoNoPackageFallback pins the "unowned file"
// fallback: a staged test file that no [package] Cargo.toml owns (only a
// workspace-only virtual manifest above it) must keep the runner unnarrowed —
// today's full-suite fail-open behavior, never a wrong scope.
func TestNarrowFailFirstTests_CargoNoPackageFallback(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\"]\n")
	write(t, root, "crates/alpha/Cargo.toml", "[package]\nname = \"alpha\"\nversion = \"0.1.0\"\n")
	write(t, root, "tools/gen_test.rs", "#[test]\nfn gen() {}\n")

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	got := narrowFailFirstTests(cargo, root, []string{"tools/gen_test.rs"})
	if !reflect.DeepEqual(got, cargo) {
		t.Fatalf("narrowFailFirstTests (no package) = %+v, want unchanged %+v", got, cargo)
	}
}

// TestNarrowFailFirstTests_NonCargoDelegatesToNarrowToStaged guards the
// non-cargo path: a runner with a related mode (go's package granularity)
// gets exactly what narrowToStaged already produces for the staged test file.
func TestNarrowFailFirstTests_NonCargoDelegatesToNarrowToStaged(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module m\n\ngo 1.21\n")
	write(t, root, "internal/x/x_test.go", "package x\n")

	goRunner := Runner{Cmd: "go", Args: []string{"test", "./..."}, Dir: "", Deadline: time.Time{}}
	got := narrowFailFirstTests(goRunner, root, []string{"internal/x/x_test.go"})
	want := Runner{Cmd: "go", Args: []string{"test", "./internal/x"}, Dir: "", Deadline: time.Time{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("narrowFailFirstTests (go) = %+v, want %+v", got, want)
	}
}

// TestNarrowFailFirstTests_NonCargoUnnarrowedFallback guards a runner with NO
// related mode (pytest): the command must stay the full unnarrowed runner.
func TestNarrowFailFirstTests_NonCargoUnnarrowedFallback(t *testing.T) {
	root := t.TempDir()
	write(t, root, "pyproject.toml", "[tool]\n")
	write(t, root, "test_thing.py", "def test_thing(): pass\n")

	pytest := Runner{Cmd: "pytest", Args: []string{"-q"}, Dir: "", Deadline: time.Time{}}
	got := narrowFailFirstTests(pytest, root, []string{"test_thing.py"})
	if !reflect.DeepEqual(got, pytest) {
		t.Fatalf("narrowFailFirstTests (pytest) = %+v, want unchanged %+v", got, pytest)
	}
}

// TestPrecommit_Mechanical_CargoWorkspaceScopedToStagedPackages pins the
// workspace-aware mechanical narrowing. UPDATED 2026-08-15 (build-infra-fix
// task A1): each member crate carries its OWN Cargo.toml, so it is now its
// OWN project root (FindProjectRoot stops at the nearest marker, which is the
// crate's own manifest, before ever reaching the workspace root's) — staged
// files in alpha and beta therefore run as TWO separate `-p <pkg>` commands.
// UPDATED AGAIN (task A4): each command's Dir now points at the WORKSPACE
// root (repoRoot here — that's where a checked-in .config/nextest.toml and
// the workspace's Cargo.lock actually live), even though the run is grouped
// per crate ROOT (recorded by `dir`, the parameter the fake SuiteRunner was
// called with — the crate's own directory, unchanged from A1) and the
// command itself carries only `-p <pkg>`, never a combined `-p alpha -p
// beta` run. A staged file that belongs to NO [package] Cargo.toml is
// SKIPPED (with a stderr note) rather than falling back to the unscoped
// whole-workspace suite — that fallback was the exact "python commit builds
// all of Bevy" bug task A1 fixes. Only SOURCE files are staged in both
// subtests, so fail-first never triggers.
func TestPrecommit_Mechanical_CargoWorkspaceScopedToStagedPackages(t *testing.T) {
	t.Run("staged sources in different member crates → one -p run per crate root, from the workspace root", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeCargoWorkspaceRepo(t)
		write(t, root, "crates/beta/src/lib.rs", "pub fn beta() -> i32 { 2 }\n")
		write(t, root, "crates/alpha/src/lib.rs", "pub fn alpha() -> i32 { 1 }\n")
		write(t, root, "crates/alpha/src/util.rs", "pub fn util() -> i32 { 3 }\n")
		gitDo(t, root, "add", ".")

		var seen []loggedRun
		res := Mechanical(root, recordAllRuns(&seen, func(string) bool { return true }))
		if res.Blocked {
			t.Fatalf("unexpected block: %s", res.Message)
		}
		if len(seen) != 2 {
			t.Fatalf("expected one run per touched crate root, got %d: %+v", len(seen), seen)
		}
		alphaDir := filepath.Join(root, "crates", "alpha")
		betaDir := filepath.Join(root, "crates", "beta")
		byDir := map[string]Runner{}
		for _, r := range seen {
			byDir[r.dir] = r.runner
		}
		if want := (Runner{Cmd: "cargo", Args: []string{"test", "-p", "alpha"}, Dir: root}); !reflect.DeepEqual(byDir[alphaDir], want) {
			t.Fatalf("alpha run = %+v, want %+v (all runs: %+v)", byDir[alphaDir], want, seen)
		}
		if want := (Runner{Cmd: "cargo", Args: []string{"test", "-p", "beta"}, Dir: root}); !reflect.DeepEqual(byDir[betaDir], want) {
			t.Fatalf("beta run = %+v, want %+v (all runs: %+v)", byDir[betaDir], want, seen)
		}
	})

	t.Run("staged source outside any package → skipped, no cargo run at all", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeCargoWorkspaceRepo(t)
		write(t, root, "tools/gen.rs", "pub fn gen() -> i32 { 0 }\n")
		gitDo(t, root, "add", ".")

		var seen []Runner
		var res GateResult
		stderr := captureStderr(t, func() {
			res = Mechanical(root, recordRunner(&seen, root))
		})
		if res.Blocked {
			t.Fatalf("unexpected block: %s", res.Message)
		}
		if len(seen) != 0 {
			t.Fatalf("an unowned file must run NOTHING (full-suite fallback removed), ran: %+v", seen)
		}
		wantNote := "gate premerge: tools/gen.rs has no owning cargo package — not tested"
		if !strings.Contains(stderr, wantNote) {
			t.Fatalf("expected unowned-file note %q, got stderr: %q", wantNote, stderr)
		}
	})
}
