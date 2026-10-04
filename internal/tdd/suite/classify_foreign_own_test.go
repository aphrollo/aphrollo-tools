package suite

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// These are suite's own tests of the foreign-build classifiers in classify.go:
// foreignBuildFailure, matchesAny, foreignBuildAdvisory, editedCargoPackage and
// foreignBuildFailureLine, reached today only through internal/tdd/postedit's
// edit-hook tests.

const linkFailure = "error: linking with `cc` failed\n  = note: rust-lld: error: undefined symbol: widget\n"

func couldNotCompile(crate string) string { return "error: could not compile `" + crate + "` (lib)\n" }

// TestForeignBuildFailure_NamesTheCratesThatFailedToLink pins the verdict: a
// link failure whose failing crates do not include the edited one names them,
// sorted and once each, however many times cargo printed them.
func TestForeignBuildFailure_NamesTheCratesThatFailedToLink(t *testing.T) {
	t.Parallel()
	out := linkFailure + couldNotCompile("zed") + couldNotCompile("beta") + couldNotCompile("zed")
	got := foreignBuildFailure(out, "alpha")
	if want := []string{"beta", "zed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("foreignBuildFailure = %v, want %v", got, want)
	}
}

// TestForeignBuildFailure_TheEditedCrateAmongTheFailuresIsNeverForeign pins the
// conservative direction: when the edited crate is one of the failing ones the
// failure could be the edit's, so nothing is downgraded — wherever in the
// output its line sits.
func TestForeignBuildFailure_TheEditedCrateAmongTheFailuresIsNeverForeign(t *testing.T) {
	t.Parallel()
	out := linkFailure + couldNotCompile("beta") + couldNotCompile("alpha")
	if got := foreignBuildFailure(out, "alpha"); got != nil {
		t.Fatalf("foreignBuildFailure = %v, want nil: alpha is the edited crate", got)
	}
}

// TestForeignBuildFailure_NoEditedCrateJudgesTheRunNormally pins that a run that
// cannot be attributed to a crate is never called foreign.
func TestForeignBuildFailure_NoEditedCrateJudgesTheRunNormally(t *testing.T) {
	t.Parallel()
	if got := foreignBuildFailure(linkFailure+couldNotCompile("beta"), ""); got != nil {
		t.Fatalf("foreignBuildFailure = %v, want nil with no edited crate", got)
	}
}

// TestForeignBuildFailure_WithoutALinkSignatureIsNeverForeign pins that a plain
// compile failure, however it names a crate, stays the session's own.
func TestForeignBuildFailure_WithoutALinkSignatureIsNeverForeign(t *testing.T) {
	t.Parallel()
	if got := foreignBuildFailure(couldNotCompile("beta"), "alpha"); got != nil {
		t.Fatalf("foreignBuildFailure = %v, want nil: no link-step signature", got)
	}
}

// TestForeignBuildFailure_EveryLinkSignatureCounts pins each of the three
// signatures that mark a build as having died at the link step.
func TestForeignBuildFailure_EveryLinkSignatureCounts(t *testing.T) {
	t.Parallel()
	for _, sig := range []string{
		"rust-lld: error: undefined symbol: f",
		"error: linking with `cc` failed: exit status: 1",
		"Undefined symbols for architecture arm64:",
	} {
		got := foreignBuildFailure(sig+"\n"+couldNotCompile("beta"), "alpha")
		if !reflect.DeepEqual(got, []string{"beta"}) {
			t.Errorf("signature %q: foreignBuildFailure = %v, want [beta]", sig, got)
		}
	}
}

// TestForeignBuildFailure_ALinkFailureNamingNoCrateHasNothingToAttribute pins
// the empty answer: with no could-not-compile line there is no crate to call
// foreign.
func TestForeignBuildFailure_ALinkFailureNamingNoCrateHasNothingToAttribute(t *testing.T) {
	t.Parallel()
	if got := foreignBuildFailure(linkFailure, "alpha"); len(got) != 0 {
		t.Fatalf("foreignBuildFailure = %v, want none", got)
	}
}

// TestMatchesAny_ReportsWhetherAnyPatternMatches pins the disjunction: one hit
// among several patterns is a hit, no hit is a miss, and no patterns match
// nothing.
func TestMatchesAny_ReportsWhetherAnyPatternMatches(t *testing.T) {
	t.Parallel()
	res := []*regexp.Regexp{regexp.MustCompile(`alpha`), regexp.MustCompile(`beta`)}
	if !matchesAny(res, "only beta here") {
		t.Fatal("matchesAny = false, want true: the second pattern matches")
	}
	if matchesAny(res, "gamma") {
		t.Fatal("matchesAny = true, want false: neither pattern matches")
	}
	if matchesAny(nil, "alpha") {
		t.Fatal("matchesAny with no patterns = true, want false")
	}
}

// TestEditedCargoPackage_NamesThePackageOwningTheTarget pins the lookup: an
// absolute target inside a member crate resolves to that crate's [package]
// name.
func TestEditedCargoPackage_NamesThePackageOwningTheTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/a\"]\n")
	write(t, root, "crates/a/Cargo.toml", "[package]\nname = \"alpha\"\nversion = \"0.1.0\"\n")
	write(t, root, "crates/a/src/lib.rs", "")
	if got := editedCargoPackage(root, root+"/crates/a/src/lib.rs"); got != "alpha" {
		t.Fatalf("editedCargoPackage = %q, want alpha", got)
	}
}

// TestEditedCargoPackage_NoTargetOwnsNoPackage pins the empty target.
func TestEditedCargoPackage_NoTargetOwnsNoPackage(t *testing.T) {
	t.Parallel()
	if got := editedCargoPackage(t.TempDir(), ""); got != "" {
		t.Fatalf("editedCargoPackage = %q, want empty for no target", got)
	}
}

// TestEditedCargoPackage_APathThatCannotBeRelatedToRootOwnsNoPackage pins the
// error arm: an absolute target against a relative root cannot be related, and
// answers no package rather than guessing.
func TestEditedCargoPackage_APathThatCannotBeRelatedToRootOwnsNoPackage(t *testing.T) {
	t.Parallel()
	if got := editedCargoPackage("relative/root", "/abs/elsewhere/lib.rs"); got != "" {
		t.Fatalf("editedCargoPackage = %q, want empty", got)
	}
}

// TestForeignBuildFailureLine_NamesTheCratesAndSaysNothingWasTested pins the
// line the session reads instead of a RED summary.
func TestForeignBuildFailureLine_NamesTheCratesAndSaysNothingWasTested(t *testing.T) {
	t.Parallel()
	got := foreignBuildFailureLine("/w", []string{"beta", "zed"}, 2500*time.Millisecond)
	for _, part := range []string{InfraFailed, "in /w", "link beta, zed", "in 2.5s", "the code was NOT tested"} {
		if !strings.Contains(got, part) {
			t.Fatalf("line %q lacks %q", got, part)
		}
	}
}

// TestForeignBuildAdvisory_APassingRunHasNothingToSay pins the pass arm.
func TestForeignBuildAdvisory_APassingRunHasNothingToSay(t *testing.T) {
	t.Parallel()
	if got := foreignBuildAdvisory(t.TempDir(), "", "cargo test", SuiteResult{Passed: true, Output: linkFailure}); got != "" {
		t.Fatalf("advisory = %q, want none for a passing run", got)
	}
}

// TestForeignBuildAdvisory_AFailureNamingNoForeignCrateHasNothingToSay pins the
// fall-through: an ordinary failure is judged normally.
func TestForeignBuildAdvisory_AFailureNamingNoForeignCrateHasNothingToSay(t *testing.T) {
	t.Parallel()
	got := foreignBuildAdvisory(t.TempDir(), "", "cargo test", SuiteResult{Output: "assertion failed"})
	if got != "" {
		t.Fatalf("advisory = %q, want none", got)
	}
}

// Serial: reads gate.log under its own CLAUDE_CONFIG_DIR, a process-wide env var.
// TestForeignBuildAdvisory_AForeignLinkFailureIsLoggedAndReported pins the
// verdict: the stand-down line names the crate, and the InfraFailed verdict is
// logged so it is counted, not just printed.
func TestForeignBuildAdvisory_AForeignLinkFailureIsLoggedAndReported(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[package]\nname = \"alpha\"\nversion = \"0.1.0\"\n")
	write(t, root, "src/lib.rs", "")
	res := SuiteResult{Output: linkFailure + couldNotCompile("beta"), Duration: time.Second}

	got := foreignBuildAdvisory(root, root+"/src/lib.rs", "cargo test", res)
	if !strings.Contains(got, "beta") || !strings.Contains(got, "NOT tested") {
		t.Fatalf("advisory = %q, want a stand-down naming beta", got)
	}
	requireLoggedVerdict(t, cfg, InfraFailed)
}
