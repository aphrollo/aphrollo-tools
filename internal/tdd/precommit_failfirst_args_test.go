package tdd

import (
	"slices"
	"testing"
)

// cargoPackagesInArgs reads the -p values out of the fail-first runner's own
// argv, which is what decides WHICH packages get invalidated afterwards. Its
// loop reads args[i+1], so the guard is a real bound: a trailing `-p` with no
// value must yield nothing rather than read past the end.
func TestCargoPackagesInArgs_StopsAtATrailingFlagWithNoValue(t *testing.T) {
	t.Parallel()
	if got := cargoPackagesInArgs([]string{"test", "-p"}); len(got) != 0 {
		t.Errorf("cargoPackagesInArgs = %q, want none — a -p with no value names no package", got)
	}
}

// TestCargoPackagesInArgs_ReadsEveryNamedPackage covers the ordinary case and
// both spellings, and pins that a flag's VALUE is never itself read as a
// package name.
func TestCargoPackagesInArgs_ReadsEveryNamedPackage(t *testing.T) {
	t.Parallel()
	got := cargoPackagesInArgs([]string{"test", "-p", "movement", "--package", "terrain", "--test", "floor"})

	if want := []string{"movement", "terrain"}; !slices.Equal(got, want) {
		t.Errorf("cargoPackagesInArgs = %q, want %q — --test's value is not a package", got, want)
	}
}

// TestCargoPackagesInArgs_ReadsNothingFromAnArgvWithNoPackages guards the
// other direction: no -p means nothing to invalidate, never a blanket clean.
func TestCargoPackagesInArgs_ReadsNothingFromAnArgvWithNoPackages(t *testing.T) {
	t.Parallel()
	if got := cargoPackagesInArgs([]string{"test", "--workspace"}); len(got) != 0 {
		t.Errorf("cargoPackagesInArgs = %q, want none", got)
	}
}

// TestFailFirstInvalidationPackages_CoversADependencyCrateNeverNamedWithP
// pins harryberg1n/borld#496: a commit stages a new test in crate "alpha" and
// non-test source in crate "beta", which alpha depends on. narrowFailFirstTests
// picks -p purely from the staged TEST files, so the fail-first argv names
// only alpha -- yet the fail-first worktree builds beta from HEAD's OLD
// source (only the staged TEST diff is applied there), and that stale rlib
// lands in the shared target dir with a fresh mtime. The invalidation set
// must cover beta too, or the mechanical stage reads the stale artifact as
// current.
func TestFailFirstInvalidationPackages_CoversADependencyCrateNeverNamedWithP(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\", \"crates/beta\"]\n")
	write(t, root, "crates/alpha/Cargo.toml", "[package]\nname = \"alpha\"\nversion = \"0.1.0\"\n")
	write(t, root, "crates/beta/Cargo.toml", "[package]\nname = \"beta\"\nversion = \"0.1.0\"\n")

	failFirstArgs := []string{"test", "-p", "alpha", "--test", "foo"}
	srcs := []string{"crates/beta/src/lib.rs"}

	got := failFirstInvalidationPackages(failFirstArgs, root, root, srcs)
	want := []string{"alpha", "beta"}
	if !slices.Equal(got, want) {
		t.Fatalf("failFirstInvalidationPackages = %q, want %q — beta owns staged non-test source and must be invalidated even though it was never named with -p", got, want)
	}
}

// TestFailFirstInvalidationPackages_DedupesAPackageNamedBothWays guards the
// overlap: a package named with -p AND owning staged non-test source appears
// once, never twice in the cargo clean argv this feeds.
func TestFailFirstInvalidationPackages_DedupesAPackageNamedBothWays(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"pkg1\"\nversion = \"0.1.0\"\n")

	failFirstArgs := []string{"test", "-p", "pkg1"}
	srcs := []string{"src/lib.rs"}

	got := failFirstInvalidationPackages(failFirstArgs, root, root, srcs)
	want := []string{"pkg1"}
	if !slices.Equal(got, want) {
		t.Fatalf("failFirstInvalidationPackages = %q, want %q — no duplicate entry", got, want)
	}
}
