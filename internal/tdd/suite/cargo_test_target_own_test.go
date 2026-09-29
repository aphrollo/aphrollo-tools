package suite

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// These are suite's own tests of cargo_test_target.go, reached today only
// through internal/tdd/precommit's and postedit's cargo tests.

const metadataDoc = `{"packages":[
 {"name":"alpha","targets":[{"name":"alpha","kind":["lib"]},{"name":"it","kind":["test"]},{"name":"suite","kind":["test"]}]},
 {"name":"beta","targets":[{"name":"beta","kind":["bin"]}]}
]}`

// TestParseCargoTestTargets_KeepsOnlyTargetsOfKindTest pins the reading: each
// package maps to the names of its "test" kind targets, a package with none
// maps to an empty set, and other kinds never appear.
func TestParseCargoTestTargets_KeepsOnlyTargetsOfKindTest(t *testing.T) {
	t.Parallel()
	got := parseCargoTestTargets([]byte(metadataDoc))
	want := map[string]map[string]bool{"alpha": {"it": true, "suite": true}, "beta": {}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseCargoTestTargets = %v, want %v", got, want)
	}
}

// TestParseCargoTestTargets_AnUnreadableDocumentIsNil pins the failure arm.
func TestParseCargoTestTargets_AnUnreadableDocumentIsNil(t *testing.T) {
	t.Parallel()
	if got := parseCargoTestTargets([]byte("not json")); got != nil {
		t.Fatalf("parseCargoTestTargets = %v, want nil", got)
	}
}

// TestLoadCargoTestTargets_NoWorkspaceIsNil pins the empty-root guard.
func TestLoadCargoTestTargets_NoWorkspaceIsNil(t *testing.T) {
	t.Parallel()
	if got := loadCargoTestTargets(""); got != nil {
		t.Fatalf("loadCargoTestTargets = %v, want nil", got)
	}
}

// Serial: points CARGO at a fake in the process-wide environment.
// TestLoadCargoTestTargets_AMissingCargoFallsBackToNil pins the absence arm:
// with no cargo to ask, every nested candidate stays unconfirmed.
func TestLoadCargoTestTargets_AMissingCargoFallsBackToNil(t *testing.T) {
	t.Setenv("CARGO", filepath.Join(t.TempDir(), "no-such-cargo"))
	if got := loadCargoTestTargets(t.TempDir()); got != nil {
		t.Fatalf("loadCargoTestTargets = %v, want nil with no cargo", got)
	}
}

// Serial: points CARGO at a fake in the process-wide environment.
// TestLoadCargoTestTargets_ReadsWhatCargoMetadataAnswers pins the success
// path end to end: the workspace's manifest is handed to `cargo metadata
// --no-deps` and its document parsed.
func TestLoadCargoTestTargets_ReadsWhatCargoMetadataAnswers(t *testing.T) {
	if runtime.GOOS == "windows" {
		// skip-ok: the fake cargo is a POSIX shell script; every assertion runs on each POSIX box.
		t.Skip("the fake cargo is a POSIX shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "args.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > '" + log + "'\ncat <<'EOF'\n" + metadataDoc + "\nEOF\n"
	if err := proc.WriteExecutable(filepath.Join(dir, "cargo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARGO", filepath.Join(dir, "cargo"))
	ws := t.TempDir()

	got := loadCargoTestTargets(ws)
	if !got["alpha"]["it"] || !got["alpha"]["suite"] {
		t.Fatalf("targets = %v, want alpha's test targets", got)
	}
	args, _ := os.ReadFile(log)
	want := "metadata --no-deps --format-version 1 --manifest-path " + filepath.Join(ws, "Cargo.toml") + "\n"
	if string(args) != want {
		t.Fatalf("cargo was run as %q, want %q", args, want)
	}
}

// Serial: swaps the package's cargo test-target probe, a process-wide override.
// TestCargoTestTargetsFor_AsksOncePerWorkspaceRoot pins the memo: a second
// question about the same workspace is answered from the first answer.
func TestCargoTestTargetsFor_AsksOncePerWorkspaceRoot(t *testing.T) {
	calls := 0
	t.Cleanup(SetCargoTestTargetsForTest(func(string) map[string]map[string]bool {
		calls++
		return map[string]map[string]bool{"alpha": {"it": true}}
	}))
	ws := t.TempDir()
	cargoTestTargetsFor(ws)
	cargoTestTargetsFor(ws)
	cargoTestTargetsFor(t.TempDir())
	if calls != 2 {
		t.Fatalf("the probe ran %d times, want once per distinct workspace (2)", calls)
	}
}

// stubTargets installs a cargo test-target probe answering targets.
func stubTargets(t *testing.T, targets map[string]map[string]bool) {
	t.Helper()
	t.Cleanup(SetCargoTestTargetsForTest(func(string) map[string]map[string]bool { return targets }))
}

// cratePackage lays down a one-crate root named pkg.
func cratePackage(t *testing.T, pkg string) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \""+pkg+"\"\nversion = \"0.1.0\"\n")
	return root
}

// Serial: swaps the package's cargo test-target probe, a process-wide override.
// TestCargoHasTestTargetAt_ResolvesTheRootsOwnPackageAndChecksItsTargets pins
// the lookup: the package named by root's manifest, and the target name in it.
func TestCargoHasTestTargetAt_ResolvesTheRootsOwnPackageAndChecksItsTargets(t *testing.T) {
	stubTargets(t, map[string]map[string]bool{"alpha": {"it": true}})
	root := cratePackage(t, "alpha")
	if !cargoHasTestTargetAt(root, "it") {
		t.Error("alpha declares the it test target")
	}
	if cargoHasTestTargetAt(root, "missing") {
		t.Error("alpha declares no missing target")
	}
	if cargoHasTestTargetAt(t.TempDir(), "it") {
		t.Error("a root with no readable package confirms nothing")
	}
}

// Serial: swaps the package's cargo test-target probe, a process-wide override.
// TestCargoTestTargetRunner_ConfirmsANestedGuessBeforeNamingIt pins the
// scoping: a flat name is trusted, a nested guess is named only when cargo
// confirms it, and an unconfirmed one falls back to the whole-package run.
func TestCargoTestTargetRunner_ConfirmsANestedGuessBeforeNamingIt(t *testing.T) {
	stubTargets(t, map[string]map[string]bool{"alpha": {"confirmed": true}})
	root := cratePackage(t, "alpha")
	base := Runner{Cmd: "cargo", Args: []string{"test"}}
	ws := cargoWorkspaceRoot(root)

	flat := cargoTestTargetRunner(base, root, "anything", false)
	if want := []string{"test", "-p", "alpha", "--test", "anything"}; !reflect.DeepEqual(flat.Args, want) || flat.Dir != ws {
		t.Errorf("flat: %+v, want args %v", flat, want)
	}
	confirmed := cargoTestTargetRunner(base, root, "confirmed", true)
	if want := []string{"test", "-p", "alpha", "--test", "confirmed"}; !reflect.DeepEqual(confirmed.Args, want) {
		t.Errorf("confirmed nested: %v, want %v", confirmed.Args, want)
	}
	guess := cargoTestTargetRunner(base, root, "guess", true)
	if want := []string{"test", "-p", "alpha"}; !reflect.DeepEqual(guess.Args, want) {
		t.Errorf("unconfirmed nested: %v, want the whole-package run %v", guess.Args, want)
	}
}

// Serial: swaps the package's cargo test-target probe, a process-wide override.
// TestCargoFailFirstTarget_ClassifiesAStagedTestFile pins the three answers:
// an inline module has no target, an unconfirmed directory guess forces the
// package run, and a flat or confirmed one names its target.
func TestCargoFailFirstTarget_ClassifiesAStagedTestFile(t *testing.T) {
	stubTargets(t, map[string]map[string]bool{"alpha": {"suite": true}})
	root := t.TempDir()

	if tgt, inline, unconfirmed := cargoFailFirstTarget(root, "alpha", "src/lib.rs"); tgt != "" || !inline || unconfirmed {
		t.Errorf("inline: (%q, %v, %v), want (\"\", true, false)", tgt, inline, unconfirmed)
	}
	if tgt, inline, unconfirmed := cargoFailFirstTarget(root, "alpha", "tests/it.rs"); tgt != "it" || inline || unconfirmed {
		t.Errorf("flat: (%q, %v, %v), want (it, false, false)", tgt, inline, unconfirmed)
	}
	if tgt, inline, unconfirmed := cargoFailFirstTarget(root, "alpha", "tests/suite/helper.rs"); tgt != "suite" || inline || unconfirmed {
		t.Errorf("confirmed nested: (%q, %v, %v), want (suite, false, false)", tgt, inline, unconfirmed)
	}
	if tgt, inline, unconfirmed := cargoFailFirstTarget(root, "alpha", "tests/guess/helper.rs"); tgt != "" || inline || !unconfirmed {
		t.Errorf("unconfirmed nested: (%q, %v, %v), want (\"\", false, true)", tgt, inline, unconfirmed)
	}
}

// TestCargoTestTarget_MapsAPathToItsTargetCandidate pins the mapping table.
func TestCargoTestTarget_MapsAPathToItsTargetCandidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rel    string
		name   string
		nested bool
	}{
		{"tests/it.rs", "it", false},
		{"crates/a/tests/it.rs", "it", false},
		{"tests/suite/main.rs", "suite", false},
		{"tests/suite/helper.rs", "suite", true},
		{"tests/suite/deep/x.rs", "suite", true},
		{"src/tire_rig/tests/ladder.rs", "", false},
		{"crates/a/src/lib.rs", "", false},
		{"README.md", "", false},
		{"tests", "", false},
	}
	for _, c := range cases {
		if name, nested := cargoTestTarget(c.rel); name != c.name || nested != c.nested {
			t.Errorf("cargoTestTarget(%q) = (%q, %v), want (%q, %v)", c.rel, name, nested, c.name, c.nested)
		}
	}
}
