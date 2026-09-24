package lock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tddtest "github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// TestGoTmpRootDir_ResolvesBesideTheWorktreesNeverInsideDir pins issue #532:
// a go child's scratch dir must land beside the primary checkout's parent
// (`<parent>/.worktrees/<repo>/gotmp`), never nested inside dir itself — a
// dir that IS the primary checkout would otherwise land the scratch dir
// under its own tracked tree, where a fixture a `go test` child creates
// inherits dir as its own project root even though the test asked for none.
func TestGoTmpRootDir_ResolvesBesideTheWorktreesNeverInsideDir(t *testing.T) {
	dir := tddtest.MakeGoRepo(t)

	got := GoTmpRootDir(dir)
	if got == "" {
		t.Fatalf("GoTmpRootDir(%q) = \"\", want a resolved path for a real git checkout", dir)
	}
	want := filepath.Join(filepath.Dir(dir), ".worktrees", filepath.Base(dir), "gotmp")
	if got != want {
		t.Fatalf("GoTmpRootDir(%q) = %q, want %q (beside the worktrees, not nested inside dir)", dir, got, want)
	}
}

// TestGoTmpRootDir_EmptyDirRefusesRatherThanGuessing is issue #515's rule: an
// unresolvable primary checkout must return "" rather than falling back to a
// path inside dir, which reproduces the very bug this resolves.
func TestGoTmpRootDir_EmptyDirRefusesRatherThanGuessing(t *testing.T) {
	if got := GoTmpRootDir(""); got != "" {
		t.Fatalf("GoTmpRootDir(\"\") = %q, want \"\" rather than a guessed path", got)
	}
}

// TestGoTmpRootDir_NonGitDirAlsoRefuses is the same #515 rule from the other
// side: a real, non-empty directory that is not inside any git checkout at
// all resolves no primary checkout either, and must refuse the same way as
// an empty dir rather than falling back to a path nested inside it.
func TestGoTmpRootDir_NonGitDirAlsoRefuses(t *testing.T) {
	if got := GoTmpRootDir(t.TempDir()); got != "" {
		t.Fatalf("GoTmpRootDir(a non-git dir) = %q, want \"\" — there is no primary checkout to resolve beside", got)
	}
}

// TestGoTmpDir_IsGoTmpRootDir pins goTmpDir as a plain alias of
// GoTmpRootDir, not a second derivation that could drift from it.
func TestGoTmpDir_IsGoTmpRootDir(t *testing.T) {
	dir := tddtest.MakeGoRepo(t)
	if got, want := goTmpDir(dir), GoTmpRootDir(dir); got != want {
		t.Fatalf("goTmpDir(%q) = %q, want it to equal GoTmpRootDir(%q) = %q", dir, got, dir, want)
	}
}

// TestGoTmpEnv_SetsAllFourTempVarsAndCreatesTheDir pins issue #520/#539: a
// `go` child stages its compiled test binary through GOTMPDIR, but any tool
// it shells out to (or a t.TempDir()/os.TempDir() call inside it) reads
// TMPDIR/TMP/TEMP instead — all four must point at the SAME repo-local
// directory, and that directory must actually exist once goTmpEnv returns,
// not merely be named.
func TestGoTmpEnv_SetsAllFourTempVarsAndCreatesTheDir(t *testing.T) {
	dir := tddtest.MakeGoRepo(t)
	want := GoTmpRootDir(dir)

	env := goTmpEnv(dir)

	got := map[string]string{}
	for _, kv := range env {
		for _, key := range []string{"GOTMPDIR", "TMPDIR", "TMP", "TEMP"} {
			if rest, ok := strings.CutPrefix(kv, key+"="); ok {
				got[key] = rest
			}
		}
	}
	for _, key := range []string{"GOTMPDIR", "TMPDIR", "TMP", "TEMP"} {
		if got[key] != want {
			t.Errorf("goTmpEnv %s = %q, want %q", key, got[key], want)
		}
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("goTmpEnv did not create %q as a directory: %v", want, err)
	}
}

// TestGoTmpEnv_EmptyDirYieldsNoEnvAtAll is goTmpEnv's own refusal: when
// GoTmpRootDir cannot resolve a scratch dir, goTmpEnv must add nothing
// rather than pointing the temp vars at an empty string.
func TestGoTmpEnv_EmptyDirYieldsNoEnvAtAll(t *testing.T) {
	if env := goTmpEnv(""); env != nil {
		t.Fatalf("goTmpEnv(\"\") = %v, want nil — nothing to set when there is no resolved scratch dir", env)
	}
}
