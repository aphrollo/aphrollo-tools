package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

func nextestWorkspace(t *testing.T, config string) string {
	t.Helper()
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, ".config", "nextest.toml"), config)
	return ws
}

func TestHasGateProfileReadsTheTableHeader(t *testing.T) {
	cases := map[string]struct {
		config string
		want   bool
	}{
		"declared":           {"[profile.default]\nslow-timeout = \"60s\"\n\n[profile.gate]\nslow-timeout = { period = \"120s\" }\n", true},
		"declared first":     {"[profile.gate]\nfail-fast = false\n", true},
		"indented":           {"  [profile.gate]  \n", true},
		"absent":             {"[profile.default]\nfail-fast = false\n", false},
		"another profile":    {"[profile.gateway]\nfail-fast = false\n", false},
		"named in a value":   {"[profile.default]\nfinal-status-level = \"[profile.gate]\"\n", false},
		"mentioned in prose": {"# see [profile.gate] below — not added yet\n[profile.default]\n", false},
	}
	for name, c := range cases {
		if got := hasGateProfile(nextestWorkspace(t, c.config)); got != c.want {
			t.Errorf("%s: hasGateProfile = %v, want %v", name, got, c.want)
		}
	}
	if hasGateProfile(t.TempDir()) {
		t.Error("no config at all means no profile")
	}
}

func TestWithGateProfileAddsTheFlagOnlyToANextestRun(t *testing.T) {
	ws := nextestWorkspace(t, "[profile.gate]\nslow-timeout = { period = \"120s\" }\n")

	got := withGateProfile(Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "solver"}, Dir: ws}, ws)
	want := "nextest run --profile gate -p solver"
	if strings.Join(got.Args, " ") != want {
		t.Errorf("args = %v, want %q", got.Args, want)
	}

	plain := withGateProfile(Runner{Cmd: "cargo", Args: []string{"test", "-p", "solver"}, Dir: ws}, ws)
	if strings.Join(plain.Args, " ") != "test -p solver" {
		t.Errorf("`cargo test` has no profiles: %v", plain.Args)
	}

	bare := nextestWorkspace(t, "[profile.default]\n")
	unprofiled := withGateProfile(Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "solver"}, Dir: bare}, bare)
	if strings.Join(unprofiled.Args, " ") != "nextest run -p solver" {
		t.Errorf("a workspace that declares no gate profile must be untouched: %v", unprofiled.Args)
	}
}

// The gate's own stages take the profile; the edit hook does not — an
// edit-time run that quietly waited longer would hide the slow test instead of
// reporting it.
func TestGateStageRunnersCarryTheProfileAndPostEditDoesNot(t *testing.T) {
	ws := nextestWorkspace(t, "[profile.gate]\nslow-timeout = { period = \"120s\" }\n")
	mustWrite(t, filepath.Join(ws, "Cargo.toml"), "[workspace]\nmembers = [\"solver\"]\n")
	mustWrite(t, filepath.Join(ws, "solver", "Cargo.toml"), "[package]\nname = \"solver\"\nversion = \"0.1.0\"\n")
	mustWrite(t, filepath.Join(ws, "solver", "src", "lib.rs"), "pub fn f() {}\n")
	if !nextestInstalled() {
		t.Skip("cargo-nextest is not installed on this box")
	}

	plan := cargoStagePlan{ws: ws, touched: []string{"solver"}, alwaysRun: []string{"ratchet"}}
	for name, r := range map[string]Runner{"suite": plan.suiteRunner(), "always-run": plan.guardRunner()} {
		if !strings.Contains(strings.Join(r.Args, " "), "--profile gate") {
			t.Errorf("%s runner = %v, want the gate profile", name, r.Args)
		}
	}

	edit, ok := cargoRunnerAt(filepath.Join(ws, "solver"), "--lib")
	if !ok {
		t.Fatal("cargoRunnerAt could not resolve the member package")
	}
	if strings.Contains(strings.Join(edit.Args, " "), "--profile") {
		t.Errorf("the edit-time runner must keep the default profile: %v", edit.Args)
	}
}
