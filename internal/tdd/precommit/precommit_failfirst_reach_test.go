package precommit

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeNpx stands in for npm's npx the way it behaves in a hook: it runs the
// tool from ./node_modules/.bin when the directory it runs in has one, and
// otherwise refuses to fetch it (CI=1, no --yes) and exits 1 without ever
// starting the tool.
const fakeNpx = `#!/bin/sh
tool=$1; shift
if [ -x "node_modules/.bin/$tool" ]; then exec "node_modules/.bin/$tool" "$@"; fi
echo "npm error npx canceled due to missing packages and no YES option: [\"$tool@3.2.7\"]" >&2
exit 1
`

// fakeVitestGreen is the root's installed vitest, which passes the test.
const fakeVitestGreen = `#!/bin/sh
echo " ✓ src/lib/caps.test.ts (3 tests) 4ms"
echo " Test Files  1 passed (1)"
echo "      Tests  3 passed (3)"
exit 0
`

// makeVitestRepoWithFakeNpx is an npm/vitest root whose own node_modules
// holds a working vitest, with the fake npx first on PATH. Its commit adds a
// characterization test for code HEAD already has, beside a config edit.
func makeVitestRepoWithFakeNpx(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake npx and vitest are shell scripts") // skip-ok: the same proof on Windows needs a native fake binary; the verdict logic under test is host-independent
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "npx"), []byte(fakeNpx), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, ".gitignore", "node_modules/\n")
	write(t, root, "package.json", `{"name": "app", "devDependencies": {"vitest": "3.2.7"}}`)
	write(t, root, "vitest.config.ts", "export default {}\n")
	write(t, root, "src/lib/caps.ts", "export const caps = () => 3\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	vitest := filepath.Join(root, "node_modules", ".bin", "vitest")
	if err := os.MkdirAll(filepath.Dir(vitest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vitest, []byte(fakeVitestGreen), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, "vitest.config.ts", "export default { test: { globals: true } }\n")
	write(t, root, "src/lib/caps.test.ts", "import { caps } from './caps'\nit('has three', () => { expect(caps()).toBe(3) })\n")
	gitDo(t, root, "add", "vitest.config.ts", "src/lib/caps.test.ts")
	return root
}

// Issue #898. The proof worktree at HEAD carries no node_modules (they are
// gitignored), so npx there never starts vitest and exits 1 — and a failed
// run was read as the new test going RED. A test that never ran proved
// nothing: the run must not be certified red-proven.
func TestFailFirst_AParentRunThatNeverStartedTheToolIsNotRedProven(t *testing.T) {
	root := makeVitestRepoWithFakeNpx(t)

	var res GateResult
	stderr := captureStderr(t, func() { res = Precommit(root, RunSuite(precommitTestTimeout)) })
	if res.Blocked {
		t.Fatalf("an unrunnable proof refused the commit:\n%s", res.Message)
	}
	var line string
	for l := range strings.Lines(stderr) {
		if strings.HasPrefix(l, "[fail-first]") {
			line = l
		}
	}
	if line == "" || strings.Contains(line, "red-proven") || !strings.Contains(line, "test-not-reached") {
		t.Fatalf("want the fail-first line to say the test was never reached, got %q in:\n%s", line, stderr)
	}
	if !strings.Contains(stderr, "failed before it reached the staged tests") || !strings.Contains(stderr, "npx canceled") {
		t.Fatalf("the verdict does not say why, with the run's own first line:\n%s", stderr)
	}
}

// makeCharacterizedVitestRepo is the #898 commit on a vitest root, for the
// tests that answer the proof run through a fake runner instead of a real
// process: a new test for code HEAD already has, beside a config edit.
func makeCharacterizedVitestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "package.json", `{"name": "app", "devDependencies": {"vitest": "3.2.7"}}`)
	write(t, root, "vitest.config.ts", "export default {}\n")
	write(t, root, "src/lib/caps.ts", "export const caps = () => 3\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "vitest.config.ts", "export default { test: { globals: true } }\n")
	write(t, root, "src/lib/caps.test.ts", "import { caps } from './caps'\nit('has three', () => { expect(caps()).toBe(3) })\n")
	gitDo(t, root, "add", "vitest.config.ts", "src/lib/caps.test.ts")
	return root
}

func failFirstLine(stderr string) string {
	for l := range strings.Lines(stderr) {
		if strings.HasPrefix(l, "[fail-first]") {
			return l
		}
	}
	return ""
}

// A characterization test passes at the parent: it pins nothing the commit
// changed, and the gate says the test passed there rather than certifying
// it as a red.
func TestFailFirst_ATestGreenAtTheParentIsNotRedProven(t *testing.T) {
	root := makeCharacterizedVitestRepo(t)
	green := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: true, Output: " ✓ src/lib/caps.test.ts (3 tests) 4ms\n Test Files  1 passed (1)\n      Tests  3 passed (3)\n"}
	}
	var res GateResult
	stderr := captureStderr(t, func() { res = Precommit(root, green) })
	line := failFirstLine(stderr)
	if strings.Contains(line, "red-proven") || !strings.Contains(line, "violated") {
		t.Fatalf("want a test green at the parent reported as passing there, got %q", line)
	}
	if !res.Blocked || !strings.Contains(res.Message, "PASS against the pre-edit code") {
		t.Fatalf("want the refusal to say the test passes at HEAD, got %+v", res)
	}
}

// A test that ran at the parent and failed there is the red the proof
// exists to find, and is certified as one.
func TestFailFirst_ATestThatRanAndFailedAtTheParentIsRedProven(t *testing.T) {
	root := makeCharacterizedVitestRepo(t)
	red := func(Runner, string) SuiteResult {
		return SuiteResult{Passed: false, Output: " ❯ src/lib/caps.test.ts (1 test | 1 failed) 5ms\n   × has three 3ms\n AssertionError: expected 2 to be 3\n"}
	}
	var res GateResult
	stderr := captureStderr(t, func() { res = Precommit(root, red) })
	if res.Blocked {
		t.Fatalf("a proven red refused the commit:\n%s", res.Message)
	}
	if line := failFirstLine(stderr); !strings.Contains(line, "red-proven") {
		t.Fatalf("want red-proven, got %q", line)
	}
}

// What the proof run printed is kept for `aphrollo gate output`: without
// it, a session told the test was never reached cannot see why.
func TestFailFirst_TheProofRunsOutputIsKeptForGateOutput(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCharacterizedVitestRepo(t)
	const printed = "npm error npx canceled due to missing packages and no YES option: [\"vitest@3.2.7\"]\n"
	unstarted := func(_ Runner, dir string) SuiteResult {
		return SuiteResult{Passed: false, Output: printed, Dir: dir}
	}
	captureStderr(t, func() { Precommit(root, unstarted) })
	got, err := RetainedSuiteOutput(root)
	if err != nil {
		t.Fatalf("no output kept for the fail-first run: %v", err)
	}
	if !strings.Contains(got, printed) || !strings.Contains(got, "verdict: "+"inconclusive (test-not-reached)") {
		t.Fatalf("the kept record does not carry the run and its verdict:\n%s", got)
	}
}
