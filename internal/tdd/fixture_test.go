package tdd

import (
	"os"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// initFixture is the golden initialised repo tddtest.Main built; TestMain
// sets it before the suite runs.
var initFixture string

func mustCopyDir(dst, src string) { tddtest.MustCopyDir(dst, src) }

// A fixture repository is only ever built in a temp dir, and the helper is
// where that is enforced rather than assumed. Under mutation a production
// path that resolves a repo root to "" lets git fall back to the process's
// own working directory — the package's own checkout — and the fixture's
// init/commit/merge then land in the real repository (issue #156). An empty
// or non-temp target is a defect in the caller, so the helper refuses it.
func TestFixtureTarget_RefusesADirectoryOutsideTheOSTempDir(t *testing.T) {
	for _, dst := range []string{"", RepoRoot(".")} {
		if err := fixtureTargetUnderTemp(dst); err == nil {
			t.Errorf("fixtureTargetUnderTemp(%q) = nil, want a refusal — a fixture must never be built there", dst)
		}
	}
}

// The refusal is narrow: the directory every fixture actually uses passes.
func TestFixtureTarget_AcceptsATestTempDir(t *testing.T) {
	if err := fixtureTargetUnderTemp(t.TempDir()); err != nil {
		t.Errorf("a t.TempDir() target must be accepted, got %v", err)
	}
}

func fixtureTargetUnderTemp(dst string) error { return tddtest.FixtureTargetUnderTemp(dst) }

// The fixture helpers spawn git for their own setup, and a session puts the
// queue shim dir FIRST on PATH. The shim re-enters aphrollo, which judges the
// fixture repo by the rules it holds the OPERATOR's checkout to: three tests
// went red on a box with the shim installed, refused with "primary checkout is
// merge-only" for a `git checkout -b` inside a temp-dir fixture. The package
// already resolves the real git for its own subprocesses (gitBinary); the
// fixtures owe the same answer, and get back the ~35 ms per spawn the shim
// costs on the way through.
func TestFixtureGit_RunsTheRealGitNotTheQueueShim(t *testing.T) {
	root := makeGoRepo(t)
	dir, marker := fakeGitShim(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	gitDo(t, root, "checkout", "-q", "-b", "lane")

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the fixture ran the queue shim instead of git")
	}
	if got := gitValue(t, root, "rev-parse", "--abbrev-ref", "HEAD"); got != "lane" {
		t.Fatalf("branch = %q, want lane — the fixture's git did not take", got)
	}
}

// The copy carries an index that was stat'd in a different directory. git
// compares content when the stat cache misses, so the copy still reads clean
// — and it has to: every staged-file assertion in this package starts from a
// fixture that git calls unmodified.
func TestMakeGoRepo_CopiedFixtureIsCleanWithOneCommit(t *testing.T) {
	root := makeGoRepo(t)

	if out := gitValue(t, root, "status", "--porcelain"); out != "" {
		t.Fatalf("a fresh fixture must be clean, got %q", out)
	}
	if n := gitValue(t, root, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("fixture has %s commits, want the single base commit", n)
	}
}

// Each caller gets its OWN repo. A shared one would make the 178 tests that
// commit into it one history, and the order they ran in would decide what
// each of them saw.
func TestMakeGoRepo_IsIndependentPerCall(t *testing.T) {
	a, b := makeGoRepo(t), makeGoRepo(t)
	if a == b {
		t.Fatalf("two fixtures share a directory: %s", a)
	}

	write(t, a, "second.go", "package m\n")
	gitDo(t, a, "add", ".")
	gitDo(t, a, "commit", "-qm", "second")

	if gitValue(t, a, "rev-parse", "HEAD") == gitValue(t, b, "rev-parse", "HEAD") {
		t.Fatal("a commit in one fixture repo reached another")
	}
	if out := gitValue(t, b, "status", "--porcelain"); out != "" {
		t.Fatalf("the untouched fixture must stay clean, got %q", out)
	}
}
