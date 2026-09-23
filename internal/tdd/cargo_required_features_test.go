package tdd

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #755: an example that declares `required-features` in its manifest
// cannot be built without them — cargo refuses the target outright ("target
// `elem_tire_rig` in package `forge` requires the features: ...") — and the
// post-edit compile check built it bare, so every edit to that example,
// a doc comment included, reported a red the code did not earn.

// requiredFeaturesCrate is the field shape: a `forge` crate whose example
// needs two features to build at all.
func requiredFeaturesCrate(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"forge\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n"+
		"[features]\ndebug-render = []\nfree-camera = []\n\n"+
		"[[example]]\nname = \"elem_tire_rig\"\nrequired-features = [\"debug-render\", \"free-camera\"]\n")
	write(t, root, filepath.Join("src", "lib.rs"), "pub fn rig() -> u32 { 4 }\n")
	write(t, root, filepath.Join("examples", "elem_tire_rig.rs"), "//! Drives the tire rig.\nfn main() {\n    let _ = forge::rig();\n}\n")
	write(t, root, filepath.Join("examples", "plain.rs"), "fn main() {}\n")
	return root
}

// The compile check the edit hook builds for that example must build it: run
// with the real toolchain, it compiles clean.
func TestNarrowToRelatedTests_AnExampleIsBuiltWithItsRequiredFeatures(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		// skip-ok: an environment probe, not a disabled assertion — the test asserts for real wherever cargo is installed.
		t.Skip("cargo not on PATH; skipping the real-toolchain case")
	}
	root := requiredFeaturesCrate(t)
	t.Setenv("CARGO_TARGET_DIR", t.TempDir())

	r := NarrowToRelatedTests(Runner{Cmd: "cargo", Args: []string{"test"}}, filepath.Join(root, "examples", "elem_tire_rig.rs"), root)
	res := RunSuite(2*time.Minute)(r, root)

	if !res.Passed {
		t.Fatalf("%s did not build the example:\n%s", cmdString(r), res.Output)
	}
}

// An example that declares no required features is built exactly as before,
// with no --features at all.
func TestNarrowToRelatedTests_AnExampleWithoutRequiredFeaturesGetsNone(t *testing.T) {
	root := requiredFeaturesCrate(t)

	r := NarrowToRelatedTests(Runner{Cmd: "cargo", Args: []string{"test"}}, filepath.Join(root, "examples", "plain.rs"), root)

	if got, want := strings.Join(r.Args, " "), "test -p forge --example plain --no-run"; got != want {
		t.Fatalf("runner args = %q, want %q", got, want)
	}
}

// A bench is the same compile check with the same refusal, and gets the same
// answer.
func TestNarrowToRelatedTests_ABenchIsBuiltWithItsRequiredFeatures(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		// skip-ok: an environment probe, not a disabled assertion — the test asserts for real wherever cargo is installed.
		t.Skip("cargo not on PATH; skipping the real-toolchain case")
	}
	root := requiredFeaturesCrate(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"forge\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n"+
		"[features]\ndebug-render = []\n\n"+
		"[[bench]]\nname = \"mix\"\nharness = false\nrequired-features = [\"debug-render\"]\n")
	write(t, root, filepath.Join("benches", "mix.rs"), "fn main() {}\n")

	r := NarrowToRelatedTests(Runner{Cmd: "cargo", Args: []string{"test"}}, filepath.Join(root, "benches", "mix.rs"), root)

	if got, want := strings.Join(r.Args, " "), "test -p forge --bench mix --no-run --features debug-render"; got != want {
		t.Fatalf("runner args = %q, want %q", got, want)
	}
}
