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
	if failFirst {
		if res := failFirstStageWithRustNotice(repoRoot, g.root, g.tests, g.srcs, run); res.Blocked {
			return res
		}
	}
	if len(plan.touched) > 0 {
		if res := suiteStage(gateName, repoRoot, g.root, plan.suiteRunner(), run); res.Blocked {
			return res
		}
	}
	return doctestStage(gateName, repoRoot, g.root, plan, run)
}

// workspaceManifestCheckStage answers issue #365's harder half: a workspace-
// root manifest, lockfile or cargo config change can reshape or break every
// crate at once and owns no [package] to scope a suite to, so a cheap
// `cargo check --workspace --tests` stands in for the per-crate suites here —
// a compile-coverage proof over the whole tree, not a test RUN of it, which
// keeps the cost in the same tier as workspaceCheckStage rather than the
// heavy nextest suites.
func workspaceManifestCheckStage(gateName, repoRoot string, plan cargoStagePlan, run SuiteRunner) GateResult {
	fmt.Fprintf(os.Stderr, "gate %s: workspace manifest changed (%v) → cargo check --workspace --tests\n",
		gateName, plan.wsManifestHit)
	runner := withGateProfile(Runner{Cmd: "cargo", Args: []string{"check", "--workspace", "--tests"}, Dir: plan.ws}, plan.ws)
	return runSuiteStage(gateName, "workspace-manifest-check", repoRoot, plan.ws, runner, run)
}
