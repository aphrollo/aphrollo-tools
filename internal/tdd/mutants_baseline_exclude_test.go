package tdd

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// A declared exclusion reaches the SAME nextest passthrough flag whether the
// call is building a baseline run's own argv or a mutant run's — cargo-mutants
// invokes the same test command for both, so one flag placed after mutantsProducerFlags's
// own `--` is what makes one declared exclusion cover both phases (issue #265).
// ratchet: test_removed TestMutantsArgv_CarriesTheDeclaredExclusionForBothBaselineAndMutantTesting: renamed with the function it tests, MutantsArgv -> mutantsProducerFlags; the assertions are unchanged
func TestMutantsProducerFlags_CarriesTheDeclaredExclusionForBothBaselineAndMutantTesting(t *testing.T) {
	want := "-- -E not(test(conditioner_burst))"
	for _, baselineSkip := range []bool{false, true} {
		got := strings.Join(mutantsProducerFlags("lane.diff", baselineSkip, nil, nil, "not(test(conditioner_burst))"), " ")
		if !strings.Contains(got, want) {
			t.Fatalf("baselineSkip=%v: mutantsProducerFlags = %q, want it to contain %q", baselineSkip, got, want)
		}
	}
}

// A repo that declares no exclusion must see today's argv, byte for byte:
// this feature is opt-in.
// ratchet: test_removed TestMutantsArgv_OmitsTheNextestPassthroughWhenNoExclusionIsDeclared: renamed with the function it tests, MutantsArgv -> mutantsProducerFlags; the assertions are unchanged
func TestMutantsProducerFlags_OmitsTheNextestPassthroughWhenNoExclusionIsDeclared(t *testing.T) {
	got := strings.Join(mutantsProducerFlags("lane.diff", false, nil, nil, ""), " ")
	want := "--in-place --in-diff lane.diff --test-tool=nextest"
	if got != want {
		t.Fatalf("mutantsProducerFlags = %q, want %q unchanged", got, want)
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

// The point of the whole feature: a refused entry is LOGGED, naming itself,
// never silently dropped -- a typo must not look like it is still excluding.
func TestMutationBaselineExcludeForRun_LogsTheRefusedEntryByName(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutation-baseline-exclude = [\n  \"test(conditioner_burst)\",\n]\n")

	var buf bytes.Buffer
	expr, count := mutationBaselineExcludeForRun(root, &buf)
	if expr != "" || count != 0 {
		t.Fatalf("expr=%q count=%d, want nothing excluded from a reason-less entry", expr, count)
	}
	if !strings.Contains(buf.String(), "test(conditioner_burst)") {
		t.Fatalf("log = %q, want the refused entry named", buf.String())
	}
}

// End to end: a declared exclusion reaches the child environment the
// producer actually runs with, both as the nextest passthrough inside
// APHROLLO_MUTANTS_ARGS and as the count in APHROLLO_MUTANTS_BASELINE_EXCLUDED
// -- so the runner can carry it into the receipt without re-parsing config.
func TestMutantsChildEnv_CarriesTheDeclaredExclusionAndItsCount(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	worktree := t.TempDir()
	write(t, worktree, "aphrollo.toml", "[aphrollo]\nmutation-baseline-exclude = [\n"+
		"  \"test(conditioner_burst) # box-contended wall-clock test, not tree-caused\",\n"+
		"]\n")
	target := worktree + "/target"
	j := MutantsJob{RepoRoot: t.TempDir(), Worktree: worktree, TargetDir: target, TipTree: laneTip}

	env := mutantsChildEnv(j, nil)
	args := mustEnvValue(t, env, MutantsArgsEnv)
	if !strings.Contains(args, "-- -E not(test(conditioner_burst))") {
		t.Fatalf("args = %q, want the declared exclusion forwarded to nextest", args)
	}
	if got := mustEnvValue(t, env, MutantsBaselineExcludedEnv); got != strconv.Itoa(1) {
		t.Fatalf("%s = %q, want \"1\"", MutantsBaselineExcludedEnv, got)
	}
}

// A worktree that declares no exclusion must see NEITHER the passthrough flag
// nor a nonzero count: opt-in, so a repo that says nothing behaves exactly as
// today.
func TestMutantsChildEnv_CarriesNoExclusionWhenNoneIsDeclared(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	worktree := t.TempDir()
	target := worktree + "/target"
	j := MutantsJob{RepoRoot: t.TempDir(), Worktree: worktree, TargetDir: target, TipTree: laneTip}

	env := mutantsChildEnv(j, nil)
	if args := mustEnvValue(t, env, MutantsArgsEnv); strings.Contains(args, "-E") {
		t.Fatalf("args = %q, want no nextest passthrough with nothing declared", args)
	}
	if got := mustEnvValue(t, env, MutantsBaselineExcludedEnv); got != "0" {
		t.Fatalf("%s = %q, want \"0\"", MutantsBaselineExcludedEnv, got)
	}
}

// The receipt the merge gate consumes must carry the excluded count in its
// own JSON body, not only in the repo's config -- the whole mitigation for a
// repo quietly excluding its way to a green receipt.
func TestMutationReceipt_RoundTripsTheExcludedCount(t *testing.T) {
	r := MutationReceipt{Verdict: receiptVerdictPass, Excluded: 2}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"excluded":2`) {
		t.Fatalf("receipt JSON = %s, want an excluded field carrying the count", data)
	}
	var got MutationReceipt
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Excluded != 2 {
		t.Fatalf("Excluded = %d after round-trip, want 2", got.Excluded)
	}
}

// A receipt that excludes nothing must not carry the field at all --
// `omitempty` is what keeps every receipt written before this feature
// existed byte-identical in shape.
func TestMutationReceipt_OmitsExcludedWhenZero(t *testing.T) {
	data, err := json.Marshal(MutationReceipt{Verdict: receiptVerdictPass})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "excluded") {
		t.Fatalf("receipt JSON = %s, want no excluded field for a zero count", data)
	}
}
