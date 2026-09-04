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
	if got := cargoPackagesInArgs([]string{"test", "-p"}); len(got) != 0 {
		t.Errorf("cargoPackagesInArgs = %q, want none — a -p with no value names no package", got)
	}
}

// TestCargoPackagesInArgs_ReadsEveryNamedPackage covers the ordinary case and
// both spellings, and pins that a flag's VALUE is never itself read as a
// package name.
func TestCargoPackagesInArgs_ReadsEveryNamedPackage(t *testing.T) {
	got := cargoPackagesInArgs([]string{"test", "-p", "movement", "--package", "terrain", "--test", "floor"})

	if want := []string{"movement", "terrain"}; !slices.Equal(got, want) {
		t.Errorf("cargoPackagesInArgs = %q, want %q — --test's value is not a package", got, want)
	}
}

// TestCargoPackagesInArgs_ReadsNothingFromAnArgvWithNoPackages guards the
// other direction: no -p means nothing to invalidate, never a blanket clean.
func TestCargoPackagesInArgs_ReadsNothingFromAnArgvWithNoPackages(t *testing.T) {
	if got := cargoPackagesInArgs([]string{"test", "--workspace"}); len(got) != 0 {
		t.Errorf("cargoPackagesInArgs = %q, want none", got)
	}
}
