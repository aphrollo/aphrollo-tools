package tdd

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

	cargo := Runner{"cargo", []string{"test"}, "", time.Time{}}
	cases := []struct {
		name   string
		target string
		want   Runner
	}{
		{
			name:   "top-level tests/ file → --test <stem>",
			target: filepath.Join(root, "tests", "movement.rs"),
			want:   Runner{"cargo", []string{"test", "--test", "movement"}, "", time.Time{}},
		},
		{
			name:   "nested tests/ dir → --test <dir> (named test binary)",
			target: filepath.Join(root, "tests", "integration", "chat.rs"),
			want:   Runner{"cargo", []string{"test", "--test", "integration"}, "", time.Time{}},
		},
		{
			name:   "rust test module under src/ → --lib, filtered to that module",
			target: filepath.Join(root, "src", "thing_test.rs"),
			want:   Runner{"cargo", []string{"test", "--lib", "thing_test::"}, "", time.Time{}},
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
	cargo := Runner{"cargo", []string{"test"}, "", time.Time{}}

	t.Run("lib crate source edit → --lib", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "src/lib.rs", "pub fn base() -> i32 { 0 }\n")
		write(t, root, "src/foo.rs", "pub fn foo() -> i32 { 1 }\n")
		got := NarrowToRelatedTests(cargo, filepath.Join(root, "src", "foo.rs"), root)
		want := Runner{"cargo", []string{"test", "--lib", "foo::"}, "", time.Time{}}
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

	cargo := Runner{"cargo", []string{"test"}, "", time.Time{}}
	got := narrowFailFirstTests(cargo, root, []string{"tests/foo.rs", "src/thing_test.rs"})
	want := Runner{"cargo", []string{"test", "-p", "pkg1", "--test", "foo", "--lib"}, "", time.Time{}}
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

	nextest := Runner{"cargo", []string{"nextest", "run"}, "", time.Time{}}
	got := narrowFailFirstTests(nextest, root, []string{"tests/foo.rs"})
	want := Runner{"cargo", []string{"nextest", "run", "-p", "pkg1", "--test", "foo"}, "", time.Time{}}
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

	cargo := Runner{"cargo", []string{"test"}, "", time.Time{}}
	got := narrowFailFirstTests(cargo, root, []string{"crates/beta/tests/b.rs", "crates/alpha/tests/a.rs"})
	want := Runner{"cargo", []string{"test", "-p", "alpha", "-p", "beta"}, "", time.Time{}}
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

	cargo := Runner{"cargo", []string{"test"}, "", time.Time{}}
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

	goRunner := Runner{"go", []string{"test", "./..."}, "", time.Time{}}
	got := narrowFailFirstTests(goRunner, root, []string{"internal/x/x_test.go"})
	want := Runner{"go", []string{"test", "./internal/x"}, "", time.Time{}}
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

	pytest := Runner{"pytest", []string{"-q"}, "", time.Time{}}
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
		res := Precommit(root, recordAllRuns(&seen, func(string) bool { return true }))
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
			res = Precommit(root, recordRunner(&seen, root))
		})
		if res.Blocked {
			t.Fatalf("unexpected block: %s", res.Message)
		}
		if len(seen) != 0 {
			t.Fatalf("an unowned file must run NOTHING (full-suite fallback removed), ran: %+v", seen)
		}
		wantNote := "gate precommit: tools/gen.rs has no owning cargo package — not tested"
		if !strings.Contains(stderr, wantNote) {
			t.Fatalf("expected unowned-file note %q, got stderr: %q", wantNote, stderr)
		}
	})
}

// --- always-run packages --------------------------------------------------

// A workspace-wide guard crate (a lint/ratchet package whose tests scan the
// whole tree) is owned by no staged file, so ownership scoping runs it only
// when someone edits the guard itself — exactly when its invariant is not at
// risk. always-run is the workspace declaring "these packages run every
// mechanical stage regardless of what was staged".

// Break this catches: the metadata key is ignored, so a declared guard package
// never joins the run and the gate keeps reporting green over rules it never
// evaluated.
func TestCargoAlwaysRunPackages_ReadsWorkspaceMetadata(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\"]\n\n"+
		"[workspace.metadata.aphrollo]\nalways-run = [\"ratchet\", \"guards\"]\n")

	got := cargoAlwaysRunPackages(root)
	want := []string{"guards", "ratchet"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cargoAlwaysRunPackages = %q, want %q", got, want)
	}
}

// Break this catches: a project that never opted in gains phantom packages,
// turning every commit in every other repo slower and possibly red.
func TestCargoAlwaysRunPackages_AbsentMetadataIsEmpty(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\"]\n")

	if got := cargoAlwaysRunPackages(root); len(got) != 0 {
		t.Fatalf("cargoAlwaysRunPackages = %q, want none", got)
	}
}

// Break this catches: a key under a DIFFERENT metadata table is read as ours,
// so an unrelated tool's config silently changes what the gate runs.
func TestCargoAlwaysRunPackages_OtherToolsMetadataIgnored(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\"]\n\n"+
		"[workspace.metadata.othertool]\nalways-run = [\"nope\"]\n")

	if got := cargoAlwaysRunPackages(root); len(got) != 0 {
		t.Fatalf("cargoAlwaysRunPackages = %q, want none", got)
	}
}

// Break this catches: the declared guard package is parsed and then dropped,
// or (the other failure mode) bundled into the touched-crate command, where
// it waits for that crate to build before a pure crate's cheap suite can say
// anything.
func TestMechanical_CargoAlwaysRunPackage_RunsFirstAsItsOwnCommand(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoWorkspaceRepo(t)
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\", \"crates/beta\"]\n\n"+
		"[workspace.metadata.aphrollo]\nalways-run = [\"beta\"]\n")
	write(t, root, "crates/alpha/src/lib.rs", "pub fn widget() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, filepath.Join(root, "crates", "alpha")))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	wantGuard := Runner{Cmd: "cargo", Args: []string{"test", "-p", "beta"}, Dir: root}
	wantTouched := Runner{Cmd: "cargo", Args: []string{"test", "-p", "alpha"}, Dir: root}
	if len(seen) != 2 {
		t.Fatalf("want the guard crate then the touched crate, got %+v", seen)
	}
	if !reflect.DeepEqual(seen[0], wantGuard) {
		t.Fatalf("first run = %+v, want the guard crate alone %+v", seen[0], wantGuard)
	}
	if !reflect.DeepEqual(seen[1], wantTouched) {
		t.Fatalf("second run = %+v, want the touched crate alone %+v", seen[1], wantTouched)
	}
}

// Break this catches: a package both staged and always-run is passed twice,
// which changes the argv and so the mech-cache key for an identical tree.
func TestMechanical_CargoAlwaysRunPackage_NotDuplicatedWhenAlsoStaged(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoWorkspaceRepo(t)
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\", \"crates/beta\"]\n\n"+
		"[workspace.metadata.aphrollo]\nalways-run = [\"beta\"]\n")
	write(t, root, "crates/beta/src/lib.rs", "pub fn widget() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, filepath.Join(root, "crates", "beta")))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := Runner{Cmd: "cargo", Args: []string{"test", "-p", "beta"}, Dir: root}
	if len(seen) != 1 || !reflect.DeepEqual(seen[0], want) {
		t.Fatalf("mechanical run = %+v, want one %+v", seen, want)
	}
}

// The fail-first stage re-proves that a STAGED test fails at HEAD; a guard
// package has no staged test under judgment there, so joining it would only
// add build time to a stage that must stay narrow.
//
// Break this catches: always-run bleeding into fail-first narrowing.
func TestNarrowFailFirstTests_CargoIgnoresAlwaysRun(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"pkg1\"\nversion = \"0.1.0\"\n\n"+
		"[workspace.metadata.aphrollo]\nalways-run = [\"guards\"]\n")
	write(t, root, "tests/foo.rs", "#[test]\nfn foo() {}\n")

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}}
	got := narrowFailFirstTests(cargo, root, []string{"tests/foo.rs"})
	for _, a := range got.Args {
		if a == "guards" {
			t.Fatalf("fail-first must not gain an always-run package: %q", got.Args)
		}
	}
}
