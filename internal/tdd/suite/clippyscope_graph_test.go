package suite

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// clippyScope compiles the touched crates and every crate downstream of them,
// transitively, and nothing they cannot reach. When the graph cannot be read
// it narrows to the touched crates alone and says so in gate.log; it never
// widens to the whole workspace.
func TestClippyScope_TouchedCratesPlusEverythingDownstream(t *testing.T) {
	graph := map[string][]string{
		"leaf": nil, "mid": {"leaf"}, "top": {"mid"}, "wide": {"leaf", "mid"}, "aside": nil,
	}
	cases := []struct {
		name    string
		touched []string
		want    []string
	}{
		{"a leaf reaches every dependent", []string{"leaf"}, []string{"leaf", "mid", "top", "wide"}},
		{"a crate nothing depends on is alone", []string{"aside"}, []string{"aside"}},
		{"duplicates collapse", []string{"top", "mid", "top"}, []string{"mid", "top", "wide"}},
		{"nothing touched is no scope", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(SetCargoWorkspaceDepsForTest(func(string) (map[string][]string, error) { return graph, nil }))

			got := clippyScope("g", t.TempDir(), t.TempDir(), tc.touched)

			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("clippyScope(%v) = %v, want %v", tc.touched, got, tc.want)
			}
		})
	}
}

func TestClippyScope_AnUnreadableGraphNarrowsToTheTouchedCratesAndLogsIt(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Cleanup(SetCargoWorkspaceDepsForTest(func(string) (map[string][]string, error) { return nil, errors.New("boom") }))

	got := clippyScope("precommit", t.TempDir(), t.TempDir(), []string{"top", "leaf"})

	if strings.Join(got, ",") != "leaf,top" {
		t.Fatalf("clippyScope with no graph = %v, want the touched crates only", got)
	}
	requireLoggedVerdict(t, cfg, "clippy-scope-degraded:boom")
}

// The graph keeps only edges between workspace members, sorted and without
// duplicates or a package's edge to itself: a registry dependency is not a
// crate the gate can select with -p.
func TestParseWorkspaceDeps_GraphIsSortedDedupedAndMemberOnly(t *testing.T) {
	const doc = `{"packages":[
	  {"name":"forge","dependencies":[{"name":"serde"},{"name":"forge_math"},{"name":"forge_io"},{"name":"forge_math"},{"name":"forge"}]},
	  {"name":"forge_math","dependencies":[{"name":"libm"}]},
	  {"name":"forge_io","dependencies":[]}
	]}`

	got, err := parseWorkspaceDeps([]byte(doc))
	if err != nil {
		t.Fatalf("parseWorkspaceDeps: %v", err)
	}

	want := map[string][]string{"forge": {"forge_io", "forge_math"}, "forge_math": nil, "forge_io": nil}
	for pkg, deps := range want {
		if strings.Join(got[pkg], ",") != strings.Join(deps, ",") {
			t.Errorf("%s depends on %v, want %v", pkg, got[pkg], deps)
		}
	}
	if len(got) != len(want) {
		t.Errorf("graph has %d packages, want %d: %v", len(got), len(want), got)
	}
	if _, err := parseWorkspaceDeps([]byte("not json")); err == nil {
		t.Error("unparsable metadata read as an empty graph, want an error")
	}
}
