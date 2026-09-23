package tdd

import (
	"fmt"
	"os"
	"path/filepath"
)

// classifyUnownedCargoFiles splits a root's unowned cargo files: those that
// ARE the workspace's own Cargo.toml/Cargo.lock/.cargo/config.toml (which own
// no [package] by construction, yet change what every member builds) are
// returned; every other unowned file is reported on stderr and dropped, per
// planCargoStages' existing rule that an unowned file never widens a run.
func classifyUnownedCargoFiles(gateName, repoRoot, ws string, unowned []string) []string {
	var hit []string
	for _, f := range unowned {
		if isWorkspaceOwnManifestFile(repoRoot, ws, f) {
			hit = append(hit, f)
			continue
		}
		fmt.Fprintf(os.Stderr, "gate %s: %s has no owning cargo package — not tested\n", gateName, f)
		suiteProof.OweUnowned()
	}
	return hit
}

// isWorkspaceOwnManifestFile reports whether fRepoRel (repo-root-relative) IS
// ws's own Cargo.toml, Cargo.lock, or .cargo/config.toml — the only files a
// workspace-wide change lands in that own no single [package]. A member
// crate's own Cargo.toml is scoped normally by cargoPackageFor and never
// reaches this check as "unowned".
func isWorkspaceOwnManifestFile(repoRoot, ws, fRepoRel string) bool {
	abs := filepath.Clean(filepath.Join(repoRoot, fRepoRel))
	for _, name := range []string{"Cargo.toml", "Cargo.lock", filepath.Join(".cargo", "config.toml")} {
		if abs == filepath.Clean(filepath.Join(ws, name)) {
			return true
		}
	}
	return false
}

// gateRootCargo is gateRoot's cargo branch (moved here so the new
// workspace-manifest plumbing has room without pushing precommit.go's line
// count past its ratchet ceiling): the same cost-ordered stage sequence
// gateRoot's doc comment describes, with one addition — a staged workspace
// manifest/lockfile/config runs workspaceManifestCheckStage right after the
// always-run guard packages, and when NOTHING owned was also touched (a
// manifest-only commit), the ownership-scoped suite is skipped rather than
// falling back to an unscoped, full-workspace `cargo test`.
func gateRootCargo(gateName, repoRoot string, g rootGroup, rootFiles []string, run SuiteRunner, failFirst bool) GateResult {
	plan, ok := planCargoStages(gateName, repoRoot, g.root, rootFiles)
	if !ok {
		return GateResult{}
	}
	if res := cargoQualityStage(gateName, plan.ws, g.root, plan.touched, run, repoRoot, qualityFmt); res.Blocked {
		return res
	}
	if res := alwaysRunStage(gateName, repoRoot, g.root, plan, run); res.Blocked {
		return res
	}
	if len(plan.wsManifestHit) > 0 {
		if res := workspaceManifestCheckStage(gateName, repoRoot, plan, run); res.Blocked {
			return res
		}
	}
	if res := cargoQualityStage(gateName, plan.ws, g.root, plan.touched, run, repoRoot, qualityClippy); res.Blocked {
		return res
	}
	if res := workspaceCheckStage(gateName, repoRoot, g.root, plan, run); res.Blocked {
		return res
	}
	// Deliberately sequential, unlike gateRoot's non-cargo branch (issue
	// #535): fail-first shares this workspace's own CARGO_TARGET_DIR with
	// the mechanical stage below it and undoes the contamination with a
	// `cargo clean` that runs AFTER the build's target-lock hold has already
	// been released (invalidateFailFirstArtifacts, precommit_failfirst.go) —
	// a window a concurrently-started mechanical run could slip into and
	// read artifacts fail-first built from HEAD before they are removed.
	// That is real shared state, not incidental sequencing, so cargo stays
	// out of scope for the concurrency change — see
	// the commit gate no longer runs a suite here at all.
	if failFirst {
		if res := failFirstStageWithRustNotice(repoRoot, g.root, g.tests, g.srcs, run); res.Blocked {
			return res
		}
	}
	// The touched crates' suite runs at the merge, not at every commit — see
	// gateRoot for the measurement behind that. failFirst is true only for the
	// commit gate, so that is the path which owes the scope without running
	// it, and must both say so and refrain from vouching for the tree
	// afterwards (suiteproof.go).
	if len(plan.touched) > 0 {
		suiteProof.Owe(plan.suiteRunner())
		if failFirst {
			reportSuitesNotRun(gateName, g.root, "crate", plan.suiteRunner(), plan.downstream)
		} else if res := suiteStage(gateName, repoRoot, g.root, plan.suiteRunner(), run); res.Blocked {
			return res
		}
	}
	return doctestStage(gateName, repoRoot, g.root, plan, run)
}

// workspaceManifestCheckStage answers issue #365's harder half: a workspace-
// root manifest, lockfile or cargo config change can reshape or break every
// crate at once and owns no [package] to scope a suite to, so a cheap
// `cargo check --tests` stands in for the per-crate suites here — a
// compile-coverage proof over the affected tree, not a test RUN of it, which
// keeps the cost in the same tier as workspaceCheckStage rather than the
// heavy nextest suites. A Cargo.toml or .cargo/config.toml change keeps
// `--workspace`: either can reshape how everything builds. A Cargo.lock-only
// change (issue #423) narrows to lockfileScope's answer — the packages whose
// locked version moved plus their workspace dependents — since a version
// bump can only affect what depends on it, directly or transitively; an
// unreadable or unparsable diff falls back to `--workspace` rather than
// guessing narrower.
func workspaceManifestCheckStage(gateName, repoRoot string, plan cargoStagePlan, run SuiteRunner) GateResult {
	scope := lockfileScope(gateName, repoRoot, plan.ws, plan.wsManifestHit)
	if scope != nil && len(scope) == 0 {
		// verdictFor (verdict.go) is the one place a stage outcome becomes a
		// GateResult -- outcomeSkipped is the deliberate, logged stand-down
		// that still lets the commit through.
		return verdictFor(gateName, "workspace-manifest-check", plan.ws, "cargo check", stageOutcome{
			kind:   outcomeSkipped,
			reason: fmt.Sprintf("%v moved no package a workspace crate depends on", plan.wsManifestHit),
		})
	}
	var args []string
	if scope == nil {
		fmt.Fprintf(os.Stderr, "gate %s: workspace manifest changed (%v) → cargo check --workspace --tests\n",
			gateName, plan.wsManifestHit)
		args = []string{"check", "--workspace", "--tests"}
	} else {
		fmt.Fprintf(os.Stderr, "gate %s: %v moved → cargo check --tests scoped to %v\n",
			gateName, plan.wsManifestHit, scope)
		args = []string{"check", "--tests"}
		for _, p := range scope {
			args = append(args, "-p", p)
		}
	}
	runner := withGateProfile(Runner{Cmd: "cargo", Args: args, Dir: plan.ws}, plan.ws)
	return runSuiteStage(gateName, "workspace-manifest-check", repoRoot, plan.ws, runner, run)
}
