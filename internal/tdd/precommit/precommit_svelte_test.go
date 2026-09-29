package precommit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #948 part 1: a .svelte component classified as nothing, so a commit
// or merge that touched only components ran no vitest at all, and fail-first
// never withheld one. A component is Source of the npm root whose
// package.json is nearest, scoped like a .ts file.

// fakeSvelteVitestScript is a vitest that passes when the Counter component
// doubles, as its test demands, and fails otherwise. It appends its argv,
// one run per line, to $FAKE_VITEST_LOG.
const fakeSvelteVitestScript = `import { appendFileSync, readFileSync } from 'node:fs'
appendFileSync(process.env.FAKE_VITEST_LOG, process.argv.slice(2).join(' ') + '\n')
if (readFileSync('src/lib/Counter.svelte', 'utf8').includes('count * 2')) {
  console.log(' ✓ src/lib/Counter.test.ts (1 test) 1ms')
  console.log(' Test Files  1 passed (1)')
  console.log('      Tests  1 passed (1)')
  process.exit(0)
}
console.log(' FAIL  src/lib/Counter.test.ts > doubles')
console.log(' Test Files  1 failed (1)')
console.log('      Tests  1 failed (1)')
process.exit(1)
`

// makeSvelteKitRepo is a committed repo holding a SvelteKit app under web/,
// with the fake vitest installed in the app's gitignored node_modules.
func makeSvelteKitRepo(t *testing.T) (repo, app, runLog string) {
	t.Helper()
	repo = t.TempDir()
	gitInit(t, repo)
	write(t, repo, ".gitignore", "node_modules/\n")
	write(t, repo, "web/package.json", `{"name": "web", "type": "module", "devDependencies": {"@sveltejs/kit": "2.20.0", "svelte": "5.25.0", "vitest": "3.2.7"}}`)
	write(t, repo, "web/src/lib/Counter.svelte", "<script>let { count } = $props()</script>\n<p>{count}</p>\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "base")
	app = filepath.Join(repo, "web")
	write(t, app, "node_modules/vitest/package.json", `{"name": "vitest", "type": "module", "bin": {"vitest": "./vitest.mjs"}}`)
	write(t, app, "node_modules/vitest/vitest.mjs", fakeSvelteVitestScript)
	runLog = filepath.Join(t.TempDir(), "runs.log")
	t.Setenv("FAKE_VITEST_LOG", runLog)
	return repo, app, runLog
}

// The merge gate owes the component's related tests: a merge that staged
// only a .svelte file runs vitest related over it, in the app, under node.
func TestMechanical_AStagedSvelteComponentRunsItsRelatedVitestTests(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Cleanup(SetLookNodeForTest(func() (string, error) { return "/opt/node/bin/node", nil }))
	repo, app, _ := makeSvelteKitRepo(t)
	write(t, app, "src/lib/Counter.svelte", "<script>let { count } = $props()</script>\n<p>{count * 2}</p>\n")
	gitDo(t, repo, "add", ".")

	var seen []Runner
	if res := Mechanical(repo, runsAt(&seen, app)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := "/opt/node/bin/node " + filepath.Join(app, "node_modules", "vitest", "vitest.mjs") + " related src/lib/Counter.svelte --run"
	if got := runLines(seen); len(got) != 1 || got[0] != want {
		t.Fatalf("the merge gate ran %q, want exactly [%s]", got, want)
	}
}

// Fail-first withholds a staged component like any other source: a new
// test of it is red at HEAD, where the component did not double, and green
// with the staged one.
func TestFailFirst_AStagedSvelteComponentIsWithheldAtHead(t *testing.T) {
	requireNode(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo, app, runLog := makeSvelteKitRepo(t)
	write(t, app, "src/lib/Counter.svelte", "<script>let { count } = $props()</script>\n<p>{count * 2}</p>\n")
	write(t, app, "src/lib/Counter.test.ts", "import Counter from './Counter.svelte'\nit('doubles', () => { expect(Counter).toBeDefined() })\n")
	gitDo(t, repo, "add", ".")

	var res GateResult
	stderr := captureStderr(t, func() { res = Precommit(repo, RunSuite(precommitTestTimeout)) })
	if res.Blocked {
		t.Fatalf("red at HEAD and green with the component was refused:\n%s\n%s", res.Message, stderr)
	}
	if !strings.Contains(stderr, "→ red-proven") || !strings.Contains(stderr, "→ green-proven") {
		t.Fatalf("want red-proven then green-proven, got:\n%s", stderr)
	}
	data, err := os.ReadFile(runLog)
	if err != nil {
		t.Fatalf("the installed vitest never ran: %v", err)
	}
	if runs := strings.Count(string(data), "\n"); runs != 2 {
		t.Fatalf("want one run at HEAD and one with the change, got %d:\n%s", runs, data)
	}
}
