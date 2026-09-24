package precommit

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

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
	// The always-run declaration is the workspace's own standing policy, not
	// part of the commit under test: committed on its own first (Cargo.toml
	// is Source since #278, so leaving it staged alongside the touched crate
	// would open a SECOND rootGroup for the manifest and race its own
	// always-run invocation against this one over the shared mech-cache key).
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\", \"crates/beta\"]\n\n"+
		"[workspace.metadata.aphrollo]\nalways-run = [\"beta\"]\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "declare always-run")
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
	// Same reason as the sibling test above: the always-run declaration is
	// committed on its own, so only the touched crate is staged.
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\", \"crates/beta\"]\n\n"+
		"[workspace.metadata.aphrollo]\nalways-run = [\"beta\"]\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "declare always-run")
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

// TestNarrowToRelatedTests_CargoTestsDirUnderSrcIsAModuleNotATarget pins
// issue #580: a directory named tests/ BELOW src/ is a unit-test module
// compiled into the lib target (`mod tests;`), never the crate's
// integration-test dir. borld's src/tire_rig/tests/vertical_ladder.rs was
// mapped to `--test vertical_ladder` and every edit printed
// `error: no test target named 'vertical_ladder'` -- a red on green code.
// The file must run on the lib target filtered by its module path, and a
// mod.rs there filters to the directory's own module.
func TestNarrowToRelatedTests_CargoTestsDirUnderSrcIsAModuleNotATarget(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"forge\"\nversion = \"0.1.0\"\n")
	write(t, root, "src/tire_rig/tests/vertical_ladder.rs", "#[test]\nfn climbs() {}\n")
	write(t, root, "src/tire_rig/tests/mod.rs", "mod vertical_ladder;\n")
	stubCargoTestTargets(t, map[string]map[string]bool{
		"forge": {}, // no integration-test targets at all
	})

	cargo := Runner{Cmd: "cargo", Args: []string{"test"}, Dir: "", Deadline: time.Time{}}
	cases := []struct {
		file string
		want Runner
	}{
		{
			file: filepath.Join(root, "src", "tire_rig", "tests", "vertical_ladder.rs"),
			want: Runner{Cmd: "cargo", Args: []string{"test", "-p", "forge", "--lib", "tire_rig::tests::vertical_ladder::"}, Dir: root, Deadline: time.Time{}},
		},
		{
			file: filepath.Join(root, "src", "tire_rig", "tests", "mod.rs"),
			want: Runner{Cmd: "cargo", Args: []string{"test", "-p", "forge", "--lib", "tire_rig::tests::"}, Dir: root, Deadline: time.Time{}},
		},
	}
	for _, c := range cases {
		t.Run(filepath.Base(c.file), func(t *testing.T) {
			if got := NarrowToRelatedTests(cargo, c.file, root); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("NarrowToRelatedTests = %+v, want %+v (never --test on a src/ module)", got, c.want)
			}
		})
	}
}
