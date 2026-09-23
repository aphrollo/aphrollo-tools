package tdd

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

const precommitTestTimeout = tddtest.PrecommitTestTimeout

func TestSplitKinds(t *testing.T) {
	tests, srcs := splitKinds([]string{"a_test.go", "a.go", "README.md", "b.test.ts", "b.ts"})
	if strings.Join(tests, ",") != "a_test.go,b.test.ts" {
		t.Fatalf("tests = %v", tests)
	}
	if strings.Join(srcs, ",") != "a.go,b.ts" {
		t.Fatalf("srcs = %v", srcs)
	}
}

func TestCleanGitEnv_StripsGitVars(t *testing.T) {
	t.Setenv("GIT_DIR", "/outer/.git")
	t.Setenv("GIT_INDEX_FILE", "/outer/.git/index")
	t.Setenv("KEEP_ME", "1")
	for _, kv := range cleanGitEnv() {
		if strings.HasPrefix(kv, "GIT_") {
			t.Fatalf("cleanGitEnv leaked %q", kv)
		}
	}
	var kept bool
	for _, kv := range cleanGitEnv() {
		if kv == "KEEP_ME=1" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("cleanGitEnv dropped a non-git var")
	}
}

// --- real-git integration: the fail-first worktree path ---------------------

func gitInit(t *testing.T, dir string) { t.Helper(); tddtest.GitInit(t, dir) }

func write(t *testing.T, dir, rel, content string) { t.Helper(); tddtest.Write(t, dir, rel, content) }

func gitDo(t *testing.T, dir string, args ...string) { t.Helper(); tddtest.GitDo(t, dir, args...) }

func makeGoRepo(t *testing.T) string { t.Helper(); return tddtest.MakeGoRepo(t) }

// makeJSRepo creates a committed repo whose only root marker is package.json,
// with the given package.json contents (which select the detected runner). The
// suite is never actually run in these tests — recordRunner fakes it — so npx
// need not be present; only DetectRunner's marker read matters. git presence is
// enforced by gitInit (it fatals if git can't run).
func makeJSRepo(t *testing.T, pkgJSON string) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "package.json", pkgJSON)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root
}

func TestPrecommit_FailFirst_BlocksTestThatPassesWithoutImpl(t *testing.T) {
	withLinter(t, false)
	root := makeGoRepo(t)
	// A test that asserts nothing about new code — it passes against HEAD.
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) { _ = 1 }\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "fail-first") {
		t.Fatalf("expected fail-first block, got %+v", res)
	}
}

func TestPrecommit_FailFirst_AllowsTestThatNeedsImpl(t *testing.T) {
	withLinter(t, false)
	root := makeGoRepo(t)
	// The test references Widget(), which does not exist at HEAD → it fails to
	// compile without the staged source → fail-first satisfied → allowed.
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if res.Blocked {
		t.Fatalf("a test that needs the impl must pass fail-first, got blocked: %s", res.Message)
	}
}

func TestPrecommit_BlocksNewlyAddedSuppression(t *testing.T) {
	withLinter(t, false)
	root := makeGoRepo(t)
	// A compiling source file whose only sin is a freshly-added linter
	// suppression: mechanical would pass, but the anti-cheat gate blocks first.
	write(t, root, "gizmo.go", "package m\n\nfunc Gizmo() int { return 1 } //nolint:unused\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "anti-cheat") {
		t.Fatalf("expected anti-cheat block for a new suppression, got %+v", res)
	}
}

func TestPrecommit_IgnoresPreexistingSuppression(t *testing.T) {
	withLinter(t, false)
	root := makeGoRepo(t)
	// Commit a file that already carries a suppression.
	write(t, root, "old.go", "package m\n\nfunc Old() int { return 2 } //nolint:unused\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "old")
	// Now stage an unrelated, clean change. The pre-existing suppression in
	// old.go is NOT in this diff, so it must not block.
	write(t, root, "clean.go", "package m\n\nfunc Clean() int { return 3 }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if res.Blocked {
		t.Fatalf("a pre-existing suppression must not block a clean commit, got %+v", res)
	}
}

// A crafted multi-line edit must not smuggle a suppression past the anti-cheat
// gate. The added-only diff buffer, masked standalone, sees an unbalanced quote
// on the first added line and blanks everything after it — including a //nolint
// on a LATER added line. Masking the full post-image instead keeps the quote
// balanced (its partner is an unchanged line) so the suppression stays visible
// and blocks. The inert quote lives inside a pre-existing block comment, so the
// file still compiles and mechanical alone would let it through.
// (reason: this is the exact case the bypass test below exists to catch.)
func TestPrecommit_MaskingBypass_FullFilePostImage(t *testing.T) {
	withLinter(t, false)
	root := makeGoRepo(t)
	// Base: a func carrying an empty block comment whose */ closer is committed.
	write(t, root, "gizmo.go", "package m\n\nfunc Gizmo() int {\n\t/* note\n\t*/\n\treturn 1\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "gizmo")

	// Stage: insert a line bearing a lone " INSIDE the existing comment (inert,
	// still compiles), then add a //nolint line AFTER the comment closes. In the
	// added-only buffer the lone " opens an unterminated string that hides the
	// //nolint; in the full file the " sits inside the comment and the //nolint
	// is live code.
	// (reason: this is the payload proving full-file masking still catches it.)
	write(t, root, "gizmo.go", "package m\n\nfunc Gizmo() int {\n\t/* note\nstray \"\n\t*/\n\t_ = 0 //nolint:unused\n\treturn 1\n}\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "anti-cheat") {
		t.Fatalf("crafted multi-line edit bypassed the suppression gate, got %+v", res)
	}
}

func TestPrecommit_Mechanical_BlocksFailingSuite(t *testing.T) {
	withLinter(t, false)
	root := makeGoRepo(t)
	// A committed test that passes, then a source-only change that breaks it:
	// the code still compiles and vets clean, so the SUITE is what rejects,
	// and fail-first does not trigger (no staged test).
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	write(t, root, "widget_test.go",
		"package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 {\n\t\tt.Fatal(\"boom\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "widget")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 2 }\n")
	gitDo(t, root, "add", ".")

	res := Mechanical(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "mechanical") {
		t.Fatalf("expected mechanical block, got %+v", res)
	}
}

func recordRunner(seen *[]Runner, root string) SuiteRunner {
	return tddtest.RecordRunner(seen, root, recordableRun, SuiteResult{Passed: true})
}

// recordableRun keeps a suite run for recordRunner, Deadline stripped, and
// drops a quality run.
func recordableRun(r Runner) (Runner, bool) {
	if isQualityRunner(r) {
		return r, false
	}
	r.Deadline = time.Time{}
	return r, true
}

func TestPrecommit_Mechanical_ScopedToStagedGoPackages(t *testing.T) {
	root := makeGoRepo(t)
	// Stage a source file in a sub-package; the mechanical run must scope to that
	// package, not `./...`.
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if len(seen) != 1 {
		t.Fatalf("expected one mechanical run at root, got %d: %+v", len(seen), seen)
	}
	want := Runner{Cmd: "go", Args: []string{"test", "-race", "-count=1", "-shuffle=on", "./internal/x"}, Dir: "", Deadline: time.Time{}}
	if !reflect.DeepEqual(seen[0], want) {
		t.Fatalf("mechanical runner = %+v, want %+v", seen[0], want)
	}
}

func TestPrecommit_ChangesGate_SkipsDocsOnlyCommit(t *testing.T) {
	root := makeGoRepo(t)
	// Only a doc file is staged — no source, no test. The mechanical stage must
	// be skipped entirely (no suite run).
	write(t, root, "NOTES.md", "# notes\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Precommit(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("docs-only commit must not block: %s", res.Message)
	}
	if len(seen) != 0 {
		t.Fatalf("docs-only commit must not run the suite, ran %+v", seen)
	}
}

// TestPrecommit_Mechanical_ScopedToStagedGoTestOnly guards the test-only commit:
// staging just a *_test.go must run the SCOPED package command, not the full
// `./...` suite. The fail-first stage never triggers (no staged source), so the
// only run recorded at root is the scoped mechanical one.
func TestPrecommit_Mechanical_ScopedToStagedGoTestOnly(t *testing.T) {
	root := makeGoRepo(t)
	// A self-contained test in a sub-package — no source file staged alongside it.
	write(t, root, "internal/x/x_test.go", "package x\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) { _ = 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if len(seen) != 1 {
		t.Fatalf("expected one scoped mechanical run, got %d: %+v", len(seen), seen)
	}
	want := Runner{Cmd: "go", Args: []string{"test", "-race", "-count=1", "-shuffle=on", "./internal/x"}, Dir: "", Deadline: time.Time{}}
	if !reflect.DeepEqual(seen[0], want) {
		t.Fatalf("test-only mechanical runner = %+v, want %+v", seen[0], want)
	}
}

// TestPrecommit_Mechanical_ScopedToStagedVitest guards the vitest scoping path:
// a staged source file in a vitest repo runs `vitest related <files> --run`, not
// the full `vitest run`. The runner is selected by DetectRunner from the repo's
// package.json, exactly as it is in production.
func TestPrecommit_Mechanical_ScopedToStagedVitest(t *testing.T) {
	root := makeJSRepo(t, `{"devDependencies":{"vitest":"^1.0.0"}}`)
	write(t, root, "src/widget.ts", "export const widget = () => 1\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := Runner{Cmd: "npx", Args: []string{"vitest", "related", "src/widget.ts", "--run"}, Dir: "", Deadline: time.Time{}}
	if len(seen) != 1 || !reflect.DeepEqual(seen[0], want) {
		t.Fatalf("vitest mechanical runs = %+v, want one %+v", seen, want)
	}
}

// TestPrecommit_Mechanical_ScopedToStagedJest guards the jest scoping path: a
// staged source file in a jest repo runs `jest --findRelatedTests <files>`, not
// the full `jest`.
func TestPrecommit_Mechanical_ScopedToStagedJest(t *testing.T) {
	root := makeJSRepo(t, `{"devDependencies":{"jest":"^29.0.0"}}`)
	write(t, root, "src/widget.js", "module.exports = () => 1\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := Runner{Cmd: "npx", Args: []string{"jest", "--findRelatedTests", "src/widget.js"}, Dir: "", Deadline: time.Time{}}
	if len(seen) != 1 || !reflect.DeepEqual(seen[0], want) {
		t.Fatalf("jest mechanical runs = %+v, want one %+v", seen, want)
	}
}

// TestPrecommit_Mechanical_UnknownRunnerFullSuiteFallback guards the fallback: a
// repo whose package.json selects the generic `npm test` script (no vitest/jest)
// has no related mode, so the mechanical stage runs the FULL command unchanged.
func TestPrecommit_Mechanical_UnknownRunnerFullSuiteFallback(t *testing.T) {
	root := makeJSRepo(t, `{"scripts":{"test":"echo ok"}}`)
	write(t, root, "src/widget.ts", "export const widget = () => 1\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Mechanical(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := Runner{Cmd: "npm", Args: []string{"test", "--silent"}, Dir: "", Deadline: time.Time{}}
	if len(seen) != 1 || !reflect.DeepEqual(seen[0], want) {
		t.Fatalf("fallback mechanical runs = %+v, want one full-suite %+v", seen, want)
	}
}

// TestPrecommit_ChangesGate_SkipsYAMLOnlyCommit mirrors the docs-only skip for a
// yaml-only (Ignore-classified) commit: no source AND no test staged, so the
// changes-gate skips the mechanical stage entirely (zero suite runs).
func TestPrecommit_ChangesGate_SkipsYAMLOnlyCommit(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "config.yaml", "key: value\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Precommit(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("yaml-only commit must not block: %s", res.Message)
	}
	if len(seen) != 0 {
		t.Fatalf("yaml-only commit must not run the suite, ran %+v", seen)
	}
}

// --- mechanical green cache ---------------------------------------------------

func makeCargoRepo(t *testing.T) string { t.Helper(); return tddtest.MakeCargoRepo(t) }

// A green mechanical run must be remembered: a second Precommit over the
// IDENTICAL worktree state and runner must not re-run the suite (the retry
// after a hook timeout, or an amend that changes nothing tested).
func TestPrecommit_Mechanical_GreenResultCached(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	if res := Mechanical(root, recordRunner(&seen, root)); res.Blocked {
		t.Fatalf("first run must not block: %s", res.Message)
	}
	if res := Mechanical(root, recordRunner(&seen, root)); res.Blocked {
		t.Fatalf("second run must not block: %s", res.Message)
	}
	if len(seen) != 1 {
		t.Fatalf("identical state must reuse the green result, suite ran %d times: %+v", len(seen), seen)
	}
}

// Any content change invalidates the cached green — the hash covers the
// worktree, so an edit between commits forces a fresh run.
func TestPrecommit_Mechanical_CacheMissAfterEdit(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	Mechanical(root, recordRunner(&seen, root))
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 2 }\n")
	gitDo(t, root, "add", ".")
	Mechanical(root, recordRunner(&seen, root))
	if len(seen) != 2 {
		t.Fatalf("an edited worktree must re-run the suite, ran %d times", len(seen))
	}
}

// A red run is never cached: the same failing state re-runs (and re-blocks
// with fresh output) every time.
func TestPrecommit_Mechanical_RedNeverCached(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var runs int
	failing := func(r Runner, dir string) SuiteResult {
		if dir == root {
			runs++
		}
		return SuiteResult{Passed: false, Output: "--- FAIL: TestX"}
	}
	for i := 0; i < 2; i++ {
		if res := Precommit(root, failing); !res.Blocked {
			t.Fatal("failing suite must block")
		}
	}
	if runs != 2 {
		t.Fatalf("a red result must never be cached, suite ran %d times", runs)
	}
}

// A green PostToolUse run seeds the cache for the commit that follows: in a
// cargo repo both hooks run the identical full `cargo test`, so the gate must
// not charge the suite twice for the same worktree state (the feedback's
// "reruns the full workspace suite I just ran green").
// The fixture is zig because seeding only pays off when the per-edit command
// IS the commit-time command (`zig build test` on both sides). Cargo no longer
// qualifies: PostEdit narrows an edit to `--lib`/`--test <name>` while the
// commit gate runs `-p <crate>`, and a narrower green must never satisfy the
// broader check.
func TestPostEdit_GreenRunSeedsMechanicalCache(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeZigRepo(t)
	write(t, root, "src/root.zig", "pub fn add(a: i32, b: i32) i32 {\n\treturn a + b + 0;\n}\n")

	// UPDATED for task A2 (2026-08-15): PostEdit is no longer silent on green
	// (silence made it indistinguishable from "the hook never ran"); it must
	// still seed the mechanical cache exactly as before.
	if got := PostEdit(postPayload("Edit", filepath.Join(root, "src", "root.zig")),
		fakeRun(true, "All 1 tests passed.")); !strings.Contains(got, "→ green") {
		t.Fatalf("green post-edit must report green, got: %s", got)
	}

	gitDo(t, root, "add", ".")
	var seen []Runner
	if res := Precommit(root, recordRunner(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if len(seen) != 0 {
		t.Fatalf("the green post-edit run must seed the mechanical cache, suite re-ran: %+v", seen)
	}
}
