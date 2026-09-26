package precommit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestPrecommit_FailFirst_ExportsTheResolvedTargetDir pins the fail-first
// worktree's target: it lives outside the repo, so cargo's default would put
// a brand-new target/ inside it and cold-build the world on every commit. It
// must name the SAME target the mechanical stage uses — the environment's
// CARGO_TARGET_DIR when set, else the repo's own target/ (one target per
// repo, 2026-09-02). The operator's value is restored once the gate is done.
func TestPrecommit_FailFirst_ExportsTheResolvedTargetDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	shared := filepath.Join(t.TempDir(), "shared-warm-target")
	t.Setenv("CARGO_TARGET_DIR", shared)
	root := makeCargoRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 1 }\n")
	write(t, root, "src/widget_test.rs", "#[test]\nfn widget_is_one() { assert_eq!(1, crate::widget::widget()); }\n")
	gitDo(t, root, "add", ".")

	var worktreeTarget, mechanicalTarget string
	run := func(r Runner, dir string) SuiteResult {
		if dir == root {
			mechanicalTarget = os.Getenv("CARGO_TARGET_DIR")
			return SuiteResult{Passed: true}
		}
		worktreeTarget = os.Getenv("CARGO_TARGET_DIR")
		// The applied test cannot compile without the staged source -> RED,
		// which satisfies fail-first.
		return SuiteResult{Passed: false, Output: "error[E0425]: cannot find function `widget`"}
	}
	if res := Precommit(root, run); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if worktreeTarget != shared {
		t.Fatalf("fail-first worktree ran with %q, want the resolved target %q", worktreeTarget, shared)
	}
	if mechanicalTarget != shared {
		t.Fatalf("mechanical run built in %q, want the resolved target %q", mechanicalTarget, shared)
	}
	if got := os.Getenv("CARGO_TARGET_DIR"); got != shared {
		t.Fatalf("CARGO_TARGET_DIR must be restored after the gate, got %q", got)
	}
}

// TestPrecommit_FailFirst_StableWorktreeUnderStateDir pins where the
// fail-first worktree lives: under the state dir (CLAUDE_CONFIG_DIR), not the
// OS temp dir, and at the SAME per-repo path on every invocation — a stable
// worktree keeps build fingerprints warm across commits instead of
// cold-compiling into a fresh MkdirTemp each time.
func TestPrecommit_FailFirst_StableWorktreeUnderStateDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 { t.Fatal(\"no\") }\n}\n")
	gitDo(t, root, "add", ".")

	var dirs []string
	run := func(r Runner, dir string) SuiteResult {
		dirs = append(dirs, dir)
		// The applied test cannot compile without the impl → RED, the normal
		// conclusive fail-first outcome, located in the test as go prints it.
		return SuiteResult{Passed: false, Output: "./widget_test.go:6:5: undefined: Widget"}
	}
	for i := 0; i < 2; i++ {
		if !failFirstViolated(root, []string{"widget_test.go"}, nil, run).Conclusive {
			t.Fatalf("fail-first run %d must be conclusive", i)
		}
	}
	if len(dirs) != 2 {
		t.Fatalf("expected two worktree runs, got %d: %v", len(dirs), dirs)
	}
	if !strings.HasPrefix(dirs[0], cfg) {
		t.Fatalf("fail-first worktree must live under the state dir %s, got %s", cfg, dirs[0])
	}
	if dirs[0] != dirs[1] {
		t.Fatalf("fail-first worktree must be a stable per-repo path across invocations: %s vs %s", dirs[0], dirs[1])
	}
}

// --- Zig (inline-test model) ------------------------------------------------

// makeZigRepo creates a committed Zig project whose root marker is build.zig,
// with a baseline src file, then returns the repo root for the caller to stage
// onto. Like makeJSRepo, the suite is faked (zig need not be installed) — only
// DetectRunner's build.zig stat and the fail-first worktree's checkout of the
// committed tree matter here. The real toolchain is exercised by the e2e test.
func makeZigRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "build.zig", "// build\n")
	write(t, root, "src/root.zig", "pub fn add(a: i32, b: i32) i32 {\n\treturn a + b;\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root
}

// loggedRun records one SuiteRunner invocation and the directory it ran in, so a
// test can tell a mechanical run (at repoRoot) from a fail-first worktree run
// (at a temp dir != repoRoot).
type loggedRun struct {
	runner Runner
	dir    string
}

// recordAllRuns is a SuiteRunner that records EVERY run (mechanical and
// fail-first alike) and reports Passed via the pass predicate, keyed on the run
// directory. Unlike recordRunner it captures worktree runs too, so a test can
// assert whether fail-first executed at all. Deadline is stripped for the
// same reason as recordRunner's.
func recordAllRuns(seen *[]loggedRun, pass func(dir string) bool) SuiteRunner {
	return func(r Runner, dir string) SuiteResult {
		r.Deadline = time.Time{}
		if !isQualityRunner(r) {
			*seen = append(*seen, loggedRun{runner: r, dir: dir})
		}
		return SuiteResult{Passed: pass(dir)}
	}
}

// TestPrecommit_Zig_InlineTestCommit_RunsFullSuite_NoFailFirst pins the
// zeta-style inline-test commit: a single src/*.zig holding BOTH production code
// and `test "..." {}` blocks classifies as Source, so splitKinds yields ZERO
// staged Test files. The `len(tests)>0 && len(srcs)>0` fail-first guard is
// therefore false and fail-first is skipped entirely (it cannot isolate inline
// tests from the impl in the same hunk, so it fails OPEN by never running). The
// mechanical stage then runs the FULL `zig build test` suite — zig has no
// related mode, so narrowing leaves it unchanged. The commit must not block.
func TestPrecommit_Zig_InlineTestCommit_RunsFullSuite_NoFailFirst(t *testing.T) {
	root := makeZigRepo(t)
	// One new .zig file with an inline test alongside the code it exercises —
	// the inline-test model. ClassifyFile → Source (not a *_test.zig, not under
	// tests/), so it is NOT a staged Test file.
	write(t, root, "src/math.zig",
		"const std = @import(\"std\");\n\npub fn mul(a: i32, b: i32) i32 {\n\treturn a * b;\n}\n\ntest \"mul multiplies\" {\n\ttry std.testing.expectEqual(@as(i32, 6), mul(2, 3));\n}\n")
	gitDo(t, root, "add", ".")

	// Sanity: the staged change is all Source, no Test — the guard precondition.
	tests, srcs := splitKinds(stagedFiles(root))
	if len(tests) != 0 || len(srcs) != 1 {
		t.Fatalf("inline-test commit: want 0 tests / 1 src, got tests=%v srcs=%v", tests, srcs)
	}

	var seen []loggedRun
	res := Mechanical(root, recordAllRuns(&seen, func(string) bool { return true }))
	if res.Blocked {
		t.Fatalf("inline-test commit must not block: %s", res.Message)
	}
	// Exactly one run, at the repo root (mechanical) — fail-first never ran, so
	// there is no second run at a worktree temp dir.
	if len(seen) != 1 {
		t.Fatalf("expected exactly one run (mechanical, no fail-first), got %d: %+v", len(seen), seen)
	}
	if seen[0].dir != root {
		t.Fatalf("the single run must be the mechanical run at root, ran in %s", seen[0].dir)
	}
	want := Runner{Cmd: "zig", Args: []string{"build", "test"}}
	if !reflect.DeepEqual(seen[0].runner, want) {
		t.Fatalf("mechanical runner = %+v, want full suite %+v", seen[0].runner, want)
	}
}

// TestPrecommit_Zig_ExplicitTestFile_FailsOpenNoFalseBlock covers the explicit
// test-file case: a tests/*.zig integration test staged ALONGSIDE the src it
// imports. Here splitKinds yields a Test file AND a Source file, so the
// fail-first guard fires and fail-first runs in a worktree at HEAD with only the
// test applied. That test cannot compile without the source it depends on, so
// the worktree run fails to RUN — modeled here by the runner reporting NOT
// passed for the worktree dir. A non-passing fail-first run is (violated=false):
// it must fail OPEN, never a false block. The mechanical run at root passes.
func TestPrecommit_Zig_ExplicitTestFile_FailsOpenNoFalseBlock(t *testing.T) {
	root := makeZigRepo(t)
	// A new src file plus an explicit integration test under tests/ that imports
	// it. The test depends on the source, so applied alone (fail-first) it would
	// not compile.
	write(t, root, "src/widget.zig", "pub fn widget() i32 {\n\treturn 1;\n}\n")
	write(t, root, "tests/widget_test.zig",
		"const std = @import(\"std\");\nconst widget = @import(\"widget\");\n\ntest \"widget integration\" {\n\ttry std.testing.expectEqual(@as(i32, 1), widget.widget());\n}\n")
	gitDo(t, root, "add", ".")

	// Sanity: both a staged Test and a staged Source — the fail-first precondition.
	tests, srcs := splitKinds(stagedFiles(root))
	if len(tests) != 1 || len(srcs) != 1 {
		t.Fatalf("explicit-test commit: want 1 test / 1 src, got tests=%v srcs=%v", tests, srcs)
	}

	// Mechanical (at root) passes; the fail-first worktree run (any dir != root)
	// "fails to compile" → not passed.
	var seen []loggedRun
	res := Precommit(root, recordAllRuns(&seen, func(dir string) bool { return dir == root }))
	if res.Blocked {
		t.Fatalf("an integration test that cannot compile without its source must fail OPEN, got block: %s", res.Message)
	}
	// Prove fail-first actually executed (and failed open): a worktree run occurred.
	var ranFailFirst bool
	for _, r := range seen {
		if r.dir != root {
			ranFailFirst = true
		}
	}
	if !ranFailFirst {
		t.Fatalf("expected a fail-first worktree run to have executed, runs=%+v", seen)
	}
}
