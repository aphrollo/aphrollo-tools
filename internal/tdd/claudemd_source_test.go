package tdd

import (
	"os"
	"strings"
	"testing"
)

// TestClaudeMDBlock_VetLintClaimMatchesGoOnlyGuard is a cheap trip-wire for
// #548's cold-review yellow: the block claims a Go root runs vet/lint,
// which is true only because precommit_gateroot.go's goQualityStage is
// guarded by `runner.Cmd == "go"` — pytest/vitest/zig roots get neither.
// This does not verify the prose is accurate in general (no general
// truth-checker over English is being attempted here) — only that the ONE
// condition this specific claim depends on has not silently moved
// underneath it, which is the same block-versus-binary drift #518 exists to
// close, caught here instead of by a human re-reading both by hand.
func TestClaudeMDBlock_VetLintClaimMatchesGoOnlyGuard(t *testing.T) {
	src, err := os.ReadFile("precommit_gateroot.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `if runner.Cmd == "go" {`) {
		t.Fatal(`precommit_gateroot.go no longer guards goQualityStage with runner.Cmd == "go" — update the "a Go root also runs vet/lint" claim in ClaudeMDBlock's stage-list bullet to match`)
	}
	block := ClaudeMDBlock(shimDir, false, false)
	if !strings.Contains(block, "a Go root also runs vet/lint") {
		t.Fatal(`ClaudeMDBlock no longer scopes its vet/lint claim to "a Go root" — check it still matches precommit_gateroot.go's runner.Cmd == "go" guard before broadening it`)
	}
}

// The block described the mutation run only as something post-commit spawns,
// so every repo that gets it learned the gate's mutation half without ever
// learning the command a person types. Four sessions independently reached for
// the repo's own producer script instead — which runs outside the box-wide
// lock and in the wrong tree — and one traced the habit to a skill that
// spelled the script out as a numbered step. There is no spawned form left,
// so the block names the typed one and nothing else: a session told the
// commit starts a run waits for one that never starts.
// The block listed the commit gate as ending in "suites", which the gate
// stopped doing when the mechanical suite moved to the merge. A session then
// read a correct precommit — fail-first and no suites line — as a suite that
// had failed to run, applied the rule that a missing verdict means untested,
// and spent a full package run proving what the gate had already decided not
// to ask for. The block is the instruction sessions act on, so a claim it
// makes about a stage that no longer exists costs a suite every commit.
func TestClaudeMDBlock_DoesNotPromiseASuiteTheCommitGateNoLongerRuns(t *testing.T) {
	src, err := os.ReadFile("precommit_gateroot.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "return failFirstStage(repoRoot, g.Root, g.tests, g.srcs, run)") {
		t.Fatal("precommit_gateroot.go no longer ends its fail-first branch at failFirstStage — if the " +
			"commit gate runs a suite again, restore the claim in ClaudeMDBlock's stage-list bullet")
	}
	block := ClaudeMDBlock(shimDir, false, false)
	if strings.Contains(block, "fail-first→\n  suites") || strings.Contains(block, "fail-first→suites") {
		t.Error("the block still says the commit gate ends in suites, but the fail-first branch returns " +
			"at failFirstStage — a session that believes it re-runs the package the gate deliberately skipped")
	}
	if !strings.Contains(block, "merge") {
		t.Error("the block does not say where the mechanical suite DID go — a session told only that the " +
			"commit gate skips it cannot tell a skipped suite from a missing one")
	}
}
