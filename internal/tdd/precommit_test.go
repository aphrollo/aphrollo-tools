package tdd

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// precommitTestTimeout bounds the real `go test` runs in these integration
// tests.
const precommitTestTimeout = 120 * time.Second

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

func gitInit(t *testing.T, dir string) {
	t.Helper()
	// Isolate git config so the operator box's global core.hooksPath (the
	// aphrollo tdd gate) does not recurse into this fixture's setup commits.
	isolateGitConfig(t)
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func gitDo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

// makeGoRepo creates a committed Go module with a passing baseline, then stages
// the given new files, returning the repo root.
func makeGoRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	write(t, root, "doc.go", "package m\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root
}

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
	root := makeGoRepo(t)
	// The test references Widget(), which does not exist at HEAD → it fails to
	// compile without the staged source → fail-first satisfied → allowed.
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif Widget() != 1 { t.Fatal(\"no\") }\n}\n")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if res.Blocked {
		t.Fatalf("a test that needs the impl must pass fail-first, got blocked: %s", res.Message)
	}
}

func TestPrecommit_BlocksNewlyAddedSuppression(t *testing.T) {
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
func TestPrecommit_MaskingBypass_FullFilePostImage(t *testing.T) {
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
	write(t, root, "gizmo.go", "package m\n\nfunc Gizmo() int {\n\t/* note\nstray \"\n\t*/\n\t_ = 0 //nolint:unused\n\treturn 1\n}\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "anti-cheat") {
		t.Fatalf("crafted multi-line edit bypassed the suppression gate, got %+v", res)
	}
}

func TestPrecommit_Mechanical_BlocksFailingSuite(t *testing.T) {
	root := makeGoRepo(t)
	// Source-only change (no staged test) that breaks the build → mechanical
	// gate blocks; fail-first does not trigger.
	write(t, root, "broken.go", "package m\n\nfunc Broken() int { return }\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "mechanical") {
		t.Fatalf("expected mechanical block, got %+v", res)
	}
}

// recordRunner is a SuiteRunner that records every Runner it executes and always
// reports passing — so a test can assert the EXACT mechanical argv without a real
// suite run. The fail-first worktree run (if any) is recorded too, but the
// mechanical stage runs against repoRoot, so the test keys off root.
func recordRunner(seen *[]Runner, root string) SuiteRunner {
	return func(r Runner, dir string) SuiteResult {
		if dir == root {
			*seen = append(*seen, r)
		}
		return SuiteResult{Passed: true}
	}
}

func TestPrecommit_Mechanical_ScopedToStagedGoPackages(t *testing.T) {
	root := makeGoRepo(t)
	// Stage a source file in a sub-package; the mechanical run must scope to that
	// package, not `./...`.
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	res := Precommit(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if len(seen) != 1 {
		t.Fatalf("expected one mechanical run at root, got %d: %+v", len(seen), seen)
	}
	want := Runner{"go", []string{"test", "./internal/x"}}
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
	res := Precommit(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if len(seen) != 1 {
		t.Fatalf("expected one scoped mechanical run, got %d: %+v", len(seen), seen)
	}
	want := Runner{"go", []string{"test", "./internal/x"}}
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
	res := Precommit(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := Runner{"npx", []string{"vitest", "related", "src/widget.ts", "--run"}}
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
	res := Precommit(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := Runner{"npx", []string{"jest", "--findRelatedTests", "src/widget.js"}}
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
	res := Precommit(root, recordRunner(&seen, root))
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := Runner{"npm", []string{"test", "--silent"}}
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

// makeCargoRepo creates a committed Rust crate whose root marker is Cargo.toml.
// Like makeJSRepo, the suite is always faked (cargo need not be installed) —
// only DetectRunner's marker read and the git state matter.
func makeCargoRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\n")
	write(t, root, "src/lib.rs", "pub fn base() -> i32 { 0 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root
}

// A green mechanical run must be remembered: a second Precommit over the
// IDENTICAL worktree state and runner must not re-run the suite (the retry
// after a hook timeout, or an amend that changes nothing tested).
func TestPrecommit_Mechanical_GreenResultCached(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	var seen []Runner
	if res := Precommit(root, recordRunner(&seen, root)); res.Blocked {
		t.Fatalf("first run must not block: %s", res.Message)
	}
	if res := Precommit(root, recordRunner(&seen, root)); res.Blocked {
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
	Precommit(root, recordRunner(&seen, root))
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 2 }\n")
	gitDo(t, root, "add", ".")
	Precommit(root, recordRunner(&seen, root))
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

	if got := PostEdit(postPayload("Edit", filepath.Join(root, "src", "root.zig")),
		fakeRun(true, "All 1 tests passed.")); got != "" {
		t.Fatalf("green post-edit must be silent, got: %s", got)
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

// The fail-first worktree run must not inherit the operator's
// CARGO_TARGET_DIR: a shared warm target can hold stale artifacts from a
// divergent sibling checkout and fail the gate on phantom compile errors. The
// gate pins its own per-repo target dir under the state dir for the worktree
// run — and restores the operator's value before the mechanical run, which
// runs in the real checkout where the shared warm target is correct.
func TestPrecommit_FailFirst_PinsCargoTargetDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("CARGO_TARGET_DIR", "/tmp/shared-warm-target")
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
		// The applied test cannot compile without the staged source → RED,
		// which satisfies fail-first.
		return SuiteResult{Passed: false, Output: "error[E0425]: cannot find function `widget`"}
	}
	if res := Precommit(root, run); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if worktreeTarget == "/tmp/shared-warm-target" || worktreeTarget == "" {
		t.Fatalf("fail-first worktree run inherited the shared CARGO_TARGET_DIR: %q", worktreeTarget)
	}
	if !strings.HasPrefix(worktreeTarget, cfg) {
		t.Fatalf("pinned target dir must live under the state dir %s, got %q", cfg, worktreeTarget)
	}
	if mechanicalTarget != "/tmp/shared-warm-target" {
		t.Fatalf("mechanical run must keep the operator's CARGO_TARGET_DIR, got %q", mechanicalTarget)
	}
	if got := os.Getenv("CARGO_TARGET_DIR"); got != "/tmp/shared-warm-target" {
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
		// conclusive fail-first outcome.
		return SuiteResult{Passed: false, Output: "undefined: Widget"}
	}
	for i := 0; i < 2; i++ {
		if _, conclusive := failFirstViolated(root, []string{"widget_test.go"}, run); !conclusive {
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
// assert whether fail-first executed at all.
func recordAllRuns(seen *[]loggedRun, pass func(dir string) bool) SuiteRunner {
	return func(r Runner, dir string) SuiteResult {
		*seen = append(*seen, loggedRun{runner: r, dir: dir})
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
	res := Precommit(root, recordAllRuns(&seen, func(string) bool { return true }))
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
