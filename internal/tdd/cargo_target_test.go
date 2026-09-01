package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// cargoCrate builds a crate root whose manifest names pkg.
func cargoCrate(t *testing.T, pkg string) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \""+pkg+"\"\nversion = \"0.1.0\"\n")
	write(t, root, "src/lib.rs", "pub fn x() {}\n")
	return root
}

// TestNarrow_TestModuleUnderSrcRunsTheLib pins a real misfire (2026-09-02): a
// `#[cfg(test)] mod` file under src/ — borld's crates/clouds/src/
// image_period_tests.rs — is compiled into the crate's LIB test binary, and
// there is no `tests/image_period_tests.rs` for `--test` to select. The run
// must be `--lib`, narrowed to that module's own tests.
func TestNarrow_TestModuleUnderSrcRunsTheLib(t *testing.T) {
	root := cargoCrate(t, "clouds")
	base := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}

	got := NarrowToRelatedTests(base, filepath.Join(root, "src", "image_period_tests.rs"), root)
	args := strings.Join(got.Args, " ")
	if strings.Contains(args, "--test") {
		t.Fatalf("args = %q, want --lib: a src/ module has no test TARGET of its own", args)
	}
	if !strings.Contains(args, "--lib") {
		t.Fatalf("args = %q, want --lib", args)
	}
	if !strings.Contains(args, "image_period_tests::") {
		t.Fatalf("args = %q, want a filter on the module path so one edit does not run the whole lib", args)
	}
}

// TestNarrow_NestedSrcModuleFilterUsesTheModulePath pins the derivation: the
// path under src/ IS the module path, so src/a/b_tests.rs is a::b_tests.
func TestNarrow_NestedSrcModuleFilterUsesTheModulePath(t *testing.T) {
	root := cargoCrate(t, "forge_solver")
	base := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}

	got := NarrowToRelatedTests(base, filepath.Join(root, "src", "truss", "state_tests.rs"), root)
	if args := strings.Join(got.Args, " "); !strings.Contains(args, "truss::state_tests::") {
		t.Fatalf("args = %q, want the nested module path", args)
	}
}

// TestNarrow_LibRootHasNoFilter pins the exception: src/lib.rs IS the crate,
// so there is no submodule to narrow to.
func TestNarrow_LibRootHasNoFilter(t *testing.T) {
	root := cargoCrate(t, "shared")
	base := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}

	got := NarrowToRelatedTests(base, filepath.Join(root, "src", "lib.rs"), root)
	args := strings.Join(got.Args, " ")
	if strings.Contains(args, "-E") {
		t.Fatalf("args = %q, want the whole lib for a crate-root edit", args)
	}
	if !strings.Contains(args, "--lib") {
		t.Fatalf("args = %q, want --lib", args)
	}
}

// TestNarrow_IntegrationTestsStillMapToTheirTarget pins what did work and
// must keep working: a file directly under tests/ is its own test binary, and
// tests/<dir>/main.rs is the <dir> binary.
func TestNarrow_IntegrationTestsStillMapToTheirTarget(t *testing.T) {
	root := cargoCrate(t, "server")
	base := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}

	got := NarrowToRelatedTests(base, filepath.Join(root, "tests", "wire.rs"), root)
	if args := strings.Join(got.Args, " "); !strings.Contains(args, "--test wire") {
		t.Fatalf("args = %q, want --test wire", args)
	}
	got = NarrowToRelatedTests(base, filepath.Join(root, "tests", "integration", "main.rs"), root)
	if args := strings.Join(got.Args, " "); !strings.Contains(args, "--test integration") {
		t.Fatalf("args = %q, want --test integration", args)
	}
}

// TestNarrow_ExamplesAndBenchesBuildOnly pins the two target kinds that carry
// no tests at all: an example is built, a bench is built and NOT run (a bench
// run at edit time costs minutes and proves nothing about correctness).
func TestNarrow_ExamplesAndBenchesBuildOnly(t *testing.T) {
	root := cargoCrate(t, "movement")
	base := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}

	got := NarrowToRelatedTests(base, filepath.Join(root, "examples", "nubis", "capture.rs"), root)
	if args := strings.Join(got.Args, " "); !strings.Contains(args, "--example nubis") {
		t.Fatalf("args = %q, want --example nubis", args)
	}
	got = NarrowToRelatedTests(base, filepath.Join(root, "benches", "apply_movement.rs"), root)
	args := strings.Join(got.Args, " ")
	if !strings.Contains(args, "--bench apply_movement") || !strings.Contains(args, "--no-run") {
		t.Fatalf("args = %q, want the bench BUILT, not run", args)
	}
}

// TestCargoModuleFilter_DerivesTheModulePath pins the mapping itself, so the
// rule is readable without a runner around it.
func TestCargoModuleFilter_DerivesTheModulePath(t *testing.T) {
	cases := []struct{ rel, want string }{
		{"src/image_period_tests.rs", "image_period_tests"},
		{"src/truss/state_tests.rs", "truss::state_tests"},
		{"src/truss/mod.rs", "truss"},
		{"src/lib.rs", ""},
		{"src/main.rs", ""},
		{"tests/wire.rs", ""},
	}
	for _, c := range cases {
		if got := cargoModulePath(c.rel); got != c.want {
			t.Errorf("cargoModulePath(%q) = %q, want %q", c.rel, got, c.want)
		}
	}
}
