package failfirst

import (
	"os"
	"strings"
)

// A suite gated behind an environment switch is invisible to a gate that does
// not carry the switch. The mutation run learned this first: 101 of 167
// mutants on one lane lived in render-world code only a GPU parity suite
// reaches, so `mutants-env` exports the repo's switches for the measurement.
//
// The fail-first proof has exactly the same problem and used to have no
// answer for it (issue #656). It builds HEAD in a throwaway worktree and runs
// the staged tests there; with the switch unset those tests self-skip, and
// the stage read the skip as "your tests pass at HEAD" and refused a correct
// commit. Exporting the switch by hand for one commit proved the test really
// did go red — which is the whole point: the switch is a property of the
// REPO, declared once, not of the shell the hook happened to inherit.
//
// So: the same key shape, in the same two tables, read the same way.
// `mutants-env` for the measurement, `fail-first-env` for the proof. A repo
// whose suites are gated declares both; they are separate keys because the
// two runs are separate decisions (a measurement may afford a switch that
// makes every commit pay for a GPU).

// failFirstEnvKey is the key a repo declares in
// [workspace.metadata.aphrollo] (Cargo.toml) or [aphrollo] (aphrollo.toml):
// a string array of "NAME=VALUE" switches exported for the fail-first proof
// run, the exact shape and precedence mutantsEnvKey already uses.
const failFirstEnvKey = "fail-first-env"

// readFailFirstEnv is the switches the repo declares for its fail-first
// proof, nil when it declares none. The Cargo spelling wins over
// aphrollo.toml, same as every other key read through mutantsConfigTables.
func readFailFirstEnv(repoRoot string) []string {
	return firstDeclaredList(mutantsConfigTables(repoRoot), failFirstEnvKey)
}

// exportFailFirstEnv puts the repo's declared switches into this process's
// environment for the duration of the proof run and hands back the restore.
// The child inherits them through suiteEnv, which builds on os.Environ().
//
// Process-global, and deliberately the SAME mechanism failFirstViolatedAt
// already uses for CARGO_TARGET_DIR a few lines below its call — one
// convention in one function, rather than a second path through the runner
// struct for the same job. An entry with no "=" in it is not a switch and is
// skipped rather than guessed at.
func exportFailFirstEnv(repoRoot string) func() {
	var restore []func()
	for _, kv := range readFailFirstEnv(repoRoot) {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			continue
		}
		k = strings.TrimSpace(k)
		prev, had := os.LookupEnv(k)
		if err := os.Setenv(k, v); err != nil {
			continue
		}
		restore = append(restore, func() {
			if had {
				os.Setenv(k, prev)
			} else {
				os.Unsetenv(k)
			}
		})
	}
	return func() {
		for _, f := range restore {
			f()
		}
	}
}
