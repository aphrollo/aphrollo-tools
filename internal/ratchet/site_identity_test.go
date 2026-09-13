package ratchet

import (
	"path/filepath"
	"testing"
)

// The measured defect of #675: a text multiset's ceiling is an AGGREGATE, so
// a hit at a path carrying no baseline row at all is invisible for as long as
// the total for that text stays under the number. Here three recorded sites
// were paid down and one brand-new site appeared somewhere else entirely —
// measured 1, ceiling 3 — which reported clean while the law's own guarantee
// ("excuse the known offenders, block everything new") was not being kept.
func TestCheck_ReportsANewSiteWhileTheTextsAggregateIsUnderItsCeiling(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "nan-guard", nanGuardLaw)
	write(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a/src/lib.rs | let a = x.clamp(0.0, 1.0);\n"+
			"crates/b/src/lib.rs | let a = x.clamp(0.0, 1.0);\n"+
			"crates/c/src/lib.rs | let a = x.clamp(0.0, 1.0);\n")
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = 1;\n")
	write(t, filepath.Join(root, "crates", "b", "src", "lib.rs"), "let a = 1;\n")
	write(t, filepath.Join(root, "crates", "c", "src", "lib.rs"), "let a = 1;\n")
	write(t, filepath.Join(root, "crates", "d", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")

	res, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one — the site no row names", res.Findings)
	}
	if f := res.Findings[0]; f.File != "crates/d/src/lib.rs" {
		t.Errorf("the finding must name the new site, got %+v", f)
	}
}

// What e69a017 bought and this must not spend: a line's debt belongs to the
// workspace, not to the file that happens to hold it. A site that relocates
// leaves exactly as many rows behind as it takes up, so the text's total is
// unchanged and no row of it is a new offence.
func TestCheck_TreatsARelocatedSiteAsNoRegressionWhenTheTotalIsUnchanged(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "nan-guard", nanGuardLaw)
	write(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a/src/lib.rs | let a = x.clamp(0.0, 1.0);\n"+
			"crates/b/src/lib.rs | let a = x.clamp(0.0, 1.0);\n"+
			"crates/c/src/lib.rs | let a = x.clamp(0.0, 1.0);\n")
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")
	write(t, filepath.Join(root, "crates", "b", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")
	write(t, filepath.Join(root, "crates", "d", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")

	res, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a move is not a regression, findings = %+v", res.Findings)
	}
}

// Deciding that a site is NEW rather than relocated needs every other site of
// that text. The pre-edit hook reads one file, so it has not looked at them:
// there the aggregate ceiling remains the whole guard, exactly as before, and
// the new site is reported by the whole-tree run at commit.
func TestCheck_LeavesANarrowedScanToTheAggregateCeiling(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "nan-guard", nanGuardLaw)
	write(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a/src/lib.rs | let a = x.clamp(0.0, 1.0);\n"+
			"crates/b/src/lib.rs | let a = x.clamp(0.0, 1.0);\n")
	write(t, filepath.Join(root, "crates", "d", "src", "lib.rs"), "let a = x.clamp(0.0, 1.0);\n")

	res, err := Check(Options{Root: root, Files: []string{"crates/d/src/lib.rs"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a one-file scan cannot tell a new site from a moved one, findings = %+v", res.Findings)
	}
}

// An identity with exactly ONE recorded row needs no set difference: its
// aggregate ceiling is already per-site, because a site at any other path
// makes the total 2 against 1. Proved by enumeration over every measurement
// of up to three sites across three paths — the per-site report never says
// anything the aggregate did not already say, so an "only above one row"
// fast path would be an optimisation with an identical guarantee, not a
// weaker variant. The second half is the contrast that keeps the first from
// being vacuous: at TWO rows the per-site report catches what the aggregate
// misses.
func TestNewSiteRegressions_AddNothingToTheAggregateForASingleRowIdentity(t *testing.T) {
	const text = "let a = x.clamp(0.0, 1.0);"
	paths := []string{"crates/a/src/lib.rs", "crates/b/src/lib.rs", "crates/c/src/lib.rs"}
	one, err := ParseBaseline(paths[0]+" | "+text+"\n", MultisetByText)
	if err != nil {
		t.Fatal(err)
	}

	aggregateSaw := false
	for _, measuredPaths := range measurementsUpTo(paths, 3) {
		sites := map[string][]string{}
		for _, p := range measuredPaths {
			sites[text] = append(sites[text], p+" | "+text)
		}
		measured := map[string]int{text: len(measuredPaths)}
		agg := one.Regressions(measured)
		perSite := one.NewSiteRegressions(sites)
		aggregateSaw = aggregateSaw || len(agg) > 0
		if len(perSite) > 0 && len(agg) == 0 {
			t.Fatalf("measured %v: per-site reported %+v where the aggregate reported nothing", measuredPaths, perSite)
		}
	}
	if !aggregateSaw {
		t.Fatal("the enumeration never produced a regression at all — it proves nothing")
	}

	two, err := ParseBaseline(paths[0]+" | "+text+"\n"+paths[1]+" | "+text+"\n", MultisetByText)
	if err != nil {
		t.Fatal(err)
	}
	sites := map[string][]string{text: {paths[2] + " | " + text}}
	if got := two.Regressions(map[string]int{text: 1}); len(got) != 0 {
		t.Fatalf("the aggregate is supposed to miss this one: %+v", got)
	}
	if got := two.NewSiteRegressions(sites); len(got) != 1 || got[0].Key != paths[2]+" | "+text {
		t.Fatalf("two rows, one unrecorded site, total down: want that site reported, got %+v", got)
	}
}

// The commit gate scans the whole tree with the STAGED content overlaid on
// the files this commit touches, so a hypothetical overlay is not a narrowed
// scan: every other site of the text was still looked at. The gate that
// refuses the commit is exactly the one that has to see a new site.
func TestCheck_ReportsANewSiteUnderAStagedOverlayOfTheWholeTree(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "nan-guard", nanGuardLaw)
	write(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a/src/lib.rs | let a = x.clamp(0.0, 1.0);\n"+
			"crates/b/src/lib.rs | let a = x.clamp(0.0, 1.0);\n"+
			"crates/c/src/lib.rs | let a = x.clamp(0.0, 1.0);\n")
	write(t, filepath.Join(root, "crates", "a", "src", "lib.rs"), "let a = 1;\n")
	write(t, filepath.Join(root, "crates", "b", "src", "lib.rs"), "let a = 1;\n")
	write(t, filepath.Join(root, "crates", "c", "src", "lib.rs"), "let a = 1;\n")
	write(t, filepath.Join(root, "crates", "d", "src", "lib.rs"), "let a = 1;\n")

	res, err := Check(Options{
		Root:     root,
		Proposed: map[string]string{"crates/d/src/lib.rs": "let a = x.clamp(0.0, 1.0);\n"},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want the staged site no row names", res.Findings)
	}
	if f := res.Findings[0]; f.File != "crates/d/src/lib.rs" {
		t.Errorf("the finding must name the staged site, got %+v", f)
	}
}

// measurementsUpTo enumerates every measurement of 0..n sites drawn
// from paths, repeats included: two hits of the same text in one file is a
// measurement the comparison has to survive.
func measurementsUpTo(paths []string, n int) [][]string {
	out := [][]string{{}}
	level := [][]string{{}}
	for i := 0; i < n; i++ {
		var next [][]string
		for _, prefix := range level {
			for _, p := range paths {
				combo := append(append([]string{}, prefix...), p)
				next = append(next, combo)
			}
		}
		out = append(out, next...)
		level = next
	}
	return out
}
