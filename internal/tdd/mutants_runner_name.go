package tdd

import "path/filepath"

// Naming the thing that will actually measure a lane, for a message somebody
// reads at the moment they are deciding what to type.

// drivesClause names the producer the run will invoke, and says nothing at
// all when the repo has none of its own. "it drives aphrollo gate mutants for
// you" is not a fact about anything.
func drivesClause(root string) string {
	producer := mutantsRunnerCommand(root)
	if producer == mutantsRunnerFallback {
		return ""
	}
	return " — it drives " + producer + " for you, under the box-wide lock"
}

// mutantsRunnerFallback is the answer for a repo that declares no producer:
// this binary's own runner, which every repo has.
const mutantsRunnerFallback = "aphrollo gate mutants"

func mutantsRunnerCommand(root string) string {
	const fallback = mutantsRunnerFallback
	if root == "" {
		return fallback
	}
	// cargoWorkspaceRoot answers `root` when it finds no workspace table, so
	// a single-crate repo's own Cargo.toml is what gets read here.
	ws := cargoWorkspaceRoot(root)
	if name, ok := cargoAphrolloString(ws, "mutation-runner"); ok && name != "" {
		return name
	}
	if name, ok := aphrolloTomlString(root, "mutation-runner"); ok && name != "" {
		return name
	}
	if fileExists(filepath.Join(root, "tools", "mutation_gate.sh")) {
		return "tools/mutation_gate.sh"
	}
	return fallback
}
