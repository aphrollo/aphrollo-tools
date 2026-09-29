package merge

import (
	"path/filepath"
	"strings"
	"testing"
)

// Issue #948 part 3: no npm commit was ever noted green. The merge gate ran
// the owed vitest selection green, but the ledger could not read a vitest
// command, so the scope it owed was unreadable and nothing could vouch for
// the tree. arrow carried 0 notes over 255 commits.
func TestMechanical_ClaimsTheGreenWhenTheOwedVitestSelectionRan(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(SetLookNodeForTest(func() (string, error) { return "/opt/node/bin/node", nil }))
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, ".gitignore", "node_modules/\n")
	write(t, root, "package.json", `{"name": "app", "devDependencies": {"vitest": "3.2.7"}}`)
	write(t, root, "src/lib/caps.ts", "export const caps = () => 2\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "node_modules/vitest/package.json", `{"name": "vitest", "bin": {"vitest": "./vitest.mjs"}}`)
	write(t, root, "node_modules/vitest/vitest.mjs", "")
	write(t, root, "src/lib/caps.ts", "export const caps = () => 3\n")
	gitDo(t, root, "add", ".")

	var ran []string
	green := func(r Runner, _ string) SuiteResult {
		ran = append(ran, r.Cmd+" "+strings.Join(r.Args, " "))
		return SuiteResult{Passed: true, Output: " ✓ src/lib/caps.test.ts (1 test) 1ms\n Test Files  1 passed (1)\n      Tests  1 passed (1)\n"}
	}
	note := noteAfterGate(t, root, func() GateResult { return Mechanical(root, green) })

	want := "/opt/node/bin/node " + filepath.Join(root, "node_modules", "vitest", "vitest.mjs") + " related src/lib/caps.ts --run"
	if len(ran) != 1 || ran[0] != want {
		t.Fatalf("premise broken — the merge gate must run the owed vitest selection alone, ran %q, want [%s]", ran, want)
	}
	tree := gitOutT(t, root, "rev-parse", "HEAD:")
	if note != gateGreenNote(tree) {
		t.Fatalf("a green run of the owed vitest selection must vouch for the tree: note = %q, want %q", note, gateGreenNote(tree))
	}
}
