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
