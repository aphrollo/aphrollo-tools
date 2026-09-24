package precommit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The workspace check ran `cargo clippy --workspace --tests` on every commit
// -- 281.9s measured, most of it compiling crates the change cannot reach. It
// is scoped instead to what the change can actually break: the touched crates
// and every crate downstream of them.

// stubWorkspaceGraph states a workspace's intra-workspace dependency edges
// without a cargo run: pkg -> the packages it depends on.
func stubWorkspaceGraph(t *testing.T, graph map[string][]string) {
	t.Helper()
	t.Cleanup(SetCargoWorkspaceDepsForTest(func(string) (map[string][]string, error) { return graph, nil }))
}

// stubWorkspaceGraphError states that the graph read itself failed — cargo
// missing, a broken manifest, unparsable output — distinct from a workspace
// that legitimately has no edges.
func stubWorkspaceGraphError(t *testing.T, err error) {
	t.Helper()
	t.Cleanup(SetCargoWorkspaceDepsForTest(func(string) (map[string][]string, error) { return nil, err }))
}

// clippyCleanWorkspace writes a workspace manifest declaring the given
// clippy-clean list. The check stage must not read it: that list gates the
// separate warning-free stage, not this one.
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

func TestClippyScope_TakesTheTouchedCrateAndEveryCrateDownstreamOfIt(t *testing.T) {
	ws := t.TempDir()
	// leaf <- mid <- top, and `aside` depends on nothing that moved.
	stubWorkspaceGraph(t, map[string][]string{
		"leaf":  nil,
		"mid":   {"leaf"},
		"top":   {"mid"},
		"aside": nil,
	})
	clippyCleanWorkspace(t, ws, "top", "aside")

	got := clippyScope("g", "r", ws, []string{"leaf"})
	if strings.Join(got, ",") != "leaf,mid,top" {
		t.Fatalf("scope = %v, want the touched crate plus its dependents, and nothing it cannot reach", got)
	}
}

// The two lints this stage carries are laws every crate owes, and the crate
// most likely to break under them is the one nobody has made warning-free. A
// clippy-clean filter here would drop exactly that crate.
func TestClippyScope_KeepsADependentThatIsNotClippyClean(t *testing.T) {
	ws := t.TempDir()
	stubWorkspaceGraph(t, map[string][]string{"leaf": nil, "mid": {"leaf"}})
	clippyCleanWorkspace(t, ws) // nothing declared clean

	got := clippyScope("g", "r", ws, []string{"leaf"})
	if strings.Join(got, ",") != "leaf,mid" {
		t.Fatalf("scope = %v, want the dependent compiled even though it is not clippy-clean", got)
	}
}

func TestClippyScope_ReadsTheGraphTransitively(t *testing.T) {
	ws := t.TempDir()
	stubWorkspaceGraph(t, map[string][]string{"leaf": nil, "mid": {"leaf"}, "top": {"mid"}})
	clippyCleanWorkspace(t, ws)

	got := clippyScope("g", "r", ws, []string{"leaf"})
	if strings.Join(got, ",") != "leaf,mid,top" {
		t.Fatalf("scope = %v, want every crate downstream of the change", got)
	}
}

func TestClippyScope_FallsBackToTheTouchedCratesWhenTheGraphIsUnreadable(t *testing.T) {
	ws := t.TempDir()
	stubWorkspaceGraphError(t, errors.New("exec: \"cargo\": executable file not found in $PATH"))
	clippyCleanWorkspace(t, ws, "top")

	got := clippyScope("g", "r", ws, []string{"leaf"})
	if strings.Join(got, ",") != "leaf" {
		t.Fatalf("scope = %v, want the touched crates — never the whole workspace", got)
	}
}

// A graph read failure must not pass through in silence: the crates
// downstream of the change are exactly the ones this stage exists to
// compile, and a commit that skips them has to be able to tell.
func TestClippyScope_LogsAndPrintsWhenTheGraphReadFails(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	ws := t.TempDir()
	stubWorkspaceGraphError(t, errors.New("boom"))
	clippyCleanWorkspace(t, ws, "top")

	stderr := captureStderr(t, func() {
		clippyScope("mygate", ws, ws, []string{"leaf"})
	})
	if !strings.Contains(stderr, "mygate") || !strings.Contains(stderr, "boom") {
		t.Fatalf("stderr = %q, want it to name the gate and the read error", stderr)
	}
	requireLoggedVerdict(t, cfg, "clippy-scope-degraded:boom")
}

// The two quiet cases — no workspace to ask, and a workspace that genuinely
// has no intra-workspace edges — must stay silent: neither is a defect, and
// logging them would bury the one case that is.
func TestClippyScope_StaysSilentWhenTheGraphHasNoEdges(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	ws := t.TempDir()
	stubWorkspaceGraph(t, nil) // a real, error-free answer: no edges
	clippyCleanWorkspace(t, ws, "top")

	stderr := captureStderr(t, func() {
		clippyScope("mygate", ws, ws, []string{"leaf"})
	})
	if strings.Contains(stderr, "degraded") || strings.Contains(stderr, "unreadable") {
		t.Fatalf("stderr = %q, want no degradation notice for a genuinely empty graph", stderr)
	}
	if _, err := os.Stat(filepath.Join(cfg, "gate-state", "gate.log")); err == nil {
		t.Fatalf("gate.log written for a genuinely empty graph:\n%s", gateLogText(t, cfg))
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

	got, err := parseWorkspaceDeps([]byte(doc))
	if err != nil {
		t.Fatalf("parseWorkspaceDeps returned an error on well-formed input: %v", err)
	}
	if strings.Join(got["forge"], ",") != "forge_math" {
		t.Fatalf("forge deps = %v, want the workspace member only", got["forge"])
	}
	if len(got["forge_math"]) != 0 {
		t.Fatalf("forge_math deps = %v, want none — libm is not a workspace member", got["forge_math"])
	}
}

// Malformed metadata output must surface as an error, not a silent empty
// graph — the two look identical to a caller that only checks len(map).
func TestParseWorkspaceDeps_ErrorsOnUnparsableJSON(t *testing.T) {
	_, err := parseWorkspaceDeps([]byte("not json"))
	if err == nil {
		t.Fatal("parseWorkspaceDeps returned nil error for unparsable input")
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
