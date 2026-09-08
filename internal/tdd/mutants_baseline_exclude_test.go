package tdd

import (
	"strings"
	"testing"
)

// A declared exclusion reaches the SAME nextest passthrough flag whether the
// call is building a baseline run's own argv or a mutant run's — cargo-mutants
// invokes the same test command for both, so one flag placed after MutantsArgv's
// own `--` is what makes one declared exclusion cover both phases (issue #265).
// ratchet: test_removed TestMutantsProducerFlags_CarriesTheDeclaredExclusionForBothBaselineAndMutantTesting: renamed with the function it tests, mutantsProducerFlags -> MutantsArgv; the claim is unchanged
// ratchet: test_removed TestMutantsProducerFlags_OmitsTheNextestPassthroughWhenNoExclusionIsDeclared: renamed with the function it tests, mutantsProducerFlags -> MutantsArgv; the claim is unchanged
func TestMutantsArgv_CarriesTheDeclaredExclusionAfterThePassthrough(t *testing.T) {
	t.Parallel()
	expr, _, _ := mutationBaselineExcludeParse([]string{"test(conditioner_burst) # box-contended wall-clock test"})

	got := strings.Join(MutantsArgv("lane.diff", 120, nil, expr), " ")

	if !strings.HasSuffix(got, "-- -E not(test(conditioner_burst))") {
		t.Fatalf("MutantsArgv = %q, want the exclusion after the `--` cargo-mutants forwards to nextest", got)
	}
}

// A repo that declares no exclusion must see no passthrough at all: the
// feature is opt-in, and a stray `--` changes what cargo-mutants forwards.
func TestMutantsArgv_OmitsThePassthroughWhenNoExclusionIsDeclared(t *testing.T) {
	t.Parallel()
	got := MutantsArgv("lane.diff", 120, nil, "")

	for _, arg := range got {
		if arg == "--" || arg == "-E" {
			t.Fatalf("MutantsArgv = %v, want no nextest passthrough with nothing declared", got)
		}
	}
}

// mutation-accept's own shape: "<value> # reason". An entry with no `#`
// reason is refused, named verbatim in bad, never silently dropped -- a typo
// here must never look like it is still excluding.
func TestMutationBaselineExcludeParse_RefusesAnEntryWithNoReasonNamingIt(t *testing.T) {
	expr, count, bad := mutationBaselineExcludeParse([]string{"test(conditioner_burst)"})
	if expr != "" || count != 0 {
		t.Fatalf("expr=%q count=%d, want nothing excluded from a reason-less entry", expr, count)
	}
	if len(bad) != 1 || bad[0] != "test(conditioner_burst)" {
		t.Fatalf("bad = %q, want the entry named verbatim", bad)
	}
}

// An entry whose reason is present but blank (just a trailing `#`) is the
// same refusal: a reason of nothing but whitespace is not an argument.
func TestMutationBaselineExcludeParse_RefusesAnEntryWithABlankReason(t *testing.T) {
	_, count, bad := mutationBaselineExcludeParse([]string{"test(conditioner_burst) #   "})
	if count != 0 {
		t.Fatalf("count = %d, want 0 for a blank reason", count)
	}
	if len(bad) != 1 {
		t.Fatalf("bad = %q, want the blank-reason entry refused", bad)
	}
}

// Two reasoned entries combine into one nextest filterset that excludes the
// union of both from measurement.
func TestMutationBaselineExcludeParse_CombinesReasonedEntriesIntoOneNotExpression(t *testing.T) {
	entries := []string{
		"test(conditioner_burst) # box-contended wall-clock test, not tree-caused",
		"test(moving_bandwidth) # ditto",
	}
	expr, count, bad := mutationBaselineExcludeParse(entries)
	if len(bad) != 0 {
		t.Fatalf("bad = %q, want none", bad)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	want := "not(test(conditioner_burst) + test(moving_bandwidth))"
	if expr != want {
		t.Fatalf("expr = %q, want %q", expr, want)
	}
}

// A mix: the reasoned entry still counts and excludes; the reason-less one is
// refused on its own and named, rather than poisoning the whole declaration.
func TestMutationBaselineExcludeParse_KeepsTheReasonedEntryWhenAnotherIsRefused(t *testing.T) {
	entries := []string{
		"test(conditioner_burst) # box-contended wall-clock test",
		"test(moving_bandwidth)",
	}
	expr, count, bad := mutationBaselineExcludeParse(entries)
	if count != 1 || expr != "not(test(conditioner_burst))" {
		t.Fatalf("expr=%q count=%d, want the reasoned entry alone excluded", expr, count)
	}
	if len(bad) != 1 || bad[0] != "test(moving_bandwidth)" {
		t.Fatalf("bad = %q, want the reason-less entry named", bad)
	}
}

// No declared entries at all is not the same shape as an empty array with bad
// entries: it is today's behaviour, unchanged.
func TestMutationBaselineExcludeParse_NoEntriesProducesNoFilter(t *testing.T) {
	expr, count, bad := mutationBaselineExcludeParse(nil)
	if expr != "" || count != 0 || bad != nil {
		t.Fatalf("got expr=%q count=%d bad=%q, want all empty for no declared entries", expr, count, bad)
	}
}

// Two spellings: a Cargo workspace's own [workspace.metadata.aphrollo] wins
// over a root aphrollo.toml [aphrollo] declaring something different --
// mirroring IssueLabels' own precedence.
func TestMutationBaselineExcludeEntries_PrefersCargoTomlOverAphrolloToml(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = []\n\n[workspace.metadata.aphrollo]\nmutation-baseline-exclude = [\n  \"test(cargo_side) # from Cargo.toml\",\n]\n")
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutation-baseline-exclude = [\n  \"test(aphrollo_side) # from aphrollo.toml\",\n]\n")

	entries := mutationBaselineExcludeEntries(root)
	joined := strings.Join(entries, " ")
	if !strings.Contains(joined, "cargo_side") {
		t.Fatalf("entries = %q, want the Cargo.toml declaration to win", entries)
	}
	if strings.Contains(joined, "aphrollo_side") {
		t.Fatalf("entries = %q, want the aphrollo.toml declaration NOT read once Cargo.toml declared any", entries)
	}
}

// A repo with no Cargo.toml at all (or none of this key in it) still gets the
// declaration from its root aphrollo.toml -- the fallback that makes the key
// usable outside a Cargo workspace.
func TestMutationBaselineExcludeEntries_FallsBackToAphrolloTomlWithNoCargoToml(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutation-baseline-exclude = [\n  \"test(only_here) # go repo, no Cargo.toml\",\n]\n")

	entries := mutationBaselineExcludeEntries(root)
	if !strings.Contains(strings.Join(entries, " "), "only_here") {
		t.Fatalf("entries = %q, want the aphrollo.toml declaration read", entries)
	}
}

// ratchet: test_removed TestMutationBaselineExcludeForRun_LogsTheRefusedEntryByName: mutationBaselineExcludeForRun is deleted with the producer's env; MeasureLane parses the list off MutantsConfig and logs each refused entry itself
// ratchet: test_removed TestMutantsChildEnv_CarriesTheDeclaredExclusionAndItsCount: mutantsChildEnv is deleted with the detached producer; the exclusion now reaches the tool through MutantsArgv, proved above
// ratchet: test_removed TestMutantsChildEnv_CarriesNoExclusionWhenNoneIsDeclared: same deletion, same replacement
// ratchet: test_removed TestMutationReceipt_RoundTripsTheExcludedCount: there is no receipt to carry a count into
// ratchet: test_removed TestMutationReceipt_OmitsExcludedWhenZero: there is no receipt to carry a count into
