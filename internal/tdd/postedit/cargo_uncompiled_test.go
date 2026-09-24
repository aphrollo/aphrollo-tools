package postedit

import (
	"strings"
	"testing"
)

// TestCargoNestedTestFileReachable_RequiresModDeclaration pins the
// reachability predicate issue #736 needs: a nested tests/<dir>/ file
// (cargoTestTarget's "folded into some binary" case) is only reachable when
// that directory's own mod.rs actually declares `mod <stem>;` — its own entry
// point (mod.rs/main.rs) is always reachable by cargo's own convention,
// whatever it does or does not declare about ITSELF.
func TestCargoNestedTestFileReachable_RequiresModDeclaration(t *testing.T) {
	t.Run("no mod.rs at all -> unreachable", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "tests/car/launch_grip_probe.rs", "#[test]\nfn grips_launch() {}\n")
		if cargoNestedTestFileReachable(root, "tests/car/launch_grip_probe.rs") {
			t.Fatal("want unreachable: no tests/car/mod.rs exists at all")
		}
	})

	t.Run("mod.rs exists but never names the file's stem -> unreachable", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "tests/car/mod.rs", "mod launch_control;\n")
		write(t, root, "tests/car/launch_grip_probe.rs", "#[test]\nfn grips_launch() {}\n")
		if cargoNestedTestFileReachable(root, "tests/car/launch_grip_probe.rs") {
			t.Fatal("want unreachable: mod.rs does not declare launch_grip_probe")
		}
	})

	t.Run("mod.rs declares the stem -> reachable", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "tests/car/mod.rs", "mod launch_control;\npub mod launch_grip_probe;\n")
		write(t, root, "tests/car/launch_grip_probe.rs", "#[test]\nfn grips_launch() {}\n")
		if !cargoNestedTestFileReachable(root, "tests/car/launch_grip_probe.rs") {
			t.Fatal("want reachable: mod.rs declares pub mod launch_grip_probe")
		}
	})

	t.Run("mod.rs itself is always reachable, declared or not", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "tests/car/mod.rs", "mod launch_control;\n")
		if !cargoNestedTestFileReachable(root, "tests/car/mod.rs") {
			t.Fatal("want reachable: a mod.rs entry point mounts itself by cargo's own convention")
		}
	})
}

// TestPostEdit_UnreachableNestedTestFile_ReportsNotCompiledNeverGreen pins
// issue #736: a Write of a new integration-test file inside tests/<dir>/ that
// no `mod` declaration in that directory's mod.rs reaches compiles into
// NOTHING at all -- cargo's own build never reads it -- so a package run that
// still passes (the rest of the crate, untouched) must never be reported as
// green. The verdict must say the file was not built or run.
func TestPostEdit_UnreachableNestedTestFile_ReportsNotCompiledNeverGreen(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := cargoCrate(t, "forge_lab")
	write(t, root, "tests/car/mod.rs", "mod launch_control;\n")
	write(t, root, "tests/car/launch_grip_probe.rs", "#[test]\nfn grips_launch() { assert!(true); }\n")

	packageStillGreen := "test result: ok. 3 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out\n"
	got := PostEdit(postPayload("Write", root+"/tests/car/launch_grip_probe.rs"),
		fakeRunResult(SuiteResult{Passed: true, Output: packageStillGreen}))

	if strings.Contains(got, "green") {
		t.Fatalf("an uncompiled test file must never read as green, got: %s", got)
	}
	for _, want := range []string{strings.ToUpper(NotCompiled), "NOT tested", "tests/car/launch_grip_probe.rs"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the advisory must name %q, got: %s", want, got)
		}
	}
	if logged := gateLogText(t, cfg); !strings.Contains(logged, NotCompiled) {
		t.Fatalf("gate.log must carry the %s verdict, got:\n%s", NotCompiled, logged)
	}
}

// TestPostEdit_DeclaredNestedTestFile_NotFlaggedNotCompiled guards the
// direction the fix must not overreach: once tests/car/mod.rs actually
// declares the file, it IS reachable, and the run's own verdict stands.
func TestPostEdit_DeclaredNestedTestFile_NotFlaggedNotCompiled(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := cargoCrate(t, "forge_lab")
	write(t, root, "tests/car/mod.rs", "mod launch_control;\npub mod launch_grip_probe;\n")
	write(t, root, "tests/car/launch_grip_probe.rs", "#[test]\nfn grips_launch() { assert!(true); }\n")

	got := PostEdit(postPayload("Write", root+"/tests/car/launch_grip_probe.rs"),
		fakeRunResult(SuiteResult{Passed: true, Output: "test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out\n"}))

	if strings.Contains(got, strings.ToUpper(NotCompiled)) {
		t.Fatalf("a declared nested test file must not be reported not-compiled, got: %s", got)
	}
}
