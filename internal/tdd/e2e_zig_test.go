package tdd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// e2eZigTimeout bounds the real `zig build test` runs. A cold Zig build
// (compiler + test binary) is slower than a warm Go suite, so this is generous.
const e2eZigTimeout = 180 * time.Second

// fixtureBuildZig is a minimal build.zig modeling zeta's `test` step: it builds
// the library module and wires its inline tests into a `zig build test` step.
// It mirrors zeta's modern (root_module) API so the fixture compiles under the
// same toolchain the gates target. No build.zig.zon is needed — the project has
// no dependencies, so a lone build.zig is a complete, runnable project.
const fixtureBuildZig = `const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});

    const mod = b.addModule("fixture", .{
        .root_source_file = b.path("src/root.zig"),
        .target = target,
    });

    const mod_tests = b.addTest(.{ .root_module = mod });
    const run_mod_tests = b.addRunArtifact(mod_tests);

    const test_step = b.step("test", "Run unit tests");
    test_step.dependOn(&run_mod_tests.step);
}
`

// fixtureRootGreen holds production code plus a PASSING inline test — the zeta
// inline-test model (test and the code it exercises in the same file).
const fixtureRootGreen = `const std = @import("std");

pub fn add(a: i32, b: i32) i32 {
    return a + b;
}

test "add sums its two arguments" {
    try std.testing.expectEqual(@as(i32, 3), add(1, 2));
}
`

// fixtureRootRed is the same file with the inline test's expectation flipped to
// a value the implementation does not produce — a real runtime assertion
// failure, so `zig build test` exits non-zero.
const fixtureRootRed = `const std = @import("std");

pub fn add(a: i32, b: i32) i32 {
    return a + b;
}

test "add sums its two arguments" {
    try std.testing.expectEqual(@as(i32, 4), add(1, 2));
}
`

// TestE2E_Zig_BuildTest drives the real Zig toolchain end-to-end through the
// production path — DetectRunner picks the command, RunSuite executes it, and
// ClassifyOutcome interprets the output. It auto-skips when `zig` is absent,
// exactly like the LSP e2e tests skip when their server isn't installed; the
// toolchain is never faked. With zig present it exercises BOTH a real green run
// and a real red run against one inline-test fixture.
func TestE2E_Zig_BuildTest(t *testing.T) {
	if _, err := exec.LookPath("zig"); err != nil {
		t.Skip("zig not on PATH; skipping e2e")
	}

	dir := t.TempDir()
	writeFixture(t, dir, "build.zig", fixtureBuildZig)
	writeFixture(t, dir, "src/root.zig", fixtureRootGreen)

	runner, ok := DetectRunner(dir)
	if !ok {
		t.Fatalf("DetectRunner did not recognize the zig project at %s", dir)
	}
	if runner.Cmd != "zig" || len(runner.Args) != 2 || runner.Args[0] != "build" || runner.Args[1] != "test" {
		t.Fatalf("detected runner = %+v, want `zig build test`", runner)
	}

	run := RunSuite(e2eZigTimeout)

	// GREEN: the inline test passes against the real implementation.
	green := run(runner, dir)
	if !green.Passed {
		t.Fatalf("expected the green fixture to pass; output:\n%s", green.Output)
	}
	if outcome := ClassifyOutcome(green.Passed, green.Output, nil); outcome.IsRed() {
		t.Fatalf("green run classified as RED (%s); output:\n%s", outcome, green.Output)
	}

	// RED: flip the expectation so the same inline test fails at runtime.
	writeFixture(t, dir, "src/root.zig", fixtureRootRed)
	red := run(runner, dir)
	if red.Passed {
		t.Fatalf("expected the red fixture to fail; output:\n%s", red.Output)
	}
	if outcome := ClassifyOutcome(red.Passed, red.Output, nil); !outcome.IsRed() {
		t.Fatalf("red run not classified as RED (got %s); output:\n%s", outcome, red.Output)
	}
}

// writeFixture writes content to dir/rel, creating parent directories.
func writeFixture(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
