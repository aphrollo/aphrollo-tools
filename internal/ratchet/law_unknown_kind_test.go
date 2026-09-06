package ratchet

import "testing"

// TestLoadLaws_UnknownMatcherKindIsSkippedNotRejected is the #440
// bootstrap-hazard fix: a law naming a matcher kind this binary predates
// must not fail LoadLaws for the WHOLE tree — every other law in the repo
// has to stay judged. LoadLaws itself only marks the one law (UnknownKind);
// Check/RunFixtures are what decide to skip and report it.
func TestLoadLaws_UnknownMatcherKindIsSkippedNotRejected(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "known", `
name = "known"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "regex-absent"
pattern = "x"
`)
	writeLaw(t, dir, "future", `
name = "future"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "vibes"
`)
	laws, err := LoadLaws(dir)
	if err != nil {
		t.Fatalf("LoadLaws: %v", err)
	}
	if len(laws) != 2 {
		t.Fatalf("loaded %d laws, want 2 (the unknown kind must not vanish OR fail the load)", len(laws))
	}
	var future *Law
	for i := range laws {
		if laws[i].Name == "future" {
			future = &laws[i]
		}
	}
	if future == nil {
		t.Fatalf("law %q missing from LoadLaws result", "future")
	}
	if future.UnknownKind != "vibes" {
		t.Errorf("UnknownKind = %q, want %q", future.UnknownKind, "vibes")
	}
}

// TestCheck_UnknownMatcherKindIsSkippedNotJudged is Check()'s half of #440:
// LoadLaws alone hands back the raw law untouched, but Check must not fold
// it into the set it actually SCANS and baselines — res.Laws counts only
// the laws judged, and the unknown one is named in SkippedLaws instead.
func TestCheck_UnknownMatcherKindIsSkippedNotJudged(t *testing.T) {
	dir := t.TempDir()
	writeLaw(t, dir, "known", `
name = "known"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	writeLaw(t, dir, "future", `
name = "future"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "vibes"
`)
	res, err := Check(Options{Root: dir})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Laws != 1 {
		t.Fatalf("Laws = %d, want 1 — the unknown-kind law must be SKIPPED, never counted among the ones judged", res.Laws)
	}
	if len(res.SkippedLaws) != 1 || res.SkippedLaws[0].Name != "future" || res.SkippedLaws[0].Kind != "vibes" {
		t.Fatalf("SkippedLaws = %+v, want one entry naming future/vibes", res.SkippedLaws)
	}
}
