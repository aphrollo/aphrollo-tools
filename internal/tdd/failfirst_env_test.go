package tdd

import (
	"os"
	"testing"
)

// TestReadFailFirstEnv_ReadsTheSwitchesTheRepoDeclares pins the key itself:
// same two tables, same Cargo-wins precedence, same "NAME=VALUE" shape as
// mutants-env, so a repo whose suites are gated declares them side by side
// instead of learning a second convention (#656).
func TestReadFailFirstEnv_ReadsTheSwitchesTheRepoDeclares(t *testing.T) {
	root := t.TempDir()
	if got := readFailFirstEnv(root); len(got) != 0 {
		t.Fatalf("%s = %v, want none for a repo that declares none", failFirstEnvKey, got)
	}

	write(t, root, "aphrollo.toml", "[aphrollo]\nfail-first-env = [\"FORGE_GPU_TESTS=1\"]\n")
	got := readFailFirstEnv(root)
	if len(got) != 1 || got[0] != "FORGE_GPU_TESTS=1" {
		t.Fatalf("%s = %v, want the switch aphrollo.toml declares", failFirstEnvKey, got)
	}

	// The Cargo spelling wins, exactly as it does for every other key read
	// through mutantsConfigTables.
	write(t, root, "Cargo.toml", "[workspace]\n[workspace.metadata.aphrollo]\nfail-first-env = [\"BORLD_GPU=1\"]\n")
	if got := readFailFirstEnv(root); len(got) != 1 || got[0] != "BORLD_GPU=1" {
		t.Fatalf("%s = %v, want the Cargo workspace's declaration to win", failFirstEnvKey, got)
	}
}

// TestFailFirstProof_RunsWithTheDeclaredSwitchExported is the plumbing half
// of #656: the field case was a GPU suite that self-skipped in the proof
// worktree because the switch lived only in the author's shell. A repo
// declares it ONCE and the proof run inherits it — and the export is undone
// afterwards, since the gate process goes on to run other stages.
func TestFailFirstProof_RunsWithTheDeclaredSwitchExported(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// Pinned empty (and restored by t.Setenv), so the assertion below reads
	// the gate's own export and not whatever the box happens to carry.
	t.Setenv("FORGE_GPU_TESTS", "")
	root := makeGoRepo(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\nfail-first-env = [\"FORGE_GPU_TESTS=1\"]\n")
	write(t, root, "gpu.go", "package m\n\nfunc Parity() int { return 1 }\n")
	write(t, root, "gpu_test.go", "package m\n\nimport \"testing\"\n\nfunc TestGPUParity(t *testing.T) {\n\tif Parity() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	gitDo(t, root, "add", ".")

	seen := "<never ran>"
	run := func(Runner, string) SuiteResult {
		seen = os.Getenv("FORGE_GPU_TESTS")
		return SuiteResult{Passed: false, Output: "FAIL\n"}
	}
	captureStderr(t, func() {
		failFirstStage(root, root, []string{"gpu_test.go"}, []string{"gpu.go"}, run)
	})
	if seen != "1" {
		t.Fatalf("the proof ran with FORGE_GPU_TESTS=%q, want the switch the repo declares", seen)
	}
	if got := os.Getenv("FORGE_GPU_TESTS"); got != "" {
		t.Fatalf("the export must be undone once the proof is over (got %q) — the gate has other stages to run", got)
	}
}
