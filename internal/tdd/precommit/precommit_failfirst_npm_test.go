package precommit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Issue #904: on an npm root every fail-first proof ended inconclusive,
// because its worktree at HEAD had no node_modules and it ran the tool
// through npx. The proof worktree links the root's own node_modules and
// runs the installed tool as `node <bin entry>`.

// fakeVitestScript is a vitest that passes when src/lib/caps.ts returns 3
// and fails otherwise, naming the test the way vitest does. It appends the
// directory it ran in to $FAKE_VITEST_LOG.
const fakeVitestScript = `import { appendFileSync, readFileSync } from 'node:fs'
appendFileSync(process.env.FAKE_VITEST_LOG, process.cwd() + '\n')
if (readFileSync('src/lib/caps.ts', 'utf8').includes('=> 3')) {
  console.log(' ✓ src/lib/caps.test.ts (1 test) 1ms')
  console.log(' Test Files  1 passed (1)')
  console.log('      Tests  1 passed (1)')
  process.exit(0)
}
console.log(' FAIL  src/lib/caps.test.ts > has three')
console.log(' Test Files  1 failed (1)')
console.log('      Tests  1 failed (1)')
process.exit(1)
`

// withNodeOnPath makes exec's PATH lookup of node succeed, for the proofs
// whose runs a fake SuiteRunner answers: no node process ever starts.
func withNodeOnPath(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err == nil {
		return
	}
	bin := t.TempDir()
	name := "node"
	if runtime.GOOS == "windows" {
		name = "node.exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// makeFakeVitestRoot is a committed vitest root whose caps() returns
// headCaps, with the fake vitest installed in its gitignored node_modules.
func makeFakeVitestRoot(t *testing.T, headCaps string) (root, runLog string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH; the fake vitest is a node script") // skip-ok: the tool under test runs on node, which this box lacks
	}
	root = t.TempDir()
	gitInit(t, root)
	write(t, root, ".gitignore", "node_modules/\n")
	write(t, root, "package.json", `{"name": "app", "type": "module", "devDependencies": {"vitest": "3.2.7"}}`)
	write(t, root, "src/lib/caps.ts", "export const caps = () => "+headCaps+"\n")
	write(t, root, "src/lib/other.ts", "export const other = 1\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	write(t, root, "node_modules/vitest/package.json", `{"name": "vitest", "type": "module", "bin": {"vitest": "./vitest.mjs"}}`)
	write(t, root, "node_modules/vitest/vitest.mjs", fakeVitestScript)
	runLog = filepath.Join(t.TempDir(), "runs.log")
	t.Setenv("FAKE_VITEST_LOG", runLog)
	return root, runLog
}

// noHeadWorktreeLeft fails when a proof left its worktree under
// node_modules, or registered with git.
func noHeadWorktreeLeft(t *testing.T, root string) {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(root, "node_modules", ".aphrollo-head-*"))
	if len(left) > 0 {
		t.Errorf("the proof left %v behind", left)
	}
	if _, err := os.Stat(filepath.Join(root, "node_modules", "vitest", "package.json")); err != nil {
		t.Error("the cleanup removed the root's installed vitest")
	}
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(out), "worktree "); n != 1 {
		t.Errorf("want only the main worktree registered, got:\n%s", out)
	}
}

// A new test that is red at HEAD is proven red there, by the root's own
// vitest run under node in a worktree outside its node_modules, and then
// green with the change by the same invocation.
func TestFailFirst_NpmRootProvesRedAtHeadWithTheInstalledVitestUnderNode(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, runLog := makeFakeVitestRoot(t, "2")
	write(t, root, "src/lib/caps.ts", "export const caps = () => 3\n")
	write(t, root, "src/lib/caps.test.ts", "import { caps } from './caps'\nit('has three', () => { expect(caps()).toBe(3) })\n")
	gitDo(t, root, "add", ".")

	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"src/lib/caps.test.ts"}, []string{"src/lib/caps.ts"}, RunSuite(precommitTestTimeout))
	})
	if res.Blocked {
		t.Fatalf("red at HEAD and green with the change was refused:\n%s\n%s", res.Message, stderr)
	}
	if !strings.Contains(stderr, "→ red-proven") || !strings.Contains(stderr, "→ green-proven") {
		t.Fatalf("want red-proven then green-proven, got:\n%s", stderr)
	}
	for l := range strings.Lines(stderr) {
		if strings.HasPrefix(l, "[fail-first]") && (strings.Contains(l, "npx") || !strings.Contains(l, filepath.Join("node_modules", "vitest", "vitest.mjs"))) {
			t.Errorf("the proof did not run the installed vitest under node: %s", l)
		}
	}
	data, err := os.ReadFile(runLog)
	if err != nil {
		t.Fatalf("the installed vitest never ran: %v", err)
	}
	dirs := strings.Fields(string(data))
	if len(dirs) != 2 {
		t.Fatalf("want one run at HEAD and one with the change, got %q", dirs)
	}
	nodeModules := filepath.Join(root, "node_modules")
	for _, d := range dirs {
		if strings.HasPrefix(d, nodeModules) || strings.HasPrefix(d, evalSymlinks(t, nodeModules)) {
			t.Errorf("a proof run was placed under the root's node_modules: %s", d)
		}
	}
	noHeadWorktreeLeft(t, root)
}

func evalSymlinks(t *testing.T, p string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// A characterization test is green at the parent, and the gate says so
// rather than certifying it red.
func TestFailFirst_NpmRootReportsATestGreenAtTheParent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ := makeFakeVitestRoot(t, "3")
	write(t, root, "src/lib/other.ts", "export const other = 2\n")
	write(t, root, "src/lib/caps.test.ts", "import { caps } from './caps'\nit('has three', () => { expect(caps()).toBe(3) })\n")
	gitDo(t, root, "add", ".")

	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"src/lib/caps.test.ts"}, []string{"src/lib/other.ts"}, RunSuite(precommitTestTimeout))
	})
	if line := failFirstLine(stderr); !strings.Contains(line, "→ violated") {
		t.Fatalf("want the test reported as passing at the parent, got %q in:\n%s", line, stderr)
	}
	if !res.Blocked || !strings.Contains(res.Message, "PASS against the pre-edit code") {
		t.Fatalf("want the refusal to say the test passes at HEAD, got %+v", res)
	}
	noHeadWorktreeLeft(t, root)
}

// A root whose vitest is not installed is inconclusive and says so; the
// proof runs nothing, npx included.
func TestFailFirst_NpmRootWithoutItsToolInstalledRunsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withNodeOnPath(t)
	root := makeCharacterizedVitestRepo(t)
	if err := os.RemoveAll(filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	var ran []Runner
	record := func(r Runner, _ string) SuiteResult {
		ran = append(ran, r)
		return SuiteResult{Passed: false, Output: " × has three\n"}
	}
	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStage(root, root, []string{"src/lib/caps.test.ts"}, []string{"vitest.config.ts"}, record)
	})
	if res.Blocked {
		t.Fatalf("a proof that could not run refused the commit:\n%s", res.Message)
	}
	if len(ran) > 0 {
		t.Fatalf("want no run without the tool installed, got %v", runLines(ran))
	}
	if line := failFirstLine(stderr); !strings.Contains(line, "inconclusive") || strings.Contains(line, "red-proven") {
		t.Fatalf("want an inconclusive verdict, got %q", line)
	}
	if !strings.Contains(stderr, "vitest is not installed in "+filepath.Join(root, "node_modules")) {
		t.Fatalf("the verdict does not say why:\n%s", stderr)
	}
}

// The real vitest, installed into the fixture from npm's cache without the
// network. A characterization test is reported green at the parent; a new
// test red at HEAD is red-proven and then green with the change.
func TestFailFirstE2E_RealVitestAtHead(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH; skipping e2e") // skip-ok: vitest runs on node, which this box lacks
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Skip("npm not on PATH; skipping e2e") // skip-ok: nothing can install vitest here
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, ".gitignore", "node_modules/\n")
	write(t, root, "package.json", `{"name": "app", "private": true, "type": "module"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	install := exec.CommandContext(ctx, npm, "install", "--offline", "--no-audit", "--no-fund", "--ignore-scripts", "--save-dev", "vitest")
	install.Dir = root
	if out, err := install.CombinedOutput(); err != nil {
		t.Skipf("vitest is not installable from npm's cache without the network; skipping e2e: %v\n%s", err, out) // skip-ok: the box has no cached vitest and the test must not fetch
	}
	write(t, root, "src/lib/caps.ts", "export const caps = (): number => 2\n")
	write(t, root, "src/lib/other.ts", "export const other = (): number => 1\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")

	gate := func(tests, srcs []string) (GateResult, string) {
		t.Helper()
		var res GateResult
		stderr := captureStderr(t, func() { res = failFirstStage(root, root, tests, srcs, RunSuite(precommitTestTimeout)) })
		noHeadWorktreeLeft(t, root)
		return res, stderr
	}

	write(t, root, "src/lib/other.ts", "export const other = (): number => 2\n")
	write(t, root, "src/lib/caps.test.ts", "import { it, expect } from 'vitest'\nimport { caps } from './caps'\nit('has two', () => { expect(caps()).toBe(2) })\n")
	gitDo(t, root, "add", ".")
	res, stderr := gate([]string{"src/lib/caps.test.ts"}, []string{"src/lib/other.ts"})
	if line := failFirstLine(stderr); !strings.Contains(line, "→ violated") || !res.Blocked {
		t.Fatalf("want the characterization test reported green at the parent and refused, got %q, %+v\n%s", line, res, stderr)
	}
	gitDo(t, root, "reset", "-q", "--hard")
	if err := os.Remove(filepath.Join(root, "src", "lib", "caps.test.ts")); err != nil {
		t.Fatal(err)
	}

	write(t, root, "src/lib/caps.ts", "export const caps = (): number => 3\n")
	write(t, root, "src/lib/caps.test.ts", "import { it, expect } from 'vitest'\nimport { caps } from './caps'\nit('has three', () => { expect(caps()).toBe(3) })\n")
	gitDo(t, root, "add", ".")
	res, stderr = gate([]string{"src/lib/caps.test.ts"}, []string{"src/lib/caps.ts"})
	if res.Blocked || !strings.Contains(stderr, "→ red-proven") || !strings.Contains(stderr, "→ green-proven") {
		t.Fatalf("want red-proven then green-proven, got %+v\n%s", res, stderr)
	}
}

// fakeResolvingVitestScript is a vitest that finds its own package the way
// Node does, walking up from the directory it runs in, and passes only when
// the new module src/hooks/fresh.ts exists and returns 3. It appends the
// directory it ran in to $FAKE_VITEST_LOG, and what the root's node_modules
// held while it ran to $FAKE_VITEST_SEEN.
const fakeResolvingVitestScript = `import { appendFileSync, existsSync, readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
appendFileSync(process.env.FAKE_VITEST_LOG, process.cwd() + '\n')
appendFileSync(process.env.FAKE_VITEST_SEEN, readdirSync(process.env.FAKE_VITEST_ROOT_NM).sort().join(',') + '\n')
let dir = process.cwd()
while (!existsSync(join(dir, 'node_modules', 'vitest', 'package.json'))) {
  if (dirname(dir) === dir) {
    console.log('Error: Cannot find package vitest imported from ' + process.cwd())
    process.exit(1)
  }
  dir = dirname(dir)
}
if (existsSync('src/hooks/fresh.ts') && readFileSync('src/hooks/fresh.ts', 'utf8').includes('=> 3')) {
  console.log(' ✓ src/hooks/fresh.test.ts (1 test) 1ms')
  console.log(' Test Files  1 passed (1)')
  console.log('      Tests  1 passed (1)')
  process.exit(0)
}
console.log(' FAIL  src/hooks/fresh.test.ts > has three')
console.log(" Error: Cannot find module './fresh'")
console.log(' Test Files  1 failed (1)')
console.log('      Tests  1 failed (1)')
process.exit(1)
`

// listTree is every path under dir, relative to it, sorted; a link is listed
// and not followed.
func listTree(t *testing.T, dir string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// Issue #932: a commit that adds a new module and its test, in an npm root
// below the repo root, is RED at HEAD and GREEN with the staged change. The
// GREEN run sees the STAGED tree, not the working tree, and neither run
// places anything under the root's node_modules.
func TestFailFirst_NpmRootProvesANewModuleGreenOnTheStagedTreeOutsideNodeModules(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH; the fake vitest is a node script") // skip-ok: the tool under test runs on node, which this box lacks
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := t.TempDir()
	gitInit(t, repo)
	write(t, repo, ".gitignore", "node_modules/\n")
	write(t, repo, "frontend/package.json", `{"name": "app", "type": "module", "devDependencies": {"vitest": "3.2.7"}}`)
	write(t, repo, "frontend/src/lib/other.ts", "export const other = 1\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-qm", "base")
	root := filepath.Join(repo, "frontend")
	nodeModules := filepath.Join(root, "node_modules")
	write(t, root, "node_modules/vitest/package.json", `{"name": "vitest", "type": "module", "bin": {"vitest": "./vitest.mjs"}}`)
	write(t, root, "node_modules/vitest/vitest.mjs", fakeResolvingVitestScript)
	runLog := filepath.Join(t.TempDir(), "runs.log")
	seenLog := filepath.Join(t.TempDir(), "seen.log")
	t.Setenv("FAKE_VITEST_LOG", runLog)
	t.Setenv("FAKE_VITEST_SEEN", seenLog)
	t.Setenv("FAKE_VITEST_ROOT_NM", nodeModules)

	write(t, root, "src/hooks/fresh.ts", "export const fresh = () => 3\n")
	write(t, root, "src/hooks/fresh.test.ts", "import { fresh } from './fresh'\nit('has three', () => { expect(fresh()).toBe(3) })\n")
	gitDo(t, repo, "add", ".")
	// An unstaged edit must not decide the verdict: the staged module
	// returns 3, the working tree's returns 2.
	write(t, root, "src/hooks/fresh.ts", "export const fresh = () => 2\n")
	before := listTree(t, nodeModules)

	var res GateResult
	stderr := captureStderr(t, func() {
		res = failFirstStage(repo, root, []string{"frontend/src/hooks/fresh.test.ts"}, []string{"frontend/src/hooks/fresh.ts"}, RunSuite(precommitTestTimeout))
	})
	if res.Blocked {
		t.Fatalf("red at HEAD and green with the staged change was refused:\n%s\n%s", res.Message, stderr)
	}
	if !strings.Contains(stderr, "→ red-proven") || !strings.Contains(stderr, "→ green-proven") {
		t.Fatalf("want red-proven then green-proven, got:\n%s", stderr)
	}
	data, err := os.ReadFile(runLog)
	if err != nil {
		t.Fatalf("the installed vitest never ran: %v", err)
	}
	dirs := strings.Fields(string(data))
	if len(dirs) != 2 {
		t.Fatalf("want one run at HEAD and one with the change, got %q", dirs)
	}
	for _, d := range dirs {
		if strings.HasPrefix(d, nodeModules) || strings.HasPrefix(d, evalSymlinks(t, nodeModules)) {
			t.Errorf("a proof run was placed under the root's node_modules: %s", d)
		}
	}
	seen, err := os.ReadFile(seenLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(seen)); len(got) != 2 || got[0] != "vitest" || got[1] != "vitest" {
		t.Errorf("the root's node_modules gained entries during the runs: %q", got)
	}
	if after := listTree(t, nodeModules); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("the root's node_modules changed:\nbefore %q\nafter  %q", before, after)
	}
	noHeadWorktreeLeft(t, root)
}
