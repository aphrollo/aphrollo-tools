package suite

import (
	"path/filepath"
	"testing"
)

// These are suite's own tests of the run-root resolution in runcheckout.go
// that only internal/tdd/postedit's suite-guard tests reach today.

// runRoots lays down a repo with two nested Go project roots: a/ and b/, each
// its own module, under one outer module.
func runRoots(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	write(t, repo, "go.mod", "module example.com/top\n\ngo 1.26\n")
	write(t, repo, "a/go.mod", "module example.com/a\n\ngo 1.26\n")
	write(t, repo, "b/go.mod", "module example.com/b\n\ngo 1.26\n")
	return repo
}

// TestEffectiveRunRoot_ACdFollowedBySuiteRunLandsInTheCdDirectorysRoot pins the
// core answer: the run's own directory, not the session's, decides the root.
func TestEffectiveRunRoot_ACdFollowedBySuiteRunLandsInTheCdDirectorysRoot(t *testing.T) {
	t.Parallel()
	repo := runRoots(t)
	if got, want := effectiveRunRoot(repo, "cd a && go test ./..."), filepath.Join(repo, "a"); got != want {
		t.Fatalf("effectiveRunRoot = %q, want %q", got, want)
	}
}

// TestEffectiveRunRoot_TheRunnersOwnDirectoryFlagDecidesTheRoot pins the -C and
// --manifest-path flags: the manifest's directory, not the file itself.
func TestEffectiveRunRoot_TheRunnersOwnDirectoryFlagDecidesTheRoot(t *testing.T) {
	t.Parallel()
	repo := runRoots(t)
	a := filepath.Join(repo, "a")
	for _, cmd := range []string{
		"go test -C b ./...",
		"go test -C=b ./...",
		"cargo test --manifest-path b/Cargo.toml",
		"cargo test --manifest-path=b/Cargo.toml",
	} {
		if got, want := effectiveRunRoot(repo, cmd), filepath.Join(repo, "b"); got != want {
			t.Errorf("%q: effectiveRunRoot = %q, want %q", cmd, got, want)
		}
	}
	if got := effectiveRunRoot(a, "go test ./..."); got != a {
		t.Errorf("no flag: effectiveRunRoot = %q, want the cwd's own root %q", got, a)
	}
}

// TestEffectiveRunRoot_ADirectoryFlagWithNoValueIsUnknown pins the malformed
// case: a trailing -C names no directory, so no root is claimed.
func TestEffectiveRunRoot_ADirectoryFlagWithNoValueIsUnknown(t *testing.T) {
	t.Parallel()
	repo := runRoots(t)
	if got := effectiveRunRoot(repo, "go test -C"); got != "" {
		t.Fatalf("effectiveRunRoot = %q, want none", got)
	}
}

// TestEffectiveRunRoot_ARunDirectoryThatDoesNotExistIsUnknown pins the
// existence guard: a cd into a path that is not there names no tree.
func TestEffectiveRunRoot_ARunDirectoryThatDoesNotExistIsUnknown(t *testing.T) {
	t.Parallel()
	repo := runRoots(t)
	if got := effectiveRunRoot(repo, "cd nowhere && go test ./..."); got != "" {
		t.Fatalf("effectiveRunRoot = %q, want none", got)
	}
}

// TestEffectiveRunRoot_ARunDirectoryThatIsAFileIsUnknown pins the other half of
// runDirExists: a path that exists as a file is no directory.
func TestEffectiveRunRoot_ARunDirectoryThatIsAFileIsUnknown(t *testing.T) {
	t.Parallel()
	repo := runRoots(t)
	write(t, repo, "afile", "x")
	if got := effectiveRunRoot(repo, "cd afile && go test ./..."); got != "" {
		t.Fatalf("effectiveRunRoot = %q, want none", got)
	}
}

// TestEffectiveRunRoot_AnUnfollowedDirectoryChangeMakesTheRunUnknown pins the
// pushd/popd arm: after a directory change the scanner does not follow, the
// later run's directory is unknown. A cd inside a subshell or brace group is
// followed instead (runcheckout_groups_test.go).
func TestEffectiveRunRoot_AnUnfollowedDirectoryChangeMakesTheRunUnknown(t *testing.T) {
	t.Parallel()
	repo := runRoots(t)
	for _, cmd := range []string{
		"pushd a && go test ./...",
		"popd && go test ./...",
	} {
		if got := effectiveRunRoot(repo, cmd); got != "" {
			t.Errorf("%q: effectiveRunRoot = %q, want none", cmd, got)
		}
	}
}

// TestEffectiveRunRoot_RunsInTwoDifferentRootsAreUnknown pins the agreement
// rule: every invocation must land in the same root.
func TestEffectiveRunRoot_RunsInTwoDifferentRootsAreUnknown(t *testing.T) {
	t.Parallel()
	repo := runRoots(t)
	if got := effectiveRunRoot(repo, "cd a && go test ./... && cd ../b && go test ./..."); got != "" {
		t.Fatalf("effectiveRunRoot = %q, want none for runs in a and b", got)
	}
}

// TestEffectiveRunRoot_RunsInTheSameRootAgree pins the positive half: two runs
// in one root answer it.
func TestEffectiveRunRoot_RunsInTheSameRootAgree(t *testing.T) {
	t.Parallel()
	repo := runRoots(t)
	if got := effectiveRunRoot(repo, "go test ./x && go test ./y"); got != repo {
		t.Fatalf("effectiveRunRoot = %q, want %q", got, repo)
	}
}

// TestEffectiveRunRoot_ANestedRootIsNotTheSameRootAsItsParent pins sameRoot's
// two-way check: a run in the outer root and one in a nested root disagree,
// whichever comes first.
func TestEffectiveRunRoot_ANestedRootIsNotTheSameRootAsItsParent(t *testing.T) {
	t.Parallel()
	repo := runRoots(t)
	for _, cmd := range []string{
		"go test ./... && cd a && go test ./...",
		"cd a && go test ./... && cd .. && go test ./...",
	} {
		if got := effectiveRunRoot(repo, cmd); got != "" {
			t.Errorf("%q: effectiveRunRoot = %q, want none", cmd, got)
		}
	}
}

// TestSameRoot_IsEqualityNotContainment pins the helper on its own.
func TestSameRoot_IsEqualityNotContainment(t *testing.T) {
	t.Parallel()
	if !sameRoot("/r/a", "/r/a") {
		t.Error("a root is the same as itself")
	}
	if sameRoot("/r", "/r/a") || sameRoot("/r/a", "/r") {
		t.Error("a parent and its child are different roots, in either order")
	}
}

// TestInvocationDir_TheFirstDirectoryFlagIsTheAnswer pins the scan: the first
// runner flag naming a directory decides, resolved against the current dir.
func TestInvocationDir_TheFirstDirectoryFlagIsTheAnswer(t *testing.T) {
	t.Parallel()
	cur := filepath.FromSlash("/repo")
	if got := invocationDir(cur, []string{"go", "test", "-C", "x", "-C", "y"}); got != filepath.FromSlash("/repo/x") {
		t.Errorf("first flag: %q, want /repo/x", got)
	}
	if got := invocationDir(cur, []string{"go", "test", "./..."}); got != cur {
		t.Errorf("no flag: %q, want the current dir", got)
	}
	if got := invocationDir(cur, []string{"go", "test", "-C"}); got != "" {
		t.Errorf("flag with no value: %q, want none", got)
	}
	if got := invocationDir(cur, []string{"cargo", "test", "--manifest-path", "sub/Cargo.toml"}); got != filepath.FromSlash("/repo/sub") {
		t.Errorf("manifest flag: %q, want the manifest's directory", got)
	}
}
