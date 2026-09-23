package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unresolvedImportOutput is issue #651's own failure verbatim in shape: a
// module that DOES exist in the crate reported as absent from the root,
// which is what a stale fingerprint looks like from the inside.
const unresolvedImportOutput = "error[E0432]: unresolved import `crate::wear`\n" +
	" --> crates/forge_powertrain/src/lib.rs:3:5\n" +
	"  |\n" +
	"3 | use crate::wear::Wear;\n" +
	"  |     ^^^^^^^^^^^ no `wear` in the root\n" +
	"\n" +
	"error: could not compile `forge_powertrain` (lib) due to 1 previous error\n"

// staleArtifactCrate writes a two-crate-shaped fixture: a workspace root and
// one member crate whose lib.rs declares `mod wear` and has the file to go
// with it. Returns the workspace root.
func staleArtifactCrate(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	crate := filepath.Join(ws, "crates", "forge_powertrain")
	if err := os.MkdirAll(filepath.Join(crate, "src"), 0o755); err != nil {
		t.Fatalf("setup: mkdir: %v", err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("setup: write %s: %v", path, err)
		}
	}
	write(filepath.Join(ws, "Cargo.toml"), "[workspace]\nmembers = [\"crates/*\"]\n")
	write(filepath.Join(crate, "Cargo.toml"), "[package]\nname = \"forge_powertrain\"\n")
	write(filepath.Join(crate, "src", "lib.rs"), "pub mod wear;\n\nuse crate::wear::Wear;\n")
	write(filepath.Join(crate, "src", "wear.rs"), "pub struct Wear;\n")
	return ws
}

// isolateCargoConfig points CARGO_HOME at an empty directory so the user's
// real ~/.cargo/config.toml (which may well declare a shared build.target-dir
// on a developer box) cannot decide what these tests measure.
func isolateCargoConfig(t *testing.T) {
	t.Helper()
	t.Setenv("CARGO_HOME", t.TempDir())
}

// TestStaleArtifactHint_NamesTheFingerprintWhenTheSymbolIsPresentAndTheTargetDirIsShared
// pins issue #651's narrow heuristic: the compiler says `wear` is not there,
// the crate's own sources say it is, and the target dir those artifacts live
// in is not private to this checkout — the one combination that earns a word
// about build state.
func TestStaleArtifactHint_NamesTheFingerprintWhenTheSymbolIsPresentAndTheTargetDirIsShared(t *testing.T) {
	withIsolatedBuildLock(t)
	isolateCargoConfig(t)
	ws := staleArtifactCrate(t)
	shared := t.TempDir()
	t.Setenv("CARGO_TARGET_DIR", shared)
	// The fingerprint the hint is about, so the test can prove the hint left
	// it alone: this feature suggests a removal, it never performs one.
	fingerprint := filepath.Join(shared, "debug", ".fingerprint", "forge_powertrain-abc123")
	if err := os.MkdirAll(fingerprint, 0o755); err != nil {
		t.Fatalf("setup: mkdir fingerprint: %v", err)
	}

	hint := staleArtifactHint(ws, unresolvedImportOutput)

	if hint == "" {
		t.Fatal("expected a stale-artifact hint for a present symbol in a shared target dir, got silence")
	}
	for _, want := range []string{
		"possible stale artifact",
		"`wear`",
		"forge_powertrain",
		filepath.ToSlash(filepath.Join("crates", "forge_powertrain", "src", "lib.rs")) + ":1",
		filepath.ToSlash(filepath.Join(shared, "debug", ".fingerprint", "forge_powertrain-*")),
		"nothing was deleted",
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint missing %q:\n%s", want, hint)
		}
	}
	if _, err := os.Stat(fingerprint); err != nil {
		t.Errorf("the hint removed build state; it must only suggest: %v", err)
	}
}

// TestStaleArtifactHint_SilentWhenTheSymbolIsAbsentFromTheCrate is the
// ordinary typo — the overwhelmingly common case. A wrong guess here teaches
// people to delete build state whenever a real error confuses them, so the
// absence of the symbol from the crate's sources must buy total silence.
func TestStaleArtifactHint_SilentWhenTheSymbolIsAbsentFromTheCrate(t *testing.T) {
	withIsolatedBuildLock(t)
	isolateCargoConfig(t)
	ws := staleArtifactCrate(t)
	t.Setenv("CARGO_TARGET_DIR", t.TempDir())
	typo := strings.ReplaceAll(unresolvedImportOutput, "wear", "waer")

	if hint := staleArtifactHint(ws, typo); hint != "" {
		t.Errorf("expected silence for a symbol no source declares, got:\n%s", hint)
	}
}

// TestStaleArtifactHint_SilentWhenTheTargetDirIsPrivateToThisCheckout pins
// the second half of the conjunction: a target dir nothing else builds into
// cannot go stale the way issue #651 describes, so a confusing diagnostic
// there is just a diagnostic.
func TestStaleArtifactHint_SilentWhenTheTargetDirIsPrivateToThisCheckout(t *testing.T) {
	withIsolatedBuildLock(t)
	isolateCargoConfig(t)
	ws := staleArtifactCrate(t)
	os.Unsetenv("CARGO_TARGET_DIR")

	if hint := staleArtifactHint(ws, unresolvedImportOutput); hint != "" {
		t.Errorf("expected silence for a target dir private to this checkout, got:\n%s", hint)
	}
}

// TestStaleArtifactHint_FiresWhenAnotherCheckoutIsBuildingIntoThisTargetDir
// covers the other half of condition two: the target dir lives inside this
// checkout, but another checkout holds its build slot right now — the
// concurrency that actually made the artifact go stale.
func TestStaleArtifactHint_FiresWhenAnotherCheckoutIsBuildingIntoThisTargetDir(t *testing.T) {
	withIsolatedBuildLock(t)
	isolateCargoConfig(t)
	ws := staleArtifactCrate(t)
	os.Unsetenv("CARGO_TARGET_DIR")
	_, release, ok := TryAcquireBuildSlot(filepath.Join(ws, "target"), "cargo build -p forge_powertrain", filepath.Join(t.TempDir(), "other-checkout"))
	if !ok {
		t.Fatal("setup: expected the build slot to acquire")
	}
	defer release()

	hint := staleArtifactHint(ws, unresolvedImportOutput)

	if hint == "" {
		t.Fatal("expected a hint while another checkout builds into this target dir, got silence")
	}
	if !strings.Contains(hint, "other-checkout") {
		t.Errorf("hint does not name the other checkout:\n%s", hint)
	}
}

// TestStaleArtifactHint_SilentWhenThisCheckoutHoldsItsOwnBuildSlot keeps the
// concurrency half honest: this session's own build recorded against its own
// target dir is not another checkout, and must read as ordinary.
func TestStaleArtifactHint_SilentWhenThisCheckoutHoldsItsOwnBuildSlot(t *testing.T) {
	withIsolatedBuildLock(t)
	isolateCargoConfig(t)
	ws := staleArtifactCrate(t)
	os.Unsetenv("CARGO_TARGET_DIR")
	_, release, ok := TryAcquireBuildSlot(filepath.Join(ws, "target"), "cargo build -p forge_powertrain", ws)
	if !ok {
		t.Fatal("setup: expected the build slot to acquire")
	}
	defer release()

	if hint := staleArtifactHint(ws, unresolvedImportOutput); hint != "" {
		t.Errorf("expected silence when this checkout owns the build slot, got:\n%s", hint)
	}
}

// TestStaleArtifactHint_SilentForAModuleFileThatIsGenuinelyMissing guards the
// one diagnostic where the declaration the scan finds is exactly what rustc
// already read: `file not found for module` means the `mod` line is there and
// the FILE is not. That is a real error with a real fix, never a fingerprint.
func TestStaleArtifactHint_SilentForAModuleFileThatIsGenuinelyMissing(t *testing.T) {
	withIsolatedBuildLock(t)
	isolateCargoConfig(t)
	ws := staleArtifactCrate(t)
	t.Setenv("CARGO_TARGET_DIR", t.TempDir())
	output := "error[E0583]: file not found for module `wear`\n" +
		" --> crates/forge_powertrain/src/lib.rs:1:1\n" +
		"  |\n" +
		"1 | pub mod wear;\n" +
		"  | ^^^^^^^^^^^^^\n"

	if hint := staleArtifactHint(ws, output); hint != "" {
		t.Errorf("expected silence for a genuinely missing module file, got:\n%s", hint)
	}
}

// TestRedSummary_CarriesTheHintWithoutSofteningTheRed pins the contract the
// brief is most exposed on: the hint is an ADDITIONAL line. The verdict, the
// outcome text and the output snippet are exactly what they were.
func TestRedSummary_CarriesTheHintWithoutSofteningTheRed(t *testing.T) {
	withIsolatedBuildLock(t)
	isolateCargoConfig(t)
	ws := staleArtifactCrate(t)
	t.Setenv("CARGO_TARGET_DIR", t.TempDir())
	r := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "forge_powertrain"}}

	got := redSummary(r, ws, Red, unresolvedImportOutput)

	if !strings.Contains(got, "outcome="+string(Red)) {
		t.Errorf("the red verdict was softened:\n%s", got)
	}
	if !strings.Contains(got, "possible stale artifact") {
		t.Errorf("red summary does not carry the hint:\n%s", got)
	}
	if !strings.Contains(got, "no `wear` in the root") {
		t.Errorf("red summary lost the runner output snippet:\n%s", got)
	}
}
