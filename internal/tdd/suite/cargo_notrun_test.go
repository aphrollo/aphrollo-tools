package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cargoIntegrationTargetsNotRun names what a run left out, so each case is
// one argv against one crate with two integration targets, integration and
// soak, and the literal clause names that argv's run never built.
func TestCargoIntegrationTargetsNotRun_NamesOnlyTheTargetsTheRunLeftOut(t *testing.T) {
	cases := []struct {
		name, argv, want string
	}{
		{"scoped lib run", "nextest run -p forge_solver --lib -E test(/^spin::/)", "--test integration, --test soak"},
		{"unfiltered lib run", "test -p forge_solver --lib", "--test integration, --test soak"},
		{"lib run naming one target", "nextest run -p forge_solver --lib --test integration", "--test soak"},
		{"lib run naming one target inline", "nextest run --package=forge_solver --lib --test=soak", "--test integration"},
		{"lib run naming both targets", "nextest run -p forge_solver --lib --test integration --test soak", ""},
		{"whole-package run", "nextest run -p forge_solver", ""},
		{"one integration target", "nextest run -p forge_solver --test integration", ""},
		{"every test target", "nextest run -p forge_solver --lib --tests", ""},
		{"two packages", "nextest run -p forge_solver -p forge_lab --lib", ""},
		{"no package", "nextest run --lib", ""},
		// A trailing --test has no value to consume: it names no target, and
		// reading past the end of argv for one would crash the edit hook.
		{"trailing --test with no value", "test -p forge_solver --lib --test", "--test integration, --test soak"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			restore := SetCargoTestTargetsForTest(func(string) map[string]map[string]bool {
				return map[string]map[string]bool{
					"forge_solver": {"integration": true, "soak": true},
					"forge_lab":    {"lab": true},
				}
			})
			defer restore()
			r := Runner{Cmd: "cargo", Args: strings.Fields(tc.argv), Dir: ws}

			got := strings.Join(cargoIntegrationTargetsNotRun(r, ws), ", ")

			if got != tc.want {
				t.Fatalf("%s: not run = %q, want %q", tc.argv, got, tc.want)
			}
		})
	}
}

// A run with no directory of its own reads the workspace the crate root
// resolves to, which is where cargoRunnerAt would have run it.
func TestCargoIntegrationTargetsNotRun_RunWithoutADirReadsTheRootsWorkspace(t *testing.T) {
	ws := t.TempDir()
	root := filepath.Join(ws, "crates", "forge_solver")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "Cargo.toml"), []byte("[workspace]\nmembers = [\"crates/forge_solver\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var asked string
	restore := SetCargoTestTargetsForTest(func(ws string) map[string]map[string]bool {
		asked = ws
		return map[string]map[string]bool{"forge_solver": {"integration": true}}
	})
	defer restore()
	r := Runner{Cmd: "cargo", Args: strings.Fields("test -p forge_solver --lib")}

	got := cargoIntegrationTargetsNotRun(r, root)

	if strings.Join(got, ", ") != "--test integration" || asked != ws {
		t.Fatalf("not run = %v read from %q, want [--test integration] read from the workspace %q", got, asked, ws)
	}
}

// Only cargo has integration targets: a go run over the same words names
// nothing.
func TestCargoIntegrationTargetsNotRun_NonCargoRunNamesNothing(t *testing.T) {
	ws := t.TempDir()
	restore := SetCargoTestTargetsForTest(func(string) map[string]map[string]bool {
		return map[string]map[string]bool{"forge_solver": {"integration": true}}
	})
	defer restore()
	r := Runner{Cmd: "go", Args: strings.Fields("test -p forge_solver --lib"), Dir: ws}

	if got := cargoIntegrationTargetsNotRun(r, ws); len(got) != 0 {
		t.Fatalf("a go run named cargo targets as not run: %v", got)
	}
}
