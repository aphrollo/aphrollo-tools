package tdd

import (
	"path/filepath"
	"reflect"
	"testing"
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
// a tests/*.rs integration test runs only its own test binary via
// `cargo test --test <name>`, where <name> is the path component directly
// under tests/ (a nested dir under tests/ is a named test binary with a
// main.rs). A Rust test file NOT under tests/ (a `#[cfg(test)]` unit-test
// module file in src/) runs the lib tests via `cargo test --lib`.
func TestNarrowToRelatedTests_CargoTestFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "src/lib.rs", "pub fn base() -> i32 { 0 }\n")
	write(t, root, "src/thing_test.rs", "#[test]\nfn thing() {}\n")
	write(t, root, "tests/movement.rs", "#[test]\nfn moves() {}\n")
	write(t, root, "tests/integration/chat.rs", "#[test]\nfn chats() {}\n")

	cargo := Runner{"cargo", []string{"test"}}
	cases := []struct {
		name   string
		target string
		want   Runner
	}{
		{
			name:   "top-level tests/ file → --test <stem>",
			target: filepath.Join(root, "tests", "movement.rs"),
			want:   Runner{"cargo", []string{"test", "--test", "movement"}},
		},
		{
			name:   "nested tests/ dir → --test <dir> (named test binary)",
			target: filepath.Join(root, "tests", "integration", "chat.rs"),
			want:   Runner{"cargo", []string{"test", "--test", "integration"}},
		},
		{
			name:   "rust test file outside tests/ → --lib",
			target: filepath.Join(root, "src", "thing_test.rs"),
			want:   Runner{"cargo", []string{"test", "--lib"}},
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

// TestNarrowToRelatedTests_CargoSourceEdits pins the cargo source-edit
// narrowing: a src edit in a LIB crate (src/lib.rs exists on disk) runs the
// unit tests via `cargo test --lib`; a bin-only crate (no src/lib.rs) keeps
// the full `cargo test` unchanged, because `--lib` on a crate with no lib
// target is an error, not a narrower run.
func TestNarrowToRelatedTests_CargoSourceEdits(t *testing.T) {
	cargo := Runner{"cargo", []string{"test"}}

	t.Run("lib crate source edit → --lib", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "src/lib.rs", "pub fn base() -> i32 { 0 }\n")
		write(t, root, "src/foo.rs", "pub fn foo() -> i32 { 1 }\n")
		got := NarrowToRelatedTests(cargo, filepath.Join(root, "src", "foo.rs"), root)
		want := Runner{"cargo", []string{"test", "--lib"}}
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

	cargo := Runner{"cargo", []string{"test"}}
	got := narrowFailFirstTests(cargo, root, []string{"tests/foo.rs", "src/thing_test.rs"})
	want := Runner{"cargo", []string{"test", "-p", "pkg1", "--test", "foo", "--lib"}}
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

	nextest := Runner{"cargo", []string{"nextest", "run"}}
	got := narrowFailFirstTests(nextest, root, []string{"tests/foo.rs"})
	want := Runner{"cargo", []string{"nextest", "run", "-p", "pkg1", "--test", "foo"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("narrowFailFirstTests (nextest) = %+v, want %+v", got, want)
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

	cargo := Runner{"cargo", []string{"test"}}
	got := narrowFailFirstTests(cargo, root, []string{"crates/beta/tests/b.rs", "crates/alpha/tests/a.rs"})
	want := Runner{"cargo", []string{"test", "-p", "alpha", "-p", "beta"}}
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

	cargo := Runner{"cargo", []string{"test"}}
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

	goRunner := Runner{"go", []string{"test", "./..."}}
	got := narrowFailFirstTests(goRunner, root, []string{"internal/x/x_test.go"})
	want := Runner{"go", []string{"test", "./internal/x"}}
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

	pytest := Runner{"pytest", []string{"-q"}}
	got := narrowFailFirstTests(pytest, root, []string{"test_thing.py"})
	if !reflect.DeepEqual(got, pytest) {
		t.Fatalf("narrowFailFirstTests (pytest) = %+v, want unchanged %+v", got, pytest)
	}
}

// TestPrecommit_Mechanical_CargoWorkspaceScopedToStagedPackages pins the
// workspace-aware mechanical narrowing: staged .rs files map to the [package]
// Cargo.toml that owns them, and the mechanical run becomes
// `cargo test -p <pkg> …` with the package list deduped and sorted — not the
// whole-workspace `cargo test`. A staged code file that belongs to NO
// [package] Cargo.toml falls back to the unnarrowed full suite. Only SOURCE
// files are staged, so fail-first never triggers and the single run recorded
// at root is the mechanical one.
func TestPrecommit_Mechanical_CargoWorkspaceScopedToStagedPackages(t *testing.T) {
	t.Run("staged sources in member crates → -p per package, deduped and sorted", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeCargoWorkspaceRepo(t)
		// Two files in alpha prove the package list is deduped; beta written
		// first proves the final list is sorted, not staging-ordered.
		write(t, root, "crates/beta/src/lib.rs", "pub fn beta() -> i32 { 2 }\n")
		write(t, root, "crates/alpha/src/lib.rs", "pub fn alpha() -> i32 { 1 }\n")
		write(t, root, "crates/alpha/src/util.rs", "pub fn util() -> i32 { 3 }\n")
		gitDo(t, root, "add", ".")

		var seen []Runner
		res := Precommit(root, recordRunner(&seen, root))
		if res.Blocked {
			t.Fatalf("unexpected block: %s", res.Message)
		}
		want := Runner{"cargo", []string{"test", "-p", "alpha", "-p", "beta"}}
		if len(seen) != 1 || !reflect.DeepEqual(seen[0], want) {
			t.Fatalf("workspace mechanical runs = %+v, want one %+v", seen, want)
		}
	})

	t.Run("staged source outside any package → full-suite fallback", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		root := makeCargoWorkspaceRepo(t)
		write(t, root, "tools/gen.rs", "pub fn gen() -> i32 { 0 }\n")
		gitDo(t, root, "add", ".")

		var seen []Runner
		res := Precommit(root, recordRunner(&seen, root))
		if res.Blocked {
			t.Fatalf("unexpected block: %s", res.Message)
		}
		want := Runner{"cargo", []string{"test"}}
		if len(seen) != 1 || !reflect.DeepEqual(seen[0], want) {
			t.Fatalf("no-package mechanical runs = %+v, want one full-suite %+v", seen, want)
		}
	})
}
