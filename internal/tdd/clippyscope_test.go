package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// The workspace check ran `cargo clippy --workspace --tests` on every commit
// -- 281.9s measured, most of it compiling crates the change cannot reach. It
// is scoped instead to what the change can actually break: the touched crates
// and the clippy-clean crates downstream of them.

// stubWorkspaceGraph states a workspace's intra-workspace dependency edges
// without a cargo run: pkg -> the packages it depends on.
func stubWorkspaceGraph(t *testing.T, graph map[string][]string) {
	t.Helper()
	prev := cargoWorkspaceDepsFn
	cargoWorkspaceDepsFn = func(string) map[string][]string { return graph }
	t.Cleanup(func() { cargoWorkspaceDepsFn = prev })
}

// clippyCleanWorkspace writes a workspace manifest declaring the given
// clippy-clean list.
func clippyCleanWorkspace(t *testing.T, ws string, clean ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("[workspace]\nmembers = [\"crates/*\"]\n\n[workspace.metadata.aphrollo]\nclippy-clean = [")
	for i, c := range clean {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("\"" + c + "\"")
	}
	b.WriteString("]\n")
	mustWrite(t, filepath.Join(ws, "Cargo.toml"), b.String())
}

func TestClippyScope_TakesTheTouchedCrateAndItsClippyCleanDependents(t *testing.T) {
	ws := t.TempDir()
	// leaf <- mid <- top, and `aside` depends on nothing that moved.
	stubWorkspaceGraph(t, map[string][]string{
		"leaf":  nil,
		"mid":   {"leaf"},
		"top":   {"mid"},
		"aside": nil,
	})
	clippyCleanWorkspace(t, ws, "top", "aside")

	got := clippyScope(ws, []string{"leaf"})
	if strings.Join(got, ",") != "leaf,top" {
		t.Fatalf("scope = %v, want the touched crate plus its clippy-clean dependents only", got)
	}
}

func TestClippyScope_LeavesOutADependentThatIsNotClippyClean(t *testing.T) {
	ws := t.TempDir()
	stubWorkspaceGraph(t, map[string][]string{"leaf": nil, "mid": {"leaf"}})
	clippyCleanWorkspace(t, ws) // nothing declared clean

	got := clippyScope(ws, []string{"leaf"})
	if strings.Join(got, ",") != "leaf" {
		t.Fatalf("scope = %v, want only the touched crate", got)
	}
}

func TestClippyScope_ReadsTheGraphTransitively(t *testing.T) {
	ws := t.TempDir()
	stubWorkspaceGraph(t, map[string][]string{"leaf": nil, "mid": {"leaf"}, "top": {"mid"}})
	clippyCleanWorkspace(t, ws, "mid", "top")

	got := clippyScope(ws, []string{"leaf"})
	if strings.Join(got, ",") != "leaf,mid,top" {
		t.Fatalf("scope = %v, want every clippy-clean crate downstream of the change", got)
	}
}

func TestClippyScope_FallsBackToTheTouchedCratesWhenTheGraphIsUnreadable(t *testing.T) {
	ws := t.TempDir()
	stubWorkspaceGraph(t, nil) // cargo metadata unavailable
	clippyCleanWorkspace(t, ws, "top")

	got := clippyScope(ws, []string{"leaf"})
	if strings.Join(got, ",") != "leaf" {
		t.Fatalf("scope = %v, want the touched crates — never the whole workspace", got)
	}
}

// The parser answers the real `cargo metadata --no-deps` document shape, and
// keeps only intra-workspace edges: a registry dependency is not a crate this
// gate can select with -p.
func TestParseWorkspaceDeps_KeepsOnlyIntraWorkspaceEdges(t *testing.T) {
	const doc = `{"packages":[
	  {"name":"forge","dependencies":[{"name":"forge_math"},{"name":"serde"}]},
	  {"name":"forge_math","dependencies":[{"name":"libm"}]}
	],"workspace_root":"/w"}`

	got := parseWorkspaceDeps([]byte(doc))
	if strings.Join(got["forge"], ",") != "forge_math" {
		t.Fatalf("forge deps = %v, want the workspace member only", got["forge"])
	}
	if len(got["forge_math"]) != 0 {
		t.Fatalf("forge_math deps = %v, want none — libm is not a workspace member", got["forge_math"])
	}
}

// The stage line has to name the crates, or a reader cannot tell a scoped run
// from a whole-workspace one that silently stopped covering something.
func TestWorkspaceStage_RunsScopedClippyAndNamesTheCrates(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	stubWorkspaceGraph(t, map[string][]string{"m": nil})
	write(t, root, "src/lib.rs", "pub fn one() -> i32 { 2 }\n")
	gitDo(t, root, "add", ".")

	var clippyArgs string
	Precommit(root, func(r Runner, _ string) SuiteResult {
		if args := strings.Join(r.Args, " "); strings.HasPrefix(args, "clippy") {
			clippyArgs = args
		}
		return SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed"}
	})

	if clippyArgs == "" {
		t.Fatal("the check stage did not run")
	}
	if strings.Contains(clippyArgs, "--workspace") {
		t.Fatalf("check stage = %q, want it scoped with -p, never --workspace", clippyArgs)
	}
	if !strings.Contains(clippyArgs, "-p m") {
		t.Fatalf("check stage = %q, want the touched crate selected with -p", clippyArgs)
	}
}
