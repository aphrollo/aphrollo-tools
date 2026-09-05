package tdd

import (
	"reflect"
	"testing"
	"time"
)

// A workspace-root Cargo.toml owns no [package], so ownership scoping used to
// exclude it silently: the commit ran no build and no suite at all (issue
// #365). It must instead answer with the cheap workspace-wide compile check
// the issue names, since the manifest owns no single crate to scope a
// per-package suite to.
func TestPrecommit_WorkspaceManifestOnlyChange_RunsCargoCheckWorkspace(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoWorkspaceRepo(t)
	write(t, root, "Cargo.toml",
		"[workspace]\nmembers = [\"crates/alpha\", \"crates/beta\"]\n\n[profile.release]\nopt-level = 3\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Precommit(root, func(r Runner, dir string) SuiteResult {
		r.Deadline = time.Time{}
		seen = append(seen, r)
		return SuiteResult{Passed: true}
	})
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}

	want := Runner{Cmd: "cargo", Args: []string{"check", "--workspace", "--tests"}, Dir: root}
	found := false
	for _, r := range seen {
		if reflect.DeepEqual(r, want) {
			found = true
		}
		if r.Cmd == "cargo" && len(r.Args) > 0 && (r.Args[0] == "test" || r.Args[0] == "nextest") {
			t.Fatalf("a manifest-only change must never fall back to an unscoped full-workspace suite, got %+v", r)
		}
	}
	if !found {
		t.Fatalf("runs = %+v, want a %+v run for the workspace manifest change", seen, want)
	}
}

// A commit touching the workspace manifest AND a member crate's source must
// get BOTH: the workspace-wide check for the manifest, and the ordinary
// ownership-scoped suite for the crate actually touched.
func TestPrecommit_WorkspaceManifestChangeAlongsideTouchedCrate_RunsBoth(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoWorkspaceRepo(t)
	write(t, root, "Cargo.toml",
		"[workspace]\nmembers = [\"crates/alpha\", \"crates/beta\"]\n\n[profile.release]\nopt-level = 3\n")
	write(t, root, "crates/alpha/src/lib.rs", "pub fn alpha() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	// isQualityRunner treats `cargo check` (the fmt/clippy stages' own probe
	// shape) as a quality run to filter out of most scoping tests; this test
	// wants the workspace-wide check counted, so it records everything
	// itself rather than reusing that filter.
	var seen []Runner
	res := Precommit(root, func(r Runner, dir string) SuiteResult {
		r.Deadline = time.Time{}
		seen = append(seen, r)
		return SuiteResult{Passed: true}
	})
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}

	wantCheck := Runner{Cmd: "cargo", Args: []string{"check", "--workspace", "--tests"}, Dir: root}
	wantSuite := Runner{Cmd: "cargo", Args: []string{"test", "-p", "alpha"}, Dir: root}
	var gotCheck, gotSuite bool
	for _, r := range seen {
		if reflect.DeepEqual(r, wantCheck) {
			gotCheck = true
		}
		if reflect.DeepEqual(r, wantSuite) {
			gotSuite = true
		}
	}
	if !gotCheck {
		t.Fatalf("runs = %+v, want the workspace check %+v", seen, wantCheck)
	}
	if !gotSuite {
		t.Fatalf("runs = %+v, want the touched crate's own suite %+v", seen, wantSuite)
	}
}

// A failing workspace-wide check must block the commit exactly like any
// other suite stage — the whole point of running it is to catch a manifest
// edit that breaks the build.
func TestPrecommit_WorkspaceManifestCheckFailure_Blocks(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoWorkspaceRepo(t)
	write(t, root, "Cargo.toml",
		"[workspace]\nmembers = [\"crates/alpha\", \"crates/beta\"]\n\nresolver = \"3\"\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, func(r Runner, dir string) SuiteResult {
		return SuiteResult{Passed: false, Output: "error: failed to parse manifest"}
	})
	if !res.Blocked {
		t.Fatal("a failing workspace check must block the commit, got unblocked")
	}
}
