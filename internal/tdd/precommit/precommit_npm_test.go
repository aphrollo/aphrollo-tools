package precommit

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeTSRepo builds a committed npm root holding the given files, with
// node_modules ignored so a fake local tool never reaches the index. The
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

// localBin is where the root's own copy of tool lives, as npm installs it.
func localBin(root, tool string) string {
	if runtime.GOOS == "windows" {
		tool += ".cmd"
	}
	return filepath.Join(root, "node_modules", ".bin", tool)
}

// installFakeTool puts an executable standing in for tool into the root's
// node_modules/.bin: it prints output (when not empty) and exits with code,
// as a shell script, or as the .cmd npm installs on Windows.
func installFakeTool(t *testing.T, root, tool, output string, code int) {
	t.Helper()
	p := localBin(root, tool)
	body := "#!/bin/sh\n"
	if output != "" {
		body += fmt.Sprintf("echo %q\n", output)
	}
	body += fmt.Sprintf("exit %d\n", code)
	if runtime.GOOS == "windows" {
		body = "@echo off\r\n"
		if output != "" {
			body += "echo " + output + "\r\n"
		}
		body += fmt.Sprintf("exit /b %d\r\n", code)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
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
// lint the staged file with the root's own eslint, in that order and nothing
// else in their place: a missing entry is the TS2322 that committed cleanly.
func TestNpmChecks_CleanRootRunsLocalTscThenEslintOverTheStagedFiles(t *testing.T) {
	root := makeTSRepo(t, map[string]string{
		"package.json":     `{"name": "app"}`,
		"tsconfig.json":    plainTsconfig,
		"eslint.config.js": "export default []\n",
	})
	installFakeTool(t, root, "tsc", "", 0)
	installFakeTool(t, root, "eslint", "", 0)
	write(t, root, "src/widget.ts", "export const widget = 1\n")
	gitDo(t, root, "add", "src/widget.ts")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{
		localBin(root, "tsc") + " -p tsconfig.json --noEmit",
		localBin(root, "eslint") + " src/widget.ts",
	}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("npm checks ran %v, want %v", runLines(seen), want)
	}
}

// A type error must refuse the commit and show what tsc printed: the error
// line is the whole point of the refusal.
func TestNpmChecks_TypeErrorBlocksTheCommitAndShowsTscsError(t *testing.T) {
	root := makeTSRepo(t, map[string]string{
		"package.json":  `{"name": "app"}`,
		"tsconfig.json": plainTsconfig,
	})
	installFakeTool(t, root, "tsc",
		"src/widget.ts(1,14): error TS2322: Type 'string' is not assignable to type 'number'.", 2)
	write(t, root, "src/widget.ts", "export const probe: number = \"not a number\"\n")
	gitDo(t, root, "add", "src/widget.ts")

	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked {
		t.Fatalf("a type error committed cleanly: %+v", res)
	}
	if !strings.Contains(res.Message, "error TS2322") {
		t.Fatalf("the refusal does not show tsc's error:\n%s", res.Message)
	}
}

// Vite's root tsconfig.json lists no files and only references the real
// projects, so `tsc -p tsconfig.json --noEmit` checks nothing and exits 0.
// Each referenced project has to be checked in its own right.
func TestNpmChecks_SolutionStyleTsconfigChecksEachReferencedProject(t *testing.T) {
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
	installFakeTool(t, root, "tsc", "", 0)
	write(t, root, "src/widget.ts", "export const widget = 1\n")
	gitDo(t, root, "add", "src/widget.ts")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{
		localBin(root, "tsc") + " -p ./tsconfig.app.json --noEmit",
		localBin(root, "tsc") + " -p ./tsconfig.node.json --noEmit",
	}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("npm checks ran %v, want %v", runLines(seen), want)
	}
}

// tsconfig is JSONC: comments and trailing commas are legal there, and a
// reader that gives up on them falls back to the root config, which in the
// solution-style shape checks nothing.
func TestNpmChecks_TsconfigCommentsAndTrailingCommasStillYieldItsReferences(t *testing.T) {
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
	installFakeTool(t, root, "tsc", "", 0)
	write(t, root, "src/widget.ts", "export const widget = 1\n")
	gitDo(t, root, "add", "src/widget.ts")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{localBin(root, "tsc") + " -p ./tsconfig.app.json --noEmit"}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("npm checks ran %v, want %v", runLines(seen), want)
	}
}

// A root whose tools are not installed cannot be checked, and must say so
// loudly with the fix: silence would read as a pass it never earned.
func TestNpmChecks_NoNodeModulesPrintsNotRunNamingNpmCi(t *testing.T) {
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
			if strings.Contains(line, tool) && strings.Contains(line, "NOT RUN") && strings.Contains(line, "npm ci") {
				found = true
			}
		}
		if !found {
			t.Fatalf("no NOT RUN line naming npm ci for %s:\n%s", tool, stderr)
		}
	}
}

// A root that declares its own typecheck and lint scripts has said how it is
// checked; the gate runs those rather than second-guessing their flags.
func TestNpmChecks_TypecheckAndLintScriptsArePreferredOverTheLocalBinaries(t *testing.T) {
	root := makeTSRepo(t, map[string]string{
		"package.json":     `{"name": "app", "scripts": {"typecheck": "tsc -b", "lint": "eslint ."}}`,
		"tsconfig.json":    plainTsconfig,
		"eslint.config.js": "export default []\n",
	})
	installFakeTool(t, root, "tsc", "", 0)
	installFakeTool(t, root, "eslint", "", 0)
	write(t, root, "src/widget.ts", "export const widget = 1\n")
	gitDo(t, root, "add", "src/widget.ts")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{"npm run typecheck", "npm run lint"}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("npm checks ran %v, want %v", runLines(seen), want)
	}
}

// Two branches that each typecheck can merge into a tree that does not, so
// the merge gate typechecks too, and before the suite, which costs more.
func TestNpmChecks_MergeGateTypechecksBeforeTheSuite(t *testing.T) {
	root := makeTSRepo(t, map[string]string{
		"package.json":  `{"name": "app", "scripts": {"test": "echo ok"}}`,
		"tsconfig.json": plainTsconfig,
	})
	installFakeTool(t, root, "tsc", "", 0)
	// Installed but not configured: eslint has no rules to run here.
	installFakeTool(t, root, "eslint", "", 0)
	write(t, root, "src/widget.ts", "export const widget = 1\n")
	gitDo(t, root, "add", "src/widget.ts")

	var seen []Runner
	if res := Mechanical(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{localBin(root, "tsc") + " -p tsconfig.json --noEmit", "npm test --silent"}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("merge gate ran %v, want %v", runLines(seen), want)
	}
}

// A declared script still needs the root's dependencies to run; without
// node_modules it would fail on a missing tsc and read as a type error.
func TestNpmChecks_DeclaredScriptsWithoutNodeModulesSayNotRun(t *testing.T) {
	root := makeTSRepo(t, map[string]string{
		"package.json":     `{"name": "app", "scripts": {"typecheck": "tsc -b", "lint": "eslint ."}}`,
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
		t.Fatalf("ran %v with no node_modules", runLines(seen))
	}
	if strings.Count(stderr, "NOT RUN — node_modules/.bin/") != 2 {
		t.Fatalf("want a NOT RUN line for each of typecheck and lint:\n%s", stderr)
	}
}

// A JavaScript root with an eslint config and no tsconfig has nothing to
// typecheck: running tsc there would fail on a missing config.
func TestNpmChecks_NoTsconfigLintsOnly(t *testing.T) {
	root := makeTSRepo(t, map[string]string{
		"package.json":     `{"name": "app"}`,
		"eslint.config.js": "export default []\n",
	})
	installFakeTool(t, root, "tsc", "", 0)
	installFakeTool(t, root, "eslint", "", 0)
	write(t, root, "src/widget.js", "export const widget = 1\n")
	gitDo(t, root, "add", "src/widget.js")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{localBin(root, "eslint") + " src/widget.js"}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("npm checks ran %v, want %v", runLines(seen), want)
	}
}

// eslint is handed the staged files it lints and that still exist: a
// deleted file fails the whole run on its path, and a Python helper is not
// eslint's to judge.
func TestNpmChecks_EslintGetsOnlyStagedLintableFilesThatExist(t *testing.T) {
	root := makeTSRepo(t, map[string]string{
		"package.json":     `{"name": "app"}`,
		"eslint.config.js": "export default []\n",
		"src/gone.ts":      "export const gone = 1\n",
	})
	installFakeTool(t, root, "eslint", "", 0)
	write(t, root, "src/widget.ts", "export const widget = 1\n")
	write(t, root, "tools/gen.py", "print(1)\n")
	gitDo(t, root, "rm", "-q", "src/gone.ts")
	gitDo(t, root, "add", "src/widget.ts", "tools/gen.py")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{localBin(root, "eslint") + " src/widget.ts"}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("npm checks ran %v, want %v", runLines(seen), want)
	}
}

// The npm checks belong to an npm root: a Go module that happens to carry a
// tsconfig.json is judged by vet and lint, not by a tsc it never declared.
func TestNpmChecks_ARootWithoutPackageJSONIsNotTypechecked(t *testing.T) {
	withLinter(t, false)
	root := makeGoRepo(t)
	write(t, root, "tsconfig.json", plainTsconfig)
	installFakeTool(t, root, "tsc", "", 0)
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

// npm installs a .cmd shim on Windows and a bare script elsewhere; naming
// the wrong one reads an installed tool as missing on that host.
func TestNpmLocalBin_NamesTheShimNpmInstallsOnEachHost(t *testing.T) {
	prev := npmBinGOOS
	t.Cleanup(func() { npmBinGOOS = prev })
	for goos, want := range map[string]string{"windows": "tsc.cmd", "linux": "tsc", "darwin": "tsc"} {
		npmBinGOOS = goos
		if got := filepath.Base(npmLocalBin("root", "tsc")); got != want {
			t.Errorf("GOOS %s: local tsc = %s, want %s", goos, got, want)
		}
	}
}
