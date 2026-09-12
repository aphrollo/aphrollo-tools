package tdd

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixture crate below is a REAL, compilable cargo crate carrying every
// `#[path]` shape this file judges, so the module paths asserted here are the
// ones rustc itself mounts (TestE2E_CargoPathMod_TheDerivedFilterSelects-
// TheMountedTests runs cargo over the same tree and proves it), not a belief
// about a string.
//
// The reported shape (issue #637): crates/client/src/prediction_systems.rs is
// mounted as `prediction::systems`, so its tests are `prediction::systems::
// tests::…` and the stem-derived filter `prediction_systems::` selects ZERO
// of them.
const (
	pathModCargoToml = `[package]
name = "pathmod"
version = "0.1.0"
edition = "2021"
`

	pathModLibRs = `pub mod nested;
pub mod outer;
pub mod plain;
pub mod prediction;
pub mod vehicle;

#[path = "generated/tables_impl.rs"]
pub mod tables;
`

	// The mounting parent: a module file (not mod.rs), so its #[path]
	// attribute resolves against src/, the directory the file itself sits in.
	pathModPredictionRs = `#[path = "prediction_systems.rs"]
pub mod systems;
`

	pathModPredictionSystemsRs = `pub fn adopt(target: f32, bound: f32) -> f32 {
    if target > bound { target } else { bound }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_adopt_past_the_smoothing_bound_snaps_instead_of_gliding() {
        assert_eq!(adopt(2.0, 1.0), 2.0);
    }
}
`

	// A mount chain: outer mounts inner from a renamed file, and that file
	// mounts leaf from another. The module path of the leaf is the whole
	// chain of MOUNT names, not the chain of file stems.
	pathModOuterRs = `#[path = "outer_inner.rs"]
pub mod inner;
`

	pathModOuterInnerRs = `#[path = "outer_inner_leaf.rs"]
pub mod leaf;
`

	pathModOuterInnerLeafRs = `pub fn leaf_value() -> u32 {
    7
}

#[cfg(test)]
mod tests {
    #[test]
    fn a_leaf_under_two_mounts_still_has_a_module_path() {
        assert_eq!(super::leaf_value(), 7);
    }
}
`

	// A mount whose attribute names a SUBDIRECTORY: the file lives in
	// src/generated/, but the module is `tables` at the crate root — the
	// directory contributes nothing to the module path.
	pathModTablesImplRs = `pub fn row(i: u32) -> u32 {
    i
}

#[cfg(test)]
mod tests {
    #[test]
    fn a_table_row_is_its_own_index() {
        assert_eq!(super::row(3), 3);
    }
}
`

	// The control: ordinary modules, mounted by nothing, in the same crate as
	// the #[path] declarations above. Their filters must stay exactly what
	// they are today.
	pathModPlainRs = `pub fn plain_value() -> u32 {
    1
}

#[cfg(test)]
mod tests {
    #[test]
    fn an_unmounted_module_keeps_its_own_name() {
        assert_eq!(super::plain_value(), 1);
    }
}
`

	pathModNestedModRs = `pub mod deep;
`

	pathModNestedDeepRs = `pub fn deep_value() -> u32 {
    2
}

#[cfg(test)]
mod tests {
    #[test]
    fn a_nested_unmounted_module_keeps_its_path() {
        assert_eq!(super::deep_value(), 2);
    }
}
`

	// Issue #653: vehicle/sim.rs is the mounting PARENT here, and it is
	// itself nested under a subdirectory (vehicle/) rather than sitting at
	// the crate root the way prediction.rs does above. Its mount also names
	// sim_tests.rs under the SAME name as the file's own stem
	// (`mod sim_tests;` mounting sim_tests.rs) — the opposite of the
	// prediction_systems case, which is caught by any scanner that composes
	// the path from the mount NAME, whatever the parent's own path is. This
	// case is not redundant with that one: the reported failure composed the
	// chain from the mounting file's own path and dropped the "sim" segment,
	// giving "vehicle::sim_tests" instead of "vehicle::sim::sim_tests" — a
	// missing PARENT segment, not a wrong name, so a fix that only handles
	// renamed mounts can still get this one wrong.
	pathModVehicleModRs = `pub mod sim;
`

	pathModVehicleSimRs = `pub fn sim_value() -> u32 {
    4
}

// Exactly as reported: #[cfg(test)] precedes #[path] on the mount itself.
#[cfg(test)]
#[path = "sim_tests.rs"]
mod sim_tests;
`

	pathModVehicleSimTestsRs = `#[test]
fn a_sibling_mounted_by_a_nested_mounting_parent_still_composes_the_whole_chain() {
    assert_eq!(super::sim_value(), 4);
}
`
)

// pathModCrate writes the fixture crate and returns its root.
func pathModCrate(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := []struct{ rel, content string }{
		{"Cargo.toml", pathModCargoToml},
		{"src/lib.rs", pathModLibRs},
		{"src/prediction.rs", pathModPredictionRs},
		{"src/prediction_systems.rs", pathModPredictionSystemsRs},
		{"src/outer.rs", pathModOuterRs},
		{"src/outer_inner.rs", pathModOuterInnerRs},
		{"src/outer_inner_leaf.rs", pathModOuterInnerLeafRs},
		{"src/generated/tables_impl.rs", pathModTablesImplRs},
		{"src/plain.rs", pathModPlainRs},
		{"src/nested/mod.rs", pathModNestedModRs},
		{"src/nested/deep.rs", pathModNestedDeepRs},
		{"src/vehicle/mod.rs", pathModVehicleModRs},
		{"src/vehicle/sim.rs", pathModVehicleSimRs},
		{"src/vehicle/sim_tests.rs", pathModVehicleSimTestsRs},
	}
	for _, f := range files {
		write(t, root, filepath.FromSlash(f.rel), f.content)
	}
	return root
}

// hasModuleFilter reports whether args narrow the run to mod's tests, in
// EITHER dialect moduleFilterArgs can emit — nextest's filter expression or
// plain `cargo test`'s substring. Which dialect a fixture gets depends on
// whether the workspace configures nextest, which is not what these tests are
// about: the question here is only which MODULE path the filter names.
func hasModuleFilter(args []string, mod string) bool {
	for i, a := range args {
		if a == mod+"::" {
			return true
		}
		if a == "test(/^"+mod+"::/)" && i > 0 && args[i-1] == "-E" {
			return true
		}
	}
	return false
}

// pathModCases is what the fixture crate above asserts, once: which module
// path each of its files is mounted at. The unit test below judges the filter
// this binary derives against it, and the cargo e2e judges it against the
// module paths rustc itself gives those same files.
var pathModCases = []struct {
	name string
	rel  string
	want string
	// tests is false for a file that carries no #[test] of its own, so the
	// e2e knows not to look for one in the crate's test listing.
	tests bool
}{
	{"the reported shape: mounted under a different name", "src/prediction_systems.rs", "prediction::systems", true},
	{"a mount whose attribute names a subdirectory", "src/generated/tables_impl.rs", "tables", true},
	{"a nested mount composes the whole chain of mount names", "src/outer_inner_leaf.rs", "outer::inner::leaf", true},
	{"a mounting parent keeps its own stem-derived path", "src/prediction.rs", "prediction", false},
	{"an unmounted module keeps its stem-derived path", "src/plain.rs", "plain", true},
	{"an unmounted nested module keeps its stem-derived path", "src/nested/deep.rs", "nested::deep", true},
	{"issue #653: a nested mounting parent's own chain segment is not dropped", "src/vehicle/sim_tests.rs", "vehicle::sim::sim_tests", true},
}

// Issue #637: the per-edit filter is derived from the file's own stem, so a
// file MOUNTED under another name (`#[path = "prediction_systems.rs"] mod
// systems;` inside `prediction`) gets `test(/^prediction_systems::/)` — a
// filter that selects zero of the tests it was built to run. Every post-edit
// run and every `mutants prove` on such a file then tests NOTHING, and a
// mutation proof that ran nothing reports the mutant as a survivor.
func TestNarrow_PathAttributeMountNamesTheModuleTheFileIsMountedAs(t *testing.T) {
	t.Parallel()
	root := pathModCrate(t)
	base := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}

	for _, c := range pathModCases {
		t.Run(c.name, func(t *testing.T) {
			got := NarrowToRelatedTests(base, filepath.Join(root, filepath.FromSlash(c.rel)), root)
			if !hasModuleFilter(got.Args, c.want) {
				t.Fatalf("args = %q, want a filter on %q — %s is mounted as %s",
					strings.Join(got.Args, " "), c.want+"::", c.rel, c.want)
			}
		})
	}
}

// e2eRustcPathModTimeout bounds the real compile below. One rustc invocation
// over a dependency-free crate is a fifth of a second; this is slack for a
// loaded box, not a budget the test is expected to use.
const e2eRustcPathModTimeout = 120 * time.Second

// TestE2E_RustcPathMod_MountsTheModulesTheFilterNames is the ground truth for
// the table above: rustc, not this package, decides what a `#[path]`-mounted
// module is called, so the derivation is judged against a real compiled crate
// rather than argued from string rules.
//
// The compiler is driven DIRECTLY (`rustc --test`, then the test binary's own
// `--list`), not through cargo: the crate has no dependencies, so cargo adds
// only its own resolution and — on a box where cargo is the build queue's
// shim — a wait behind whatever else is building. That wait is what put the
// commit gate's own fail-first budget at risk; rustc reads the `#[path]`
// attributes exactly the same way, in a fifth of a second.
//
// The listing settles both halves of #637: the module path the fix derives is
// the one the tests are really under, and the stem-derived path it replaces
// is under nothing at all (so `-E test(/^prediction_systems::/)` selects zero
// tests).
//
// It skips when rustc is not installed, the same way the other toolchain e2e
// tests do; the toolchain is never faked.
func TestE2E_RustcPathMod_MountsTheModulesTheFilterNames(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("rustc"); err != nil {
		// skip-ok: an environment probe, not a disabled assertion — the test asserts for real wherever rustc is installed.
		t.Skip("rustc not on PATH; skipping the real-compiler e2e")
	}

	root := pathModCrate(t)
	ctx, cancel := context.WithTimeout(context.Background(), e2eRustcPathModTimeout)
	defer cancel()

	// The .exe suffix is unconditional: Windows needs it to execute the file
	// at all, and every other platform is happy to run an executable whatever
	// it is called — one name beats a GOOS switch over a fixture's filename.
	bin := filepath.Join(t.TempDir(), "pathmod_tests.exe")
	build := exec.CommandContext(ctx, "rustc", "--test", "--edition", "2021",
		filepath.Join(root, "src", "lib.rs"), "-o", bin)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("rustc --test over the fixture crate failed (%v):\n%s", err, out)
	}

	list := exec.CommandContext(ctx, bin, "--list")
	out, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("listing the fixture crate's tests failed (%v):\n%s", err, out)
	}

	// Every listed test, by the full module path rustc gave it:
	// "prediction::systems::tests::an_adopt…: test".
	var listed []string
	for _, line := range strings.Split(string(out), "\n") {
		if name, ok := strings.CutSuffix(strings.TrimSpace(line), ": test"); ok {
			listed = append(listed, name)
		}
	}
	if len(listed) == 0 {
		t.Fatalf("the fixture crate's test binary listed no tests at all:\n%s", out)
	}

	underPrefix := func(mod string) bool {
		for _, name := range listed {
			if strings.HasPrefix(name, mod+"::") {
				return true
			}
		}
		return false
	}

	for _, c := range pathModCases {
		if !c.tests {
			continue
		}
		if !underPrefix(c.want) {
			t.Errorf("rustc mounts no test under %q; %s's tests are listed as %v",
				c.want+"::", c.rel, listed)
		}
	}

	// The filter the fix replaces, proved empty against the real crate: this
	// is what every post-edit run and every mutation proof on a mounted file
	// was selecting.
	if underPrefix("prediction_systems") {
		t.Fatalf("a test IS listed under the stem-derived path — the premise of #637 does not hold here: %v", listed)
	}
}

// A crate with no #[path] declaration anywhere must keep answering exactly as
// it does today: the stem-derived path stands.
func TestNarrow_CrateWithoutAnyPathAttributeKeepsTheStemDerivedFilter(t *testing.T) {
	t.Parallel()
	root := cargoCrate(t, "stemonly")
	write(t, root, filepath.FromSlash("src/truss/state_tests.rs"), "#[cfg(test)]\nmod tests {}\n")
	base := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}

	got := NarrowToRelatedTests(base, filepath.Join(root, "src", "truss", "state_tests.rs"), root)
	if !hasModuleFilter(got.Args, "truss::state_tests") {
		t.Fatalf("args = %q, want the stem-derived nested module path", strings.Join(got.Args, " "))
	}
}

// A #[path] attribute pointing at a file that does not exist mounts nothing,
// so no file can be resolved through it — every other file in the crate keeps
// its stem-derived path rather than inheriting a broken declaration's name.
func TestNarrow_DanglingPathAttributeLeavesTheStemDerivedFilterAlone(t *testing.T) {
	t.Parallel()
	root := cargoCrate(t, "dangling")
	write(t, root, filepath.FromSlash("src/lib.rs"), "#[path = \"gone.rs\"]\npub mod ghost;\npub mod real;\n")
	write(t, root, filepath.FromSlash("src/real.rs"), "#[cfg(test)]\nmod tests {}\n")
	base := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}

	got := NarrowToRelatedTests(base, filepath.Join(root, "src", "real.rs"), root)
	if !hasModuleFilter(got.Args, "real") {
		t.Fatalf("args = %q, want the stem-derived filter", strings.Join(got.Args, " "))
	}
}

// A cycle in the mount graph (two files each claiming to mount the other) is
// malformed input, not a reason for the gate to stop answering: the walk must
// terminate, give up on the chain, and hand back the stem-derived path. A
// regression here hangs rather than fails, so the package timeout is the
// backstop.
func TestCargoMountedModulePath_TerminatesOnAMountCycleAndFallsBackToTheStem(t *testing.T) {
	t.Parallel()
	root := cargoCrate(t, "cyclic")
	write(t, root, filepath.FromSlash("src/a.rs"), "#[path = \"b.rs\"]\nmod b;\n")
	write(t, root, filepath.FromSlash("src/b.rs"), "#[path = \"a.rs\"]\nmod a;\n")
	base := Runner{Cmd: "cargo", Args: []string{"nextest", "run"}}

	got := NarrowToRelatedTests(base, filepath.Join(root, "src", "a.rs"), root)
	if !hasModuleFilter(got.Args, "a") {
		t.Fatalf("args = %q, want the stem-derived filter a:: after giving up on the cycle",
			strings.Join(got.Args, " "))
	}
}
