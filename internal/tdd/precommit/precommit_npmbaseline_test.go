package precommit

import (
	"path/filepath"
	"strings"
	"testing"
)

// fakeTscScript stands in for tsc across the whole project, as tsc runs: it
// reports TS2322 on every line under src/ that declares a probe, wherever it
// sits, and exits 2 when it found any. A CRASH file at the root makes it die
// without a diagnostic, as a broken config does.
const fakeTscScript = `const fs = require("fs"), path = require("path");
if (fs.existsSync("CRASH")) { console.log("boom"); process.exit(1); }
let n = 0;
function walk(d) {
  for (const e of fs.readdirSync(d, { withFileTypes: true }).sort((a, b) => a.name < b.name ? -1 : 1)) {
    const p = path.join(d, e.name);
    if (e.isDirectory()) { walk(p); continue; }
    if (!p.endsWith(".ts")) continue;
    fs.readFileSync(p, "utf8").split("\n").forEach((l, i) => {
      if (!l.includes("probe")) return;
      n++;
      console.log(p.split(path.sep).join("/") + "(" + (i + 1) + ",14): error TS2322: Type 'string' is not assignable to type 'number'.");
    });
  }
}
if (fs.existsSync("src")) walk("src");
process.exit(n ? 2 : 0);
`

// makeProbedRepo is a TS root whose committed src/a.ts already carries a
// type error, with the fake whole-project tsc installed.
func makeProbedRepo(t *testing.T, extra map[string]string) string {
	t.Helper()
	requireNode(t)
	files := map[string]string{
		"package.json":  `{"name": "app"}`,
		"tsconfig.json": plainTsconfig,
		"src/a.ts":      "export const probe: number = \"old\"\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	root := makeTSRepo(t, files)
	installFakePackage(t, root, "typescript", "tsc", fakeTscScript)
	return root
}

// An error that stood at HEAD in a file the commit never touched is not
// this commit's to answer for: holding it would refuse every commit to the
// root until somebody fixed it. The report still counts it, and the HEAD
// tree it was measured in leaves nothing behind.
func TestNpmBaseline_AnErrorAlreadyAtHeadInAnUntouchedFileDoesNotBlock(t *testing.T) {
	root := makeProbedRepo(t, nil)
	write(t, root, "src/b.ts", "export const b = 1\n")
	gitDo(t, root, "add", "src/b.ts")

	var res GateResult
	stderr := captureStderr(t, func() { res = Precommit(root, RunSuite(precommitTestTimeout)) })
	if res.Blocked {
		t.Fatalf("a HEAD-only error refused the commit:\n%s", res.Message)
	}
	if !strings.Contains(stderr, "1 already at HEAD") {
		t.Fatalf("the pass does not count the error it did not hold:\n%s", stderr)
	}
	if got := gitOut(root, "worktree", "list"); len(strings.Split(strings.TrimSpace(got), "\n")) != 1 {
		t.Fatalf("the HEAD worktree was left registered:\n%s", got)
	}
	if left, _ := filepath.Glob(filepath.Join(root, "node_modules", ".aphrollo-head-*")); len(left) != 0 {
		t.Fatalf("the HEAD tree was left on disk: %v", left)
	}
}

// A new error blocks, and the report lists it alone: the old one is a
// count, not a line the author has to read past.
func TestNpmBaseline_ANewErrorBlocksAndOnlyItIsListed(t *testing.T) {
	root := makeProbedRepo(t, nil)
	write(t, root, "src/b.ts", "export const probe2: number = \"new\"\n")
	gitDo(t, root, "add", "src/b.ts")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked {
		t.Fatalf("a new error committed cleanly: %+v", res)
	}
	if !strings.Contains(res.Message, "src/b.ts(1,14): error TS2322") || strings.Contains(res.Message, "src/a.ts(") {
		t.Fatalf("want b.ts's error listed and a.ts's not:\n%s", res.Message)
	}
	if !strings.Contains(res.Message, "1 diagnostic(s) already at HEAD") {
		t.Fatalf("the refusal does not count the HEAD error:\n%s", res.Message)
	}
}

// Lines added above an old error move it down; it is the same error, and a
// comparison that keyed on the line number would call it new.
func TestNpmBaseline_AnOldErrorMovedDownByAnEditStaysOld(t *testing.T) {
	root := makeProbedRepo(t, nil)
	write(t, root, "src/a.ts", "export const x = 1\nexport const y = 2\nexport const probe: number = \"old\"\n")
	gitDo(t, root, "add", "src/a.ts")

	if res := Precommit(root, RunSuite(precommitTestTimeout)); res.Blocked {
		t.Fatalf("a moved HEAD error refused the commit:\n%s", res.Message)
	}
}

// A second copy of an error HEAD already had once is new: the comparison
// counts, it does not just ask whether the kind was seen.
func TestNewDiagnostics_CountsRepeatsOfOneKey(t *testing.T) {
	d := func(key string) diagnostic { return diagnostic{Key: key, Line: key} }
	fresh := newDiagnostics([]diagnostic{d("a"), d("a"), d("b")}, []diagnostic{d("a")})
	if len(fresh) != 2 || fresh[0].Key != "a" || fresh[1].Key != "b" {
		t.Fatalf("new diagnostics = %+v, want one a and one b", fresh)
	}
}

// A repo with no commit yet has no HEAD to compare with, and the gate must
// say so and hold everything, never pass on a baseline it does not have.
func TestNpmBaseline_NoHeadCommitHoldsEveryErrorAndSaysSo(t *testing.T) {
	requireNode(t)
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, ".gitignore", "node_modules/\n")
	write(t, root, "package.json", `{"name": "app"}`)
	write(t, root, "tsconfig.json", plainTsconfig)
	installFakePackage(t, root, "typescript", "tsc", fakeTscScript)
	write(t, root, "src/a.ts", "export const probe: number = \"x\"\n")
	gitDo(t, root, "add", ".")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "no baseline at HEAD") {
		t.Fatalf("want a refusal naming the missing baseline, got %+v", res)
	}
}

// A HEAD run that dies without a diagnostic measured nothing, and nothing
// can be subtracted from it.
func TestNpmBaseline_AHeadRunWithNoReadingHoldsEveryError(t *testing.T) {
	root := makeProbedRepo(t, map[string]string{"CRASH": ""})
	gitDo(t, root, "rm", "-q", "CRASH")
	write(t, root, "src/b.ts", "export const b = 1\n")
	gitDo(t, root, "add", "src/b.ts")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "no baseline at HEAD") || !strings.Contains(res.Message, "src/a.ts(1,14)") {
		t.Fatalf("want a refusal holding a.ts's error for want of a baseline, got %+v", res)
	}
}

// The HEAD run is paid once per tree and command: the next commit on the
// same HEAD reads the cached result.
func TestNpmBaseline_TheHeadRunIsCachedByTreeAndCommand(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeProbedRepo(t, nil)
	write(t, root, "src/b.ts", "export const b = 1\n")
	gitDo(t, root, "add", "src/b.ts")

	headRuns := 0
	real := RunSuite(precommitTestTimeout)
	counting := func(r Runner, dir string) SuiteResult {
		if dir != root {
			headRuns++
		}
		return real(r, dir)
	}
	for range 2 {
		if res := Precommit(root, counting); res.Blocked {
			t.Fatalf("unexpected block: %s", res.Message)
		}
	}
	if headRuns != 1 {
		t.Fatalf("ran at HEAD %d times over two commits on one HEAD, want 1", headRuns)
	}
}

// ESLint's JSON names files absolutely; they are keyed relative to the run,
// so one file compares equal from either tree, and a warning, which does
// not fail the run, is not held against anyone.
func TestParseEslintDiagnostics_KeysErrorsByRelativeFileRuleAndMessage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "head")
	out := "(node) a warning on stderr\n" + `[{"filePath": ` + eslintJSONPath(filepath.Join(dir, "src", "c.js")) + `, "messages": [` +
		`{"ruleId": "no-unused-vars", "severity": 2, "message": "'u' is unused.", "line": 3, "column": 7},` +
		`{"ruleId": "semi", "severity": 1, "message": "Missing semicolon.", "line": 4, "column": 1},` +
		`{"ruleId": null, "severity": 2, "message": "Parsing error: x", "line": 9, "column": 1}]}]`
	got := parseEslintDiagnostics(out, dir)
	want := []diagnostic{
		{Key: "src/c.js\x00no-unused-vars\x00'u' is unused.", Line: "src/c.js:3:7: error 'u' is unused. (no-unused-vars)"},
		{Key: "src/c.js\x00\x00Parsing error: x", Line: "src/c.js:9:1: error Parsing error: x ()"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
}

func eslintJSONPath(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

// A cache entry that cannot be read is a miss, run again, never an empty
// baseline that would excuse nothing — or everything.
func TestReadHeadCache_AnUnreadableEntryIsAMiss(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "garbage.json", "not json")
	write(t, dir, "good.json", `[{"key": "k", "line": "l"}]`)
	for _, name := range []string{"garbage.json", "absent.json"} {
		if _, ok := readHeadCache(filepath.Join(dir, name)); ok {
			t.Errorf("%s read as a cache hit", name)
		}
	}
	if got, ok := readHeadCache(filepath.Join(dir, "good.json")); !ok || len(got) != 1 || got[0].Key != "k" {
		t.Errorf("good entry = %v, %v", got, ok)
	}
}

// The exit status is the verdict: a run that exits 0 passed, whatever
// error-shaped text it printed, and costs no run at HEAD.
func TestNpmBaseline_ARunThatExitsZeroPassesWithoutAHeadRun(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json":  `{"name": "app"}`,
		"tsconfig.json": plainTsconfig,
	})
	installFakePackage(t, root, "typescript", "tsc", "")
	write(t, root, "src/b.ts", "export const b = 1\n")
	gitDo(t, root, "add", "src/b.ts")

	runs := 0
	passing := func(Runner, string) SuiteResult {
		runs++
		return SuiteResult{Passed: true, Output: "src/b.ts(1,14): error TS2322: printed, yet the run exited 0.\n"}
	}
	if res := Precommit(root, passing); res.Blocked {
		t.Fatalf("a run that exited 0 refused the commit:\n%s", res.Message)
	}
	if runs != 1 {
		t.Fatalf("ran %d times for one passing typecheck, want 1 (no run at HEAD)", runs)
	}
}
