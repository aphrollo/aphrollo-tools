package tdd

import (
	"path/filepath"
	"strings"
)

// A wall-clock test in the mutation baseline measures the BOX, not the tree:
// under load it fails whether or not a mutation is applied, so it both vetoes
// a merge for a reason the tree is not responsible for and — worse — reads
// a mutant as CAUGHT when the load, not the mutation, is what failed the
// test. Nothing ever re-examines a false CAUGHT (issue #265).
//
// mutation-baseline-exclude lets a repo declare which tests are excluded from
// mutation measurement — from the unmutated BASELINE and from mutant testing
// alike, since the same reasoning holds for both. Opt-in: a repo that
// declares none gets today's behaviour, unchanged.

// mutationBaselineExcludeKey is the key, read under `[aphrollo]` in
// aphrollo.toml and under `[workspace.metadata.aphrollo]` in Cargo.toml —
// both spellings, the same fallback IssueLabels already uses for a repo that
// may or may not be a Cargo workspace.
const mutationBaselineExcludeKey = "mutation-baseline-exclude"

// mutationBaselineExcludeEntries reads the repo's declared exclusion list,
// preferring a non-empty Cargo.toml declaration and falling back to
// aphrollo.toml — the same two-spelling precedence IssueLabels uses.
func mutationBaselineExcludeEntries(root string) []string {
	ws := cargoWorkspaceRoot(root)
	if ws == "" {
		ws = root
	}
	if entries := cargoAphrolloPackages(ws, mutationBaselineExcludeKey); len(entries) > 0 {
		return entries
	}
	return tomlStringsIn(filepath.Join(root, "aphrollo.toml"), "[aphrollo]", mutationBaselineExcludeKey)
}

// mutationBaselineExcludeParse turns declared entries into one combined
// nextest filterset expression and how many entries make it up. It follows
// mutation-accept's own shape (acceptedMutants): "<filter> # why", and a
// reason is not decoration — an entry missing its `#` reason, or carrying no
// filter before it, is refused, named verbatim in bad, rather than dropped
// silently: a typo here must never look like it is still excluding.
//
// ("", 0, nil) for no declared entries at all: opt-in, so a repo that
// declares nothing behaves exactly as before.
func mutationBaselineExcludeParse(entries []string) (expr string, count int, bad []string) {
	if len(entries) == 0 {
		return "", 0, nil
	}
	var filters []string
	for _, entry := range entries {
		filter, reason, ok := strings.Cut(entry, "#")
		filter = strings.TrimSpace(filter)
		if !ok || strings.TrimSpace(reason) == "" || filter == "" {
			bad = append(bad, entry)
			continue
		}
		filters = append(filters, filter)
	}
	if len(filters) == 0 {
		return "", 0, bad
	}
	// A single `not(...)` over the union of every declared filter excludes
	// each named test from BOTH the baseline and every mutant's own test run
	// — MutantsArgv passes this to cargo-mutants as one trailing passthrough
	// flag, and cargo-mutants runs the SAME test command for both phases.
	return "not(" + strings.Join(filters, " + ") + ")", len(filters), bad
}
