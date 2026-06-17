package tdd

import (
	"os"
	"os/exec"
	"path/filepath"
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
