package suite

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// staleCrate writes a workspace with one member crate, forge_powertrain,
// whose lib.rs declares `mod wear` and has wear.rs to go with it. Returns
// the workspace root.
func staleCrate(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	write(t, ws, "Cargo.toml", "[workspace]\nmembers = [\"crates/*\"]\n")
	write(t, ws, "crates/forge_powertrain/Cargo.toml", "[package]\nname = \"forge_powertrain\"\n")
	write(t, ws, "crates/forge_powertrain/src/lib.rs", "pub mod wear;\n\nuse crate::wear::Wear;\n")
	write(t, ws, "crates/forge_powertrain/src/wear.rs", "pub struct Wear;\n")
	return ws
}

// staleOutput is issue #651's failure in shape: a module the crate does have,
// reported absent from the root.
const staleOutput = "error[E0432]: unresolved import `crate::wear`\n" +
	" --> crates/forge_powertrain/src/lib.rs:3:5\n" +
	"  |\n" +
	"3 | use crate::wear::Wear;\n" +
	"  |     ^^^^^^^^^^^ no `wear` in the root\n"

// Each rustc "does not exist" phrasing yields the one identifier it claims is
// missing; a qualified path yields its last segment, and a capture that is
// not a plain identifier yields nothing rather than a guess.
func TestClaimedMissingName_ReadsTheIdentifierEachDiagnosticClaimsIsAbsent(t *testing.T) {
	cases := []struct {
		line, want string
		ok         bool
	}{
		{"  |     ^^^^ no `wear` in the root", "wear", true},
		{"error[E0432]: unresolved import `crate::engine::wear`", "wear", true},
		{"error[E0433]: failed to resolve: use of undeclared crate or module `tyre`", "tyre", true},
		{"error[E0425]: cannot find function `spin_up` in this scope", "spin_up", true},
		{"error[E0599]: no method named `grip` found for struct `Tire`", "grip", true},
		{"error[E0412]: cannot find type `Vec<u8>` in this scope", "", false},
		{"error[E0308]: mismatched types", "", false},
	}
	for _, tc := range cases {
		got, ok := claimedMissingName(tc.line)
		if got != tc.want || ok != tc.ok {
			t.Errorf("claimedMissingName(%q) = (%q, %v), want (%q, %v)", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}

// Each claim takes the nearest `-->` location: the following one for a claim
// on the header line, the preceding one for a claim on the caret line. A
// name claimed twice is reported once, and a claim whose nearest location
// does not resolve to a file is dropped rather than pinned on a guess.
func TestDiagnosedMissingSymbols_PairsEachClaimWithItsNearestLocation(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/a.rs", "")
	write(t, dir, "src/b.rs", "")
	// alpha's claim is on a header line, so it takes the NEXT location (a.rs);
	// beta's is on a caret line, so it takes the PREVIOUS one (b.rs); alpha
	// claimed again is reported once; gamma's nearest location is a file that
	// does not exist.
	output := strings.Join([]string{
		"error[E0432]: unresolved import `crate::alpha`",
		" --> src/a.rs:1:5",
		"  |",
		"  |",
		"  |",
		"  |",
		" --> src/b.rs:9:1",
		"  |     ^^^ no `beta` in the root",
		"error: unresolved import `crate::alpha`",
		"",
		"error: cannot find value `gamma` in this scope",
		" --> src/gone.rs:2:2",
	}, "\r\n")

	got := diagnosedMissingSymbols(dir, output)

	want := []missingSymbol{
		{name: "alpha", file: filepath.Join(dir, "src", "a.rs")},
		{name: "beta", file: filepath.Join(dir, "src", "b.rs")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnosedMissingSymbols =\n %+v\nwant\n %+v", got, want)
	}
}

// Output with no location at all names no file, so there is no crate to
// scan: nothing is claimed.
func TestDiagnosedMissingSymbols_NoLocationClaimsNothing(t *testing.T) {
	if got := diagnosedMissingSymbols(t.TempDir(), "error: cannot find value `gamma` in this scope\n"); got != nil {
		t.Fatalf("diagnosedMissingSymbols = %+v, want nil", got)
	}
}

// The scan matches declaration forms only, so the use site that produced the
// diagnostic is never its own evidence; it reports the 1-based line of the
// declaration and never reads build output or dot-directories.
func TestDeclarationSite_FindsOnlyDeclarationsInTheCratesOwnSources(t *testing.T) {
	crate := t.TempDir()
	write(t, crate, "src/lib.rs", "use crate::wear::Wear;\nfn main() { wear(); }\n")
	write(t, crate, "src/engine.rs", "// engine\n\npub(crate) async fn spin_up() {}\nmacro_rules! grip { () => {} }\n")
	write(t, crate, "target/debug/build/gen.rs", "pub struct Ghost;\n")
	write(t, crate, ".cache/x.rs", "pub struct Hidden;\n")
	write(t, crate, "src/notes.txt", "pub struct Textual;\n")

	cases := []struct {
		name     string
		wantFile string
		wantLine int
		found    bool
	}{
		{"spin_up", filepath.Join(crate, "src", "engine.rs"), 3, true},
		{"grip", filepath.Join(crate, "src", "engine.rs"), 4, true},
		{"wear", "", 0, false},     // only ever used, never declared
		{"Ghost", "", 0, false},    // under target/
		{"Hidden", "", 0, false},   // under a dot-directory
		{"Textual", "", 0, false},  // not a .rs file
		{"spin_upx", "", 0, false}, // a longer name is not this one
	}
	for _, tc := range cases {
		file, line, found := declarationSite(crate, tc.name)
		if file != tc.wantFile || line != tc.wantLine || found != tc.found {
			t.Errorf("declarationSite(%q) = (%q, %d, %v), want (%q, %d, %v)", tc.name, file, line, found, tc.wantFile, tc.wantLine, tc.found)
		}
	}
}

// A crate living under a dot-directory is still scanned: only the walk's
// own root is exempt from the dot-directory skip.
func TestDeclarationSite_ScansACrateWhoseOwnDirectoryIsDotted(t *testing.T) {
	crate := filepath.Join(t.TempDir(), ".worktrees", "lane")
	write(t, crate, "src/wear.rs", "pub struct Wear;\n")

	if _, line, found := declarationSite(crate, "Wear"); !found || line != 1 {
		t.Fatalf("declarationSite under a dotted crate dir = (line %d, found %v), want (1, true)", line, found)
	}
}

// crateOwning walks up past a virtual manifest to the member crate owning
// the file, and answers false when no [package] owns it.
func TestCrateOwning_NamesTheNearestPackageAboveTheFile(t *testing.T) {
	ws := staleCrate(t)

	dir, pkg, ok := crateOwning(filepath.Join(ws, "crates", "forge_powertrain", "src", "wear.rs"))
	if !ok || pkg != "forge_powertrain" || dir != filepath.Join(ws, "crates", "forge_powertrain") {
		t.Errorf("crateOwning(member file) = (%q, %q, %v), want the forge_powertrain crate", dir, pkg, ok)
	}
	if _, _, ok := crateOwning(filepath.Join(ws, "README.md")); ok {
		t.Error("crateOwning answered a package for a file only a virtual manifest sits above")
	}
}

// The hint needs both halves: a symbol the crate declares, and a target dir
// shared outside this checkout. Then it names the symbol, the declaration,
// and the fingerprint glob to remove by hand; either half missing is silence.
func TestStaleArtifactHint_SpeaksOnlyWhenTheSymbolExistsAndTheTargetDirIsShared(t *testing.T) {
	t.Run("present and shared", func(t *testing.T) {
		ws := staleCrate(t)
		shared := t.TempDir()
		t.Setenv("CARGO_TARGET_DIR", shared)

		hint := staleArtifactHint(ws, staleOutput)

		for _, want := range []string{
			"`wear` does not exist",
			"crate forge_powertrain declares it at crates/forge_powertrain/src/lib.rs:1",
			filepath.ToSlash(filepath.Join(shared, "debug", ".fingerprint", "forge_powertrain-*")),
			"nothing was deleted",
		} {
			if !strings.Contains(hint, want) {
				t.Errorf("hint missing %q:\n%s", want, hint)
			}
		}
	})
	t.Run("absent and shared", func(t *testing.T) {
		ws := staleCrate(t)
		t.Setenv("CARGO_TARGET_DIR", t.TempDir())
		if hint := staleArtifactHint(ws, strings.ReplaceAll(staleOutput, "wear", "waer")); hint != "" {
			t.Errorf("a symbol no source declares earned a hint:\n%s", hint)
		}
	})
	t.Run("present and private", func(t *testing.T) {
		ws := staleCrate(t)
		t.Setenv("CARGO_TARGET_DIR", "")
		os.Unsetenv("CARGO_TARGET_DIR")
		if hint := staleArtifactHint(ws, staleOutput); hint != "" {
			t.Errorf("a target dir private to this checkout earned a hint:\n%s", hint)
		}
	})
}
