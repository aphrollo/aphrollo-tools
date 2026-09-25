package precommit

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ratchet: test_removed TestNpmChecks_TypecheckAndLintScriptsArePreferredOverTheLocalBinaries: the script preference is gone — npm is npm.cmd on Windows, and cmd.exe mangles the arguments; a custom command is declared in [aphrollo.precommit] instead
// ratchet: test_removed TestNpmChecks_DeclaredScriptsWithoutNodeModulesSayNotRun: the script preference it guarded is gone; the NOT RUN line without node_modules is TestNpmChecks_NoNodeModulesPrintsNotRunNamingNpmCi
// ratchet: test_removed TestNpmLocalBin_NamesTheShimNpmInstallsOnEachHost: the gate no longer runs the node_modules/.bin shim on any host; the tool runs as node <package entry>, pinned by TestNpmBinEntry_ReadsThePackagesBinInBothShapes

// makeTSRepo builds a committed npm root holding the given files, with
// node_modules ignored so an installed tool never reaches the index. The
// caller stages the change under test itself.
func makeTSRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, ".gitignore", "node_modules/\n")
	for rel, content := range files {
		write(t, root, rel, content)
	}
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	return root
}

// fakeNode is the node binary the argv tests see: no process runs there.
const fakeNode = "/fake/node"

func withFakeNode(t *testing.T) {
	t.Helper()
	prev := lookNode
	lookNode = func() (string, error) { return fakeNode, nil }
	t.Cleanup(func() { lookNode = prev })
}

// requireNode points the gate at the real node, for the tests whose tool
// actually runs.
func requireNode(t *testing.T) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; the fake tool is a node script") // skip-ok: the tool under test runs on node, which this box lacks
	}
	prev := lookNode
	lookNode = func() (string, error) { return node, nil }
	t.Cleanup(func() { lookNode = prev })
}

// installFakePackage installs pkg into root's node_modules the way npm lays
// it out: a package.json whose "bin" names bin, and that script.
func installFakePackage(t *testing.T, root, pkg, bin, script string) {
	t.Helper()
	dir := filepath.Join("node_modules", pkg)
	write(t, root, filepath.Join(dir, "package.json"), fmt.Sprintf(`{"name": %q, "bin": {%q: "./bin/%s.js"}}`, pkg, bin, bin))
	write(t, root, filepath.Join(dir, "bin", bin+".js"), script)
}

// entry is where installFakePackage put pkg's bin script.
func entry(root, pkg, bin string) string {
	return filepath.Join(root, "node_modules", pkg, "bin", bin+".js")
}

func runLines(seen []Runner) []string {
	var out []string
	for _, r := range seen {
		out = append(out, cmdLine(r))
	}
	return out
}

const plainTsconfig = `{"compilerOptions": {"strict": true, "noEmit": true}, "include": ["src"]}`

// The issue's shape: a strict-TS root with an eslint config and its tools
// installed. The commit must typecheck the root with the root's OWN tsc, then
// lint the staged file with the root's own eslint, each run by node, in that
// order and nothing else in their place: a missing entry is the TS2322 that
// committed cleanly.
func TestNpmChecks_CleanRootRunsLocalTscThenEslintOverTheStagedFiles(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json":     `{"name": "app"}`,
		"tsconfig.json":    plainTsconfig,
		"eslint.config.js": "export default []\n",
	})
	installFakePackage(t, root, "typescript", "tsc", "")
	installFakePackage(t, root, "eslint", "eslint", "")
	write(t, root, "src/widget.ts", "export const widget = 1\n")
	gitDo(t, root, "add", "src/widget.ts")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{
		fakeNode + " " + entry(root, "typescript", "tsc") + " -p tsconfig.json --noEmit",
		fakeNode + " " + entry(root, "eslint", "eslint") + " --format json src/widget.ts",
	}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("npm checks ran %v, want %v", runLines(seen), want)
	}
}

// A staged path carrying every character cmd.exe rewrites reaches eslint as
// one untouched argument: the reason the tool runs under node and not
// through its .cmd shim.
func TestNpmChecks_AStagedPathWithShellMetacharactersReachesEslintVerbatim(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json":     `{"name": "app"}`,
		"eslint.config.js": "export default []\n",
	})
	installFakePackage(t, root, "eslint", "eslint", "")
	const odd = "src/a b&c^d(e)%f.ts"
	write(t, root, odd, "export const odd = 1\n")
	gitDo(t, root, "add", odd)

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{entry(root, "eslint", "eslint"), "--format", "json", odd}
	if len(seen) != 1 || seen[0].Cmd != fakeNode || fmt.Sprintf("%q", seen[0].Args) != fmt.Sprintf("%q", want) {
		t.Fatalf("eslint ran %+v, want %s %q", seen, fakeNode, want)
	}
}

// A type error must refuse the commit and show what tsc printed: the error
// line is the whole point of the refusal.
func TestNpmChecks_TypeErrorBlocksTheCommitAndShowsTscsError(t *testing.T) {
	requireNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json":  `{"name": "app"}`,
		"tsconfig.json": plainTsconfig,
	})
	installFakePackage(t, root, "typescript", "tsc", fakeTscScript)
	write(t, root, "src/widget.ts", "export const probe: number = \"not a number\"\n")
	gitDo(t, root, "add", "src/widget.ts")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked {
		t.Fatalf("a type error committed cleanly: %+v", res)
	}
	if !strings.Contains(res.Message, "src/widget.ts(1,14): error TS2322") {
		t.Fatalf("the refusal does not show tsc's error:\n%s", res.Message)
	}
	// HEAD was clean, and measured: nothing to count, nothing to excuse.
	if strings.Contains(res.Message, "already at HEAD") || strings.Contains(res.Message, "no baseline") {
		t.Fatalf("a clean HEAD reads as something else in the refusal:\n%s", res.Message)
	}
}

// Vite's root tsconfig.json lists no files and only references the real
// projects, so `tsc -p tsconfig.json --noEmit` checks nothing and exits 0.
// Each referenced project has to be checked in its own right.
func TestNpmChecks_SolutionStyleTsconfigChecksEachReferencedProject(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json": `{"name": "app"}`,
		"tsconfig.json": `{
  "files": [],
  "references": [
    { "path": "./tsconfig.app.json" },
    { "path": "./tsconfig.node.json" }
  ]
}`,
		"tsconfig.app.json":  plainTsconfig,
		"tsconfig.node.json": `{"include": ["vite.config.ts"]}`,
	})
	installFakePackage(t, root, "typescript", "tsc", "")
	write(t, root, "src/widget.ts", "export const widget = 1\n")
	gitDo(t, root, "add", "src/widget.ts")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	tsc := fakeNode + " " + entry(root, "typescript", "tsc")
	want := []string{tsc + " -p ./tsconfig.app.json --noEmit", tsc + " -p ./tsconfig.node.json --noEmit"}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("npm checks ran %v, want %v", runLines(seen), want)
	}
}

// tsconfig is JSONC: comments and trailing commas are legal there, and a
// reader that gives up on them falls back to the root config, which in the
// solution-style shape checks nothing.
func TestNpmChecks_TsconfigCommentsAndTrailingCommasStillYieldItsReferences(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json": `{"name": "app"}`,
		"tsconfig.json": `// the solution file
{
  /* nothing of its own */
  "files": [],
  "references": [
    { "path": "./tsconfig.app.json" }, // the app
  ],
}`,
		"tsconfig.app.json": plainTsconfig,
	})
	installFakePackage(t, root, "typescript", "tsc", "")
	write(t, root, "src/widget.ts", "export const widget = 1\n")
	gitDo(t, root, "add", "src/widget.ts")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{fakeNode + " " + entry(root, "typescript", "tsc") + " -p ./tsconfig.app.json --noEmit"}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("npm checks ran %v, want %v", runLines(seen), want)
	}
}

// A root whose tools are not installed cannot be checked, and must say so
// loudly with the fix: silence would read as a pass it never earned.
func TestNpmChecks_NoNodeModulesPrintsNotRunNamingNpmCi(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json":     `{"name": "app"}`,
		"tsconfig.json":    plainTsconfig,
		"eslint.config.js": "export default []\n",
	})
	write(t, root, "src/widget.ts", "export const widget = 1\n")
	gitDo(t, root, "add", "src/widget.ts")

	var seen []Runner
	var res GateResult
	stderr := captureStderr(t, func() { res = Precommit(root, runsAt(&seen, root)) })
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	if len(seen) != 0 {
		t.Fatalf("ran %v with no tool installed", runLines(seen))
	}
	for _, tool := range []string{"tsc", "eslint"} {
		found := false
		for line := range strings.Lines(stderr) {
			if strings.Contains(line, "gate precommit: "+tool+" in") && strings.Contains(line, "NOT RUN") && strings.Contains(line, "npm ci") {
				found = true
			}
		}
		if !found {
			t.Fatalf("no NOT RUN line naming npm ci for %s:\n%s", tool, stderr)
		}
	}
}

// Installed tools with no node to run them are just as unchecked, and the
// line has to say what is missing.
func TestNpmChecks_NoNodeOnPathPrintsNotRun(t *testing.T) {
	prev := lookNode
	lookNode = func() (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookNode = prev })
	root := makeTSRepo(t, map[string]string{
		"package.json":  `{"name": "app"}`,
		"tsconfig.json": plainTsconfig,
	})
	installFakePackage(t, root, "typescript", "tsc", "")
	write(t, root, "src/widget.ts", "export const widget = 1\n")
	gitDo(t, root, "add", "src/widget.ts")

	var seen []Runner
	var res GateResult
	stderr := captureStderr(t, func() { res = Precommit(root, runsAt(&seen, root)) })
	if res.Blocked || len(seen) != 0 {
		t.Fatalf("want no block and no run, got %+v and %v", res, runLines(seen))
	}
	if !strings.Contains(stderr, "NOT RUN — node is not on PATH") {
		t.Fatalf("no NOT RUN line naming node:\n%s", stderr)
	}
}

// Two branches that each typecheck can merge into a tree that does not, so
// the merge gate typechecks too, and before the suite, which costs more.
func TestNpmChecks_MergeGateTypechecksBeforeTheSuite(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json":  `{"name": "app", "scripts": {"test": "echo ok"}}`,
		"tsconfig.json": plainTsconfig,
	})
	installFakePackage(t, root, "typescript", "tsc", "")
	// Installed but not configured: eslint has no rules to run here.
	installFakePackage(t, root, "eslint", "eslint", "")
	write(t, root, "src/widget.ts", "export const widget = 1\n")
	gitDo(t, root, "add", "src/widget.ts")

	var seen []Runner
	if res := Mechanical(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{fakeNode + " " + entry(root, "typescript", "tsc") + " -p tsconfig.json --noEmit", "npm test --silent"}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("merge gate ran %v, want %v", runLines(seen), want)
	}
}

// A JavaScript root with an eslint config and no tsconfig has nothing to
// typecheck: running tsc there would fail on a missing config.
func TestNpmChecks_NoTsconfigLintsOnly(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json":     `{"name": "app"}`,
		"eslint.config.js": "export default []\n",
	})
	installFakePackage(t, root, "typescript", "tsc", "")
	installFakePackage(t, root, "eslint", "eslint", "")
	write(t, root, "src/widget.js", "export const widget = 1\n")
	gitDo(t, root, "add", "src/widget.js")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{fakeNode + " " + entry(root, "eslint", "eslint") + " --format json src/widget.js"}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("npm checks ran %v, want %v", runLines(seen), want)
	}
}

// eslint is handed the staged files it lints and that still exist: a
// deleted file fails the whole run on its path, and a Python helper is not
// eslint's to judge.
func TestNpmChecks_EslintGetsOnlyStagedLintableFilesThatExist(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json":     `{"name": "app"}`,
		"eslint.config.js": "export default []\n",
		"src/gone.ts":      "export const gone = 1\n",
	})
	installFakePackage(t, root, "eslint", "eslint", "")
	write(t, root, "src/widget.ts", "export const widget = 1\n")
	write(t, root, "tools/gen.py", "print(1)\n")
	gitDo(t, root, "rm", "-q", "src/gone.ts")
	gitDo(t, root, "add", "src/widget.ts", "tools/gen.py")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{fakeNode + " " + entry(root, "eslint", "eslint") + " --format json src/widget.ts"}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("npm checks ran %v, want %v", runLines(seen), want)
	}
}

// The npm checks belong to an npm root: a Go module that happens to carry a
// tsconfig.json is judged by vet and lint, not by a tsc it never declared.
func TestNpmChecks_ARootWithoutPackageJSONIsNotTypechecked(t *testing.T) {
	withLinter(t, false)
	withFakeNode(t)
	root := makeGoRepo(t)
	write(t, root, "tsconfig.json", plainTsconfig)
	installFakePackage(t, root, "typescript", "tsc", "")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", "widget.go")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{"go vet ./..."}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("a Go root ran %v, want %v", runLines(seen), want)
	}
}

// A package's "bin" is a map of command names, or one string for a package
// with a single command; a name it does not declare, or a script that is not
// there, is no tool at all.
func TestNpmBinEntry_ReadsThePackagesBinInBothShapes(t *testing.T) {
	root := t.TempDir()
	write(t, root, "map/package.json", `{"bin": {"tsserver": "./bin/tsserver", "tsc": "./bin/tsc"}}`)
	write(t, root, "map/bin/tsc", "")
	write(t, root, "one/package.json", `{"bin": "bin/cli.js"}`)
	write(t, root, "one/bin/cli.js", "")
	write(t, root, "gone/package.json", `{"bin": {"tsc": "./bin/tsc"}}`)
	cases := []struct{ pkg, name, want string }{
		{"map", "tsc", filepath.Join(root, "map", "bin", "tsc")},
		{"map", "eslint", ""},
		{"one", "eslint", filepath.Join(root, "one", "bin", "cli.js")},
		{"gone", "tsc", ""},
		{"absent", "tsc", ""},
	}
	for _, tc := range cases {
		if got := npmBinEntry(filepath.Join(root, tc.pkg), tc.name); got != tc.want {
			t.Errorf("npmBinEntry(%s, %s) = %q, want %q", tc.pkg, tc.name, got, tc.want)
		}
	}
}

// Only a config that checks nothing of its own is a solution file; any
// other shape is typechecked as itself, or its own files go unchecked.
func TestTscArgvs_OnlyAConfigThatChecksNothingItselfIsSplitIntoItsReferences(t *testing.T) {
	root := t.TempDir()
	refs := `"references": [{"path": "./tsconfig.app.json"}]`
	self := [][]string{{"-p", "tsconfig.json", "--noEmit"}}
	cases := []struct {
		name, tsconfig string
		want           [][]string
	}{
		{"solution file", `{"files": [], ` + refs + `}`, [][]string{{"-p", "./tsconfig.app.json", "--noEmit"}}},
		{"references beside the default include", `{` + refs + `}`, self},
		{"references beside files of its own", `{"files": ["src/main.ts"], ` + refs + `}`, self},
		{"references beside an include of its own", `{"files": [], "include": ["src"], ` + refs + `}`, self},
		{"no references", `{"files": []}`, self},
	}
	for _, tc := range cases {
		write(t, root, "tsconfig.json", tc.tsconfig)
		if got := tscArgvs(root, nil); fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s: tsc argvs = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// tsconfig's comments go and its strings stay whole, including the ones
// that look like comments: Vite writes "src/**/*" and "@/*" paths.
func TestJsoncToJSON_StripsCommentsButNeverStringContent(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"a": 1} // trailing`, `{"a": 1} `},
		{"{\n// line\n\"a\": 1}", "{\n\n\"a\": 1}"},
		{`{/* block */"a": 1}`, `{"a": 1}`},
		{`{/** starred **/"a": 1}`, `{"a": 1}`},
		{`{/* a * b */"a": 1}`, `{"a": 1}`},
		{`{"include": ["src/**/*"]}`, `{"include": ["src/**/*"]}`},
		{`{"p": "a//b"}`, `{"p": "a//b"}`},
		{`{"q": "say \"//\" x"}`, `{"q": "say \"//\" x"}`},
		{`{"e": "back\\"}// c`, `{"e": "back\\"}`},
		{`{"a": [1, 2,], }`, `{"a": [1, 2] }`},
		{`{"a": 1 / 2}`, `{"a": 1 / 2}`},
	}
	for _, tc := range cases {
		if got := jsoncToJSON(tc.in); got != tc.want {
			t.Errorf("jsoncToJSON(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The same eslint run at HEAD lints only the files HEAD had: a file this
// commit adds had no diagnostics before it, and naming it would fail the
// HEAD run on a path.
func TestEslintHeadArgs_KeepsOnlyTheFilesHeadHad(t *testing.T) {
	head := t.TempDir()
	write(t, head, "src/old.ts", "")
	args := []string{"--format", "json", "src/old.ts", "src/new.ts"}
	if got := eslintHeadArgs(head, args); fmt.Sprint(got) != fmt.Sprint([]string{"--format", "json", "src/old.ts"}) {
		t.Errorf("head args = %v, want the old file only", got)
	}
	if got := eslintHeadArgs(head, []string{"--format", "json", "src/new.ts"}); got != nil {
		t.Errorf("head args = %v, want none when HEAD had none of the files", got)
	}
}
