package postedit

import (
	"strings"
	"testing"
)

// Issue #830: a Write of scratch_edit.py at the root of a virtual cargo
// workspace (a Cargo.toml with [workspace] and no [package]) queued a
// full-tree `cargo nextest run --no-run` for the whole workspace target. The
// file sits in no crate and no build reads it, so there is nothing to build.

// virtualWorkspace is a cargo workspace whose root manifest declares members
// only, with one member crate carrying a lib.
func virtualWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/forge_solver\"]\nresolver = \"2\"\n")
	write(t, root, "crates/forge_solver/Cargo.toml", "[package]\nname = \"forge_solver\"\nversion = \"0.1.0\"\n")
	write(t, root, "crates/forge_solver/src/lib.rs", "pub fn cost() -> u32 { 1 }\n")
	return root
}

// recordEditRuns is a SuiteRunner that remembers every command it was handed.
func recordEditRuns(ran *[]string) SuiteRunner {
	return func(r Runner, _ string) SuiteResult {
		*ran = append(*ran, cmdString(r))
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out\n"}
	}
}

func TestPostEdit_RootScriptInVirtualCargoWorkspace_RunsNoBuild(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := virtualWorkspace(t)
	write(t, root, "scratch_edit.py", "print('probe')\n")

	var ran []string
	got := PostEdit(postPayload("Write", root+"/scratch_edit.py"), recordEditRuns(&ran))

	if len(ran) != 0 {
		t.Fatalf("a file in no crate must run nothing, ran %v (%q)", ran, got)
	}
	for _, want := range []string{"skipped", "scratch_edit.py", "no crate"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the gate line must say it skipped and why (%q), got: %q", want, got)
		}
	}
	if logged := gateLogText(t, cfg); !strings.Contains(logged, "skipped-unowned") {
		t.Fatalf("gate.log must record the skip, got:\n%s", logged)
	}
}

func TestPostEdit_RootScriptInVirtualCargoWorkspace_DeferredPathSpawnsNoBuild(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := virtualWorkspace(t)
	write(t, root, "scratch_edit.py", "print('probe')\n")
	spawned := fakePhases(t, &PhaseOutcome{ExitCode: 0, Seconds: 1}, &PhaseOutcome{ExitCode: 0, Seconds: 1})

	got := PostEdit(postPayload("Write", root+"/scratch_edit.py"), fakeRun(true, "ok"))

	if len(*spawned) != 0 {
		t.Fatalf("a file in no crate must spawn no deferred phase, spawned %+v (%q)", *spawned, got)
	}
	if !strings.Contains(got, "skipped") {
		t.Fatalf("want the skip line, got: %q", got)
	}
}

// The root manifest is also in no crate, but it is a build input: a member
// list or dependency edit reshapes every crate, so it keeps its run.
func TestPostEdit_RootManifestInVirtualCargoWorkspace_StillRuns(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := virtualWorkspace(t)

	var ran []string
	got := PostEdit(postPayload("Edit", root+"/Cargo.toml"), recordEditRuns(&ran))

	if len(ran) != 1 {
		t.Fatalf("the workspace manifest must keep its run, ran %v (%q)", ran, got)
	}
}

// build.rs is compiled by name wherever it sits, so it is a build input and
// keeps its run even where no [package] claims it.
func TestPostEdit_BuildScriptAtVirtualWorkspaceRoot_StillRuns(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := virtualWorkspace(t)
	write(t, root, "build.rs", "fn main() {}\n")

	var ran []string
	got := PostEdit(postPayload("Write", root+"/build.rs"), recordEditRuns(&ran))

	if len(ran) != 1 {
		t.Fatalf("build.rs must keep its run, ran %v (%q)", ran, got)
	}
}

// A script inside a crate belongs to that crate and keeps its crate's run.
func TestPostEdit_ScriptInsideACrate_StillRuns(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := virtualWorkspace(t)
	write(t, root, "crates/forge_solver/scripts/gen.py", "print('gen')\n")

	var ran []string
	got := PostEdit(postPayload("Write", root+"/crates/forge_solver/scripts/gen.py"), recordEditRuns(&ran))

	if len(ran) != 1 {
		t.Fatalf("a file a crate owns must keep its run, ran %v (%q)", ran, got)
	}
}

// A Bash command judges one file per root. When the first changed path in a
// root is a script no crate owns, the build input beside it in the same root
// must still get the run: skipping the script cannot use up the root's turn.
func TestPostBash_ScriptInNoCrateDoesNotUseUpItsRootsRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := virtualWorkspace(t)
	gitInit(t, root)
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "base")
	cmd := "python3 gen.py"

	PreBash(bashPayload(t, "s830", root, cmd))
	write(t, root, "Probe.py", "print('probe')\n")
	write(t, root, "aphrollo.toml", "[aphrollo]\nundercover = true\n")

	var dirs []string
	text := PostBash(bashPayload(t, "s830", root, cmd), recordSuiteDirs(&dirs))

	if len(dirs) != 1 {
		t.Fatalf("aphrollo.toml is a build input in the same root and must still run, ran in %v (%q)", dirs, text)
	}
}

// A root manifest carrying both [package] and [workspace] makes the root a
// crate: a script beside it sits inside that crate and keeps the crate's run.
func TestPostEdit_ScriptAtRootOfAWorkspaceThatIsAlsoACrate_StillRuns(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"forge\"\nversion = \"0.1.0\"\n\n[workspace]\nmembers = [\"crates/forge_solver\"]\n")
	write(t, root, "src/lib.rs", "pub fn forge() {}\n")
	write(t, root, "scratch_edit.py", "print('probe')\n")

	var ran []string
	got := PostEdit(postPayload("Write", root+"/scratch_edit.py"), recordEditRuns(&ran))

	if len(ran) != 1 {
		t.Fatalf("a file the root crate owns must keep its run, ran %v (%q)", ran, got)
	}
}

// The Go analogue: a Go module whose root holds no .go files narrowed a root
// .py to the whole `go test ./...`.
func TestPostEdit_RootScriptInGoModuleWithNoRootPackage_RunsNoBuild(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	write(t, root, "internal/a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	write(t, root, "scratch_edit.py", "print('probe')\n")

	var ran []string
	got := PostEdit(postPayload("Write", root+"/scratch_edit.py"), recordEditRuns(&ran))

	if len(ran) != 0 {
		t.Fatalf("a file in no Go package must run nothing, ran %v (%q)", ran, got)
	}
	if !strings.Contains(got, "no Go package") {
		t.Fatalf("want the skip line naming why, got: %q", got)
	}
}

// go.mod sits in the same package-less root and is a build input: it keeps
// the module-wide run.
func TestPostEdit_GoModInModuleWithNoRootPackage_StillRuns(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	write(t, root, "internal/a/a.go", "package a\n\nfunc A() int { return 1 }\n")

	var ran []string
	got := PostEdit(postPayload("Edit", root+"/go.mod"), recordEditRuns(&ran))

	if len(ran) != 1 {
		t.Fatalf("go.mod must keep its run, ran %v (%q)", ran, got)
	}
}
