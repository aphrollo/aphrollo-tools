package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// depGraphCeilingLawText is a standalone dep-graph-ceiling law, unrelated to
// which crates actually exist: baselineStage never runs the scanner, so no
// real Cargo.toml graph is needed to prove the guard's own decision.
const depGraphCeilingLawText = `name = "crate-fanout"
description = "a root may not reach more of the workspace than its baseline"
severity = "deny"
baseline = ".ratchet/baselines/crate-fanout.txt"

[scope]
include = ["**/Cargo.toml"]

[matcher]
kind = "dep-graph-ceiling"
roots = ["server", "client"]
`

// crateFanoutRepo commits crate-fanout.toml (unchanged across seed and
// staged, since #480 is about the WORKSPACE growing, not the law) with its
// baseline at seedBaseline, then stages stagedBaseline on top.
func crateFanoutRepo(t *testing.T, seedBaseline, stagedBaseline string) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "crate-fanout.toml"), depGraphCeilingLawText)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "crate-fanout.txt"), seedBaseline)
	gitAddAll(t, root)
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "crate-fanout.txt"), stagedBaseline)
	gitAddAll(t, root)
	return root
}

// TestBaselineGuard_AdmitsAFirstEverRowForANewWorkspaceRoot reproduces #480:
// a dep-graph-ceiling law is keyed on the workspace ROOT, so adding a crate
// produces a first-ever `<root> | <count>` row under a law nobody touched.
// That must be adopted, not read as a hand-raised ceiling.
func TestBaselineGuard_AdmitsAFirstEverRowForANewWorkspaceRoot(t *testing.T) {
	root := crateFanoutRepo(t, "server | 2\n", "server | 2\nclient | 1\n")

	res := baselineStage("precommit", root)
	if res.Blocked {
		t.Fatalf("a new workspace root's first row must not be refused as a raise: %s", res.Message)
	}
}

// TestBaselineGuard_StillRefusesARaisedExistingRootUnderDepGraphCeiling is
// the other direction the same fix must prove: admitting a brand-new root
// key must not widen into ignoring an EXISTING root's count going up.
func TestBaselineGuard_StillRefusesARaisedExistingRootUnderDepGraphCeiling(t *testing.T) {
	root := crateFanoutRepo(t, "server | 2\nclient | 1\n", "server | 3\nclient | 1\n")

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("an existing root's count going up is still a hand-raise and must be refused")
	}
	if !strings.Contains(res.Message, "server") || !strings.Contains(res.Message, "2 -> 3") {
		t.Errorf("message must name the raised row: %s", res.Message)
	}
}

// TestBaselineGuard_StillRefusesANewKeyUnderAPerFileLaw proves the fix is
// scoped to the matcher kind, not to "any baseline gaining a key": a law
// keyed on files (nan-guard, module_size, ...) gaining a new key is exactly
// the case the guard exists to catch, and must stay refused.
func TestBaselineGuard_StillRefusesANewKeyUnderAPerFileLaw(t *testing.T) {
	root := lawAndBaselineRepo(t, nanGuardLawText, "crates/a.rs | let a = x.clamp(0.0, 1.0);\n",
		nanGuardLawText, "crates/a.rs | let a = x.clamp(0.0, 1.0);\ncrates/b.rs | let b = y.clamp(0.0, 1.0);\n")

	res := baselineStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "crates/b.rs") {
		t.Fatalf("a new key under a per-file law must still be refused: %+v", res)
	}
}

// benchCeilingLawText is a go-bench-ceiling law, keyed on the BENCH CASE
// name. That key space grows as a normal consequence of work — adding a bench
// adds a key — exactly like the workspace-root key space #480 covered.
const benchCeilingLawText = `name = "bench-ceiling"
description = "a benchmark's allocations may only fall"
severity = "deny"
baseline = ".ratchet/baselines/bench-ceiling.txt"

[scope]
include = ["testdata/bench/baseline.txt"]

[matcher]
kind  = "go-bench-ceiling"
files = "testdata/bench/baseline.txt"
`

// jsonCeilingLawText is the same shape read out of generated JSON — the kind
// borld #227's live case uses, whose `sample_ground/inside_tile` and
// `sample_ground/on_seam` figures could not be committed at all.
const jsonCeilingLawText = `name = "perf"
description = "a tier-1 kernel bench may not regress"
severity = "deny"
baseline = ".ratchet/baselines/perf.txt"

[scope]
include = ["**/*.json"]

[matcher]
kind = "json-number-ceiling"
files = "criterion/**/new/estimates.json"
path = "mean.point_estimate"
`

// benchCeilingRepo commits bench-ceiling.toml unchanged across seed and
// staged — #577 is about the BENCH SET growing, not the law — with its
// baseline at seedBaseline, then stages stagedBaseline on top.
func benchCeilingRepo(t *testing.T, seedBaseline, stagedBaseline string) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "bench-ceiling.toml"), benchCeilingLawText)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "bench-ceiling.txt"), seedBaseline)
	gitAddAll(t, root)
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "bench-ceiling.txt"), stagedBaseline)
	gitAddAll(t, root)
	return root
}

// TestBaselineGuard_NewBenchKeyUnderUnchangedCeilingLawIsAdmitted is #577:
// adding a bench case adds a baseline key, the guard reads the missing HEAD
// row as `0 -> N` and refuses the commit as a raise, so a new bench can never
// receive its first ceiling under a law nobody touched — the same gap #480
// closed for dep-graph-ceiling's workspace roots.
func TestBaselineGuard_NewBenchKeyUnderUnchangedCeilingLawIsAdmitted(t *testing.T) {
	root := benchCeilingRepo(t, "sample_ground/inside_tile | 120\n",
		"sample_ground/inside_tile | 120\nsample_ground/on_seam | 340\n")

	res := baselineStage("precommit", root)

	if res.Blocked {
		t.Fatalf("a new bench case's first-ever ceiling must not be refused as a hand-raise: %s", res.Message)
	}
}

// TestBaselineGuard_RaisedBenchKeyUnderUnchangedLawIsStillRefused is the other
// direction the same fix must prove: admitting a brand-new bench key must not
// widen into ignoring an EXISTING case's figure going up.
func TestBaselineGuard_RaisedBenchKeyUnderUnchangedLawIsStillRefused(t *testing.T) {
	root := benchCeilingRepo(t, "sample_ground/inside_tile | 120\nsample_ground/on_seam | 340\n",
		"sample_ground/inside_tile | 130\nsample_ground/on_seam | 340\n")

	res := baselineStage("precommit", root)

	if !res.Blocked {
		t.Fatal("an existing bench case's figure going up is still a hand-raise and must be refused")
	}
	if !strings.Contains(res.Message, "sample_ground/inside_tile") || !strings.Contains(res.Message, "120 -> 130") {
		t.Errorf("message must name the raised row: %s", res.Message)
	}
}

// ratchet: test_removed TestWholeTreeCeilingBaseline_TrueOnlyForDepGraphCeiling: the
// discriminator covers three ceiling kinds since #577, not one; renamed to
// TestWholeTreeCeilingBaseline_TrueForACountedCeilingLawFalseForAPerFileLaw below.
func TestWholeTreeCeilingBaseline_TrueForACountedCeilingLawFalseForAPerFileLaw(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "crate-fanout.toml"), depGraphCeilingLawText)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "bench-ceiling.toml"), benchCeilingLawText)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "perf.toml"), jsonCeilingLawText)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), nanGuardLawText)
	gitAddAll(t, root)
	commitAll(t, root)

	if !wholeTreeCeilingBaseline(root, ".ratchet/baselines/crate-fanout.txt") {
		t.Error("a dep-graph-ceiling law's own baseline must report true")
	}
	if !wholeTreeCeilingBaseline(root, ".ratchet/baselines/bench-ceiling.txt") {
		t.Error("a go-bench-ceiling law's own baseline must report true")
	}
	if !wholeTreeCeilingBaseline(root, ".ratchet/baselines/perf.txt") {
		t.Error("a json-number-ceiling law's own baseline must report true")
	}
	if wholeTreeCeilingBaseline(root, ".ratchet/baselines/nan-guard.txt") {
		t.Error("a per-file law's baseline must report false")
	}
	if wholeTreeCeilingBaseline(root, ".ratchet/baselines/no-such-law.txt") {
		t.Error("a baseline no law declares must report false")
	}
}
