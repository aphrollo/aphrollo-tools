package tdd

import (
	"fmt"
	"os"
	"strings"
)

// gateRoot runs ONE project root's gate stages in COST order, stopping at
// the first rejection:
//
//  1. cargo fmt --check   (milliseconds)
//  2. the always-run guard packages, as their OWN invocation (a pure crate;
//     bundling it into `-p ratchet -p client` made it wait for client to link)
//  3. cargo clippy on the crates declared clippy-clean
//  4. fail-first RED proof (precommit only, and only when staged tests add a
//     declaration)
//  5. the touched crates' suites — full test build, link and run, the
//     heaviest thing the gate does
//
// Before this the heaviest stage ran first, so a commit with a formatting
// slip paid the whole test build to be told about a space. gateName
// ("precommit"/"premergecommit") names the calling gate in every stderr line
// and gate.log entry; failFirst is false for the merge gate, whose commits
// were each already judged when authored.
func gateRoot(gateName, repoRoot string, g rootGroup, run SuiteRunner, failFirst bool) GateResult {
	// Changes-gate: a docs/yaml-only commit (no staged source AND no staged
	// test) under this root has nothing to check.
	if len(g.tests) == 0 && len(g.srcs) == 0 {
		return GateResult{}
	}
	runner, ok := DetectRunner(g.root)
	if !ok {
		fmt.Fprintf(os.Stderr, "gate %s: %s → skipped (no detected runner)\n", gateName, g.root)
		appendGateLog(gateName, g.root, "", "no-runner-skipped", 0)
		return GateResult{}
	}
	rootFiles := append(append([]string{}, g.tests...), g.srcs...)

	if runner.Cmd == "cargo" {
		// The cargo branch moved to precommit_wsmanifest.go, alongside the
		// workspace-manifest plumbing it now also drives (issue #365).
		return gateRootCargo(gateName, repoRoot, g, rootFiles, run, failFirst)
	}

	// Scope the mechanical run to the related tests of the staged
	// source+test files: commit-time is a fast scoped check; CI runs the
	// full suite at submit as the authoritative gate. A runner with no
	// related mode (or an unknown command) falls back to the full suite
	// unchanged.
	rootRelFiles := toRootRelative(repoRoot, g.root, rootFiles)
	if scoped, narrowed := narrowToStaged(runner, g.root, rootRelFiles); narrowed {
		runner = scoped
	}
	if runner.Cmd == "go" {
		runner = withGoCIParity(runner, gateName == premergeDisplayName)
	}
	// CI parity for a Go root: the same vet and lint the branch is judged by,
	// both cheaper than the suite and therefore ahead of it.
	if runner.Cmd == "go" {
		if res := goQualityStage(gateName, repoRoot, g.root, rootRelFiles, run); res.Blocked {
			return res
		}
	}
	if failFirst {
		// Only pair the two stages when fail-first will actually launch a
		// worktree run: a commit with no staged test, or one whose test adds
		// no new declaration (failFirstWouldRun == false), makes
		// failFirstStage a same-line no-op, so pairing it here would cap the
		// mechanical suite's own parallelism (capGoTestParallelism) for a
		// concurrency that never happens. Fail-first and the mechanical
		// suite read different trees and neither writes state the other
		// reads — see runFailFirstAndMechanicalConcurrently's own doc
		// comment for why that is NOT true of gateRootCargo's cargo branch
		// below, which keeps its two stages sequential.
		if failFirstWouldRun(repoRoot, g.tests, g.srcs) {
			return runFailFirstAndMechanicalConcurrently(gateName, repoRoot, g.root, g.tests, g.srcs, runner, run)
		}
		if res := failFirstStage(repoRoot, g.root, g.tests, g.srcs, run); res.Blocked {
			return res
		}
	}
	return suiteStage(gateName, repoRoot, g.root, runner, run)
}

// cargoStagePlan is what the cargo stages of one root need: the workspace
// commands run from, the packages this commit TOUCHED (never the always-run
// additions — no staged file belongs to those), the guard packages the
// workspace declares, and any staged files that ARE the workspace's own
// manifest/lockfile/build config rather than a member's.
type cargoStagePlan struct {
	ws            string
	touched       []string
	alwaysRun     []string
	wsManifestHit []string
}

// suiteRunner is the touched crates' own scoped test command — the guard
// packages deliberately absent, since they run as their own cheap stage.
func (p cargoStagePlan) suiteRunner() Runner {
	args := cargoVerbArgs(p.ws)
	for _, pkg := range p.touched {
		args = append(args, "-p", pkg)
	}
	return withGateProfile(Runner{Cmd: "cargo", Args: args, Dir: p.ws}, p.ws)
}

// guardRunner is the always-run packages' own invocation.
func (p cargoStagePlan) guardRunner() Runner {
	args := cargoVerbArgs(p.ws)
	for _, pkg := range p.alwaysRun {
		args = append(args, "-p", pkg)
	}
	return withGateProfile(Runner{Cmd: "cargo", Args: args, Dir: p.ws}, p.ws)
}

// planCargoStages resolves package ownership for a cargo root. Ownership is
// judged PER FILE against the [package] Cargo.toml that covers it: a file no
// package owns is SKIPPED, never a trigger to widen the run — except the
// workspace's OWN manifest/lockfile/config, pulled out as wsManifestHit
// rather than skipped (classifyUnownedCargoFiles, issue #365). ok=false means
// nothing here is owned AND no workspace manifest file was staged either.
func planCargoStages(gateName, repoRoot, root string, rootFiles []string) (cargoStagePlan, bool) {
	owned, unowned := cargoOwnedFiles(repoRoot, root, rootFiles)
	// The actual WORKSPACE root: a checked-in .config/nextest.toml and the
	// workspace's Cargo.lock live there, not in a member crate's own
	// directory. State/mech-cache keys still use the crate root.
	ws := cargoWorkspaceRoot(root)
	wsManifestHit := classifyUnownedCargoFiles(gateName, repoRoot, ws, unowned)
	if len(owned) == 0 && len(wsManifestHit) == 0 {
		return cargoStagePlan{}, false
	}
	return cargoStagePlan{
		ws:            ws,
		touched:       cargoPackagesOwning(root, toRootRelative(repoRoot, root, owned)),
		alwaysRun:     cargoAlwaysRunPackages(ws),
		wsManifestHit: wsManifestHit,
	}, true
}

// alwaysRunStage runs the workspace's declared guard packages as their own
// invocation, before the touched crates' heavier one. A guard package owns
// no staged file, so ownership scoping alone would run it only when someone
// edits the guard itself — precisely when its invariant is not at risk.
func alwaysRunStage(gateName, repoRoot, root string, plan cargoStagePlan, run SuiteRunner) GateResult {
	if len(plan.alwaysRun) == 0 {
		return GateResult{}
	}
	return runSuiteStage(gateName, "always-run", repoRoot, root, plan.guardRunner(), run)
}

// workspaceCheckStage compiles the WHOLE workspace's code and tests, without
// codegen. The suites only cover the crates a commit touched, so a change
// that breaks a crate nobody staged lands green: borld's
// forge_jbeam/tests/conformance.rs reached main not compiling because a lane
// added a struct field and the gate ran only that lane's crates. A check is
// the cheapest stage that can see the whole graph — tens of seconds warm,
// against minutes for the suites — so it sits between clippy and fail-first.
func workspaceCheckStage(gateName, repoRoot, root string, plan cargoStagePlan, run SuiteRunner) GateResult {
	ws := plan.ws
	if ws == "" {
		ws = root
	}
	// clippy SUBSUMES check — a compile error fails it just the same — and it
	// is the only way to enforce the two lints that carry project laws
	// (clippy.toml's disallowed methods and types) across crates that are not
	// on the clippy-clean list. Nothing else is denied here: -D warnings
	// belongs to the per-crate stage, where a crate has actually reached zero.
	//
	// Scoped, never --workspace: see clippyscope.go. The crates are named on
	// stderr because a scoped stage that does not say what it covered cannot
	// be told from one that silently stopped covering something.
	scope := clippyScope(gateName, repoRoot, ws, plan.touched)
	if len(scope) == 0 {
		fmt.Fprintf(os.Stderr, "gate %s: check → skipped (no cargo package owns anything staged)\n", gateName)
		appendGateLog(gateName, ws, "", "clippy-scope-empty-skipped", 0)
		return GateResult{}
	}
	fmt.Fprintf(os.Stderr, "gate %s: check scope → %s (touched crates + everything downstream of them)\n",
		gateName, strings.Join(scope, " "))
	args := []string{"clippy"}
	for _, pkg := range scope {
		args = append(args, "-p", pkg)
	}
	args = append(args, "--tests", "--",
		"-D", "clippy::disallowed_methods", "-D", "clippy::disallowed_types")
	runner := Runner{Cmd: "cargo", Args: args, Dir: ws}
	return runSuiteStage(gateName, "check", repoRoot, root, runner, run)
}

// doctestStage runs the touched crates' doctests, which the nextest-based
// suite stage above cannot: nextest does not run them at all, so a
// `compile_fail` proof would otherwise never execute.
func doctestStage(gateName, repoRoot, root string, plan cargoStagePlan, run SuiteRunner) GateResult {
	ws := plan.ws
	if ws == "" {
		ws = root
	}
	for _, runner := range doctestRunners(ws, plan.touched) {
		if res := runSuiteStage(gateName, "doctest", repoRoot, root, runner, run); res.Blocked {
			return res
		}
	}
	return GateResult{}
}

// suiteStage runs the touched crates' own suites — the heaviest stage, and
// therefore the last.
func suiteStage(gateName, repoRoot, root string, runner Runner, run SuiteRunner) GateResult {
	return runSuiteStage(gateName, "mechanical", repoRoot, root, runner, run)
}
