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

func TestWholeTreeCeilingBaseline_TrueOnlyForDepGraphCeiling(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "crate-fanout.toml"), depGraphCeilingLawText)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), nanGuardLawText)
	gitAddAll(t, root)
	commitAll(t, root)

	if !wholeTreeCeilingBaseline(root, ".ratchet/baselines/crate-fanout.txt") {
		t.Error("a dep-graph-ceiling law's own baseline must report true")
	}
	if wholeTreeCeilingBaseline(root, ".ratchet/baselines/nan-guard.txt") {
		t.Error("a per-file law's baseline must report false")
	}
	if wholeTreeCeilingBaseline(root, ".ratchet/baselines/no-such-law.txt") {
		t.Error("a baseline no law declares must report false")
	}
}
