package precommit

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #948 part 2: an npm root's typecheck was tsc or nothing. A
// SvelteKit root's tsc checks no component, and a repo could not say what
// its root's typecheck is. The typecheck is now, in order: the root's argv
// in aphrollo.toml [aphrollo.typecheck], its package.json "typecheck" or
// "check" script, svelte-check when the root depends on it, and tsc. Every
// tool runs as `node <its bin entry>`.

// svelteKitManifest is a SvelteKit root's package.json with no scripts.
const svelteKitManifest = `{"name": "web", "type": "module", "devDependencies": {"@sveltejs/kit": "2.20.0", "svelte-check": "4.1.0", "typescript": "5.8.0"}}`

// installSvelteKitTools installs the fake svelte-kit, svelte-check and tsc a
// SvelteKit root carries, each with the given script.
func installSvelteKitTools(t *testing.T, root, kit, check string) {
	t.Helper()
	installFakePackage(t, root, "@sveltejs/kit", "svelte-kit", kit)
	installFakePackage(t, root, "svelte-check", "svelte-check", check)
	installFakePackage(t, root, "typescript", "tsc", "")
}

// A SvelteKit root depending on svelte-check is typechecked by it, after
// svelte-kit sync writes the types it reads, and not by tsc, which checks
// no component.
func TestNpmTypecheck_ASvelteKitRootRunsSyncThenSvelteCheckInsteadOfTsc(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json":  svelteKitManifest,
		"tsconfig.json": plainTsconfig,
	})
	installSvelteKitTools(t, root, "", "")
	write(t, root, "src/lib/a.ts", "export const a = 1\n")
	gitDo(t, root, "add", "src/lib/a.ts")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{
		fakeNode + " " + entry(root, "@sveltejs/kit", "svelte-kit") + " sync",
		fakeNode + " " + entry(root, "svelte-check", "svelte-check") + " --tsconfig ./tsconfig.json --output machine",
	}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("typecheck ran %v, want %v", runLines(seen), want)
	}
}

// A root whose package.json says how it is typechecked is typechecked that
// way, each `&&` step under node; "typecheck" wins over "check", which many
// repos spend on formatting.
func TestNpmTypecheck_TheRootsOwnScriptIsRunUnderNode(t *testing.T) {
	cases := []struct {
		name, scripts string
		want          func(root string) []string
	}{
		{
			name:    "check script",
			scripts: `{"check": "svelte-kit sync && svelte-check --tsconfig './tsconfig.json' --threshold error"}`,
			want: func(root string) []string {
				return []string{
					fakeNode + " " + entry(root, "@sveltejs/kit", "svelte-kit") + " sync",
					fakeNode + " " + entry(root, "svelte-check", "svelte-check") + " --tsconfig ./tsconfig.json --threshold error --output machine",
				}
			},
		},
		{
			name:    "typecheck over check",
			scripts: `{"check": "prettier --check .", "typecheck": "tsc --noEmit -p tsconfig.app.json"}`,
			want: func(root string) []string {
				return []string{fakeNode + " " + entry(root, "typescript", "tsc") + " --noEmit -p tsconfig.app.json"}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withFakeNode(t)
			root := makeTSRepo(t, map[string]string{
				"package.json":  `{"name": "web", "scripts": ` + tc.scripts + `}`,
				"tsconfig.json": plainTsconfig,
			})
			installSvelteKitTools(t, root, "", "")
			write(t, root, "src/lib/a.ts", "export const a = 1\n")
			gitDo(t, root, "add", "src/lib/a.ts")

			var seen []Runner
			if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
				t.Fatalf("unexpected block: %s", res.Message)
			}
			if got, want := strings.Join(runLines(seen), " | "), strings.Join(tc.want(root), " | "); got != want {
				t.Fatalf("typecheck ran %v, want %v", got, want)
			}
		})
	}
}

// A script the gate cannot run without a shell is not guessed at: nothing
// runs, the commit is not refused over it, and the line names the script
// and the key that declares the command instead.
func TestNpmTypecheck_AScriptNeedingAShellSaysNotRun(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json":  `{"name": "web", "scripts": {"typecheck": "tsc --noEmit | tee out.log"}}`,
		"tsconfig.json": plainTsconfig,
	})
	installFakePackage(t, root, "typescript", "tsc", "")
	write(t, root, "src/lib/a.ts", "export const a = 1\n")
	gitDo(t, root, "add", "src/lib/a.ts")

	var seen []Runner
	var res GateResult
	stderr := captureStderr(t, func() { res = Precommit(root, runsAt(&seen, root)) })
	if res.Blocked || len(seen) != 0 {
		t.Fatalf("want no block and no run, got %+v and %v", res, runLines(seen))
	}
	if !strings.Contains(stderr, "NOT RUN") || !strings.Contains(stderr, `script "typecheck"`) || !strings.Contains(stderr, "[aphrollo.typecheck]") {
		t.Fatalf("no NOT RUN line naming the script and the key:\n%s", stderr)
	}
}

// aphrollo.toml declares a root's typecheck and lint as argv, and the gate
// runs exactly those, each bin found in whichever installed package
// declares it, and eslint asked for the output it reads.
func TestNpmTypecheck_AphrolloTomlDeclaresTheRootsTypecheckAndLint(t *testing.T) {
	withFakeNode(t)
	repo := makeTSRepo(t, map[string]string{
		"aphrollo.toml":     "[aphrollo.typecheck]\n\"web\" = [\"vue-tsc\", \"--noEmit\"]\n\n[aphrollo.lint]\n\"web\" = [\"eslint\", \"src\"]\n",
		"web/package.json":  `{"name": "web", "scripts": {"check": "tsc --noEmit"}}`,
		"web/tsconfig.json": plainTsconfig,
	})
	root := repo + "/web"
	installFakePackage(t, root, "@vue/typecheck", "vue-tsc", "")
	installFakePackage(t, root, "typescript", "tsc", "")
	installFakePackage(t, root, "eslint", "eslint", "")
	write(t, root, "src/a.ts", "export const a = 1\n")
	gitDo(t, repo, "add", "web/src/a.ts")

	var seen []Runner
	if res := Precommit(repo, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{
		fakeNode + " " + entry(root, "@vue/typecheck", "vue-tsc") + " --noEmit",
		fakeNode + " " + entry(root, "eslint", "eslint") + " src --format json",
	}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("declared checks ran %v, want %v", runLines(seen), want)
	}
}

// A declaration the gate cannot read refuses the commit: falling back to
// what it would have detected judges the root by rules its repo replaced.
func TestNpmTypecheck_AnUnreadableDeclarationRefuses(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"aphrollo.toml": "[aphrollo.typecheck]\n\".\" = \"tsc --noEmit\"\n",
		"package.json":  `{"name": "web"}`,
		"tsconfig.json": plainTsconfig,
	})
	installFakePackage(t, root, "typescript", "tsc", "")
	write(t, root, "src/a.ts", "export const a = 1\n")
	gitDo(t, root, "add", "src/a.ts")

	var seen []Runner
	res := Precommit(root, runsAt(&seen, root))
	if !res.Blocked || !strings.Contains(res.Message, "[aphrollo.typecheck]") {
		t.Fatalf("want a refusal naming the declaration, got %+v", res)
	}
	if len(seen) != 0 {
		t.Fatalf("ran %v over an unreadable declaration", runLines(seen))
	}
}

// fakeSvelteKitScript is svelte-kit: `sync` writes the tsconfig the root's
// own extends, as the real one does.
const fakeSvelteKitScript = `const fs = require("fs");
if (process.argv[2] === "sync") {
  fs.mkdirSync(".svelte-kit", { recursive: true });
  fs.writeFileSync(".svelte-kit/tsconfig.json", "{}");
}
`

// fakeSvelteCheckScript is svelte-check in machine output: it cannot start
// before svelte-kit sync has run, and reports an error on every component
// under src/ that declares a probe.
const fakeSvelteCheckScript = `const fs = require("fs"), path = require("path");
if (!fs.existsSync(".svelte-kit/tsconfig.json")) {
  console.log("Cannot find base config file ./.svelte-kit/tsconfig.json");
  process.exit(1);
}
console.log(Date.now() + " START " + JSON.stringify(process.cwd()));
let n = 0;
function walk(d) {
  for (const e of fs.readdirSync(d, { withFileTypes: true }).sort((a, b) => a.name < b.name ? -1 : 1)) {
    const p = path.join(d, e.name);
    if (e.isDirectory()) { walk(p); continue; }
    if (!p.endsWith(".svelte") || !fs.readFileSync(p, "utf8").includes("probe")) continue;
    n++;
    console.log(Date.now() + " ERROR " + JSON.stringify(p.split(path.sep).join("/")) + " 1:5 \"Type 'string' is not assignable to type 'number'.\"");
  }
}
walk("src");
console.log(Date.now() + " COMPLETED 3 FILES " + n + " ERRORS 0 WARNINGS " + n + " FILES_WITH_PROBLEMS");
process.exit(n > 0 ? 1 : 0);
`

// svelte-check checks the whole project, so an error standing at HEAD is
// measured there too, after the same svelte-kit sync, and only an error
// the commit adds refuses it.
func TestNpmTypecheck_SvelteCheckHoldsOnlyWhatTheCommitAddsAfterSyncAtHead(t *testing.T) {
	requireNode(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeTSRepo(t, map[string]string{
		".gitignore":          "node_modules/\n.svelte-kit/\n",
		"package.json":        svelteKitManifest,
		"tsconfig.json":       `{"extends": "./.svelte-kit/tsconfig.json"}`,
		"src/lib/Old.svelte":  "<script lang=\"ts\">const probe: number = \"x\"</script>\n",
		"src/lib/Card.svelte": "<p>card</p>\n",
	})
	installSvelteKitTools(t, root, fakeSvelteKitScript, fakeSvelteCheckScript)

	write(t, root, "src/lib/Card.svelte", "<p>a card</p>\n")
	gitDo(t, root, "add", "src/lib/Card.svelte")
	if res := Precommit(root, RunSuite(precommitTestTimeout)); res.Blocked {
		t.Fatalf("a clean component change was refused over Old.svelte's HEAD error:\n%s", res.Message)
	}

	write(t, root, "src/lib/Card.svelte", "<script lang=\"ts\">const probe: number = \"y\"</script>\n")
	gitDo(t, root, "add", "src/lib/Card.svelte")
	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "src/lib/Card.svelte:1:5") {
		t.Fatalf("want the new error in Card.svelte refused, got %+v", res)
	}
	if strings.Contains(res.Message, "Old.svelte:") {
		t.Fatalf("Old.svelte's HEAD error was held against the commit:\n%s", res.Message)
	}
}

// A script is run step by step without a shell, so only a script that means
// the same thing without one is read; a quoted word loses its quotes as it
// would in sh.
func TestScriptArgvs_ReadsOnlyWhatRunsTheSameWithoutAShell(t *testing.T) {
	cases := []struct {
		script  string
		want    string
		wantErr bool
	}{
		{script: `svelte-kit sync && svelte-check --tsconfig "./tsconfig.json"`, want: `[[svelte-kit sync] [svelte-check --tsconfig ./tsconfig.json]]`},
		{script: `tsc -p 'tsconfig.app.json' --noEmit`, want: `[[tsc -p tsconfig.app.json --noEmit]]`},
		{script: `tsc --noEmit &&`, wantErr: true},
		{script: `tsc --noEmit; eslint .`, wantErr: true},
		{script: `tsc --noEmit & eslint .`, wantErr: true},
		{script: `tsc --project=$TSCONFIG`, wantErr: true},
		{script: `tsc --project="a b"`, wantErr: true},
		{script: `tsc '`, wantErr: true},
	}
	for _, tc := range cases {
		got, err := scriptArgvs(tc.script)
		if tc.wantErr {
			if err == nil {
				t.Errorf("scriptArgvs(%q) = %v, want an error", tc.script, got)
			}
			continue
		}
		if err != nil || fmt.Sprint(got) != tc.want {
			t.Errorf("scriptArgvs(%q) = %v, %v; want %s", tc.script, got, err, tc.want)
		}
	}
}

// A declaration is one argv array naming a program; anything else is a
// shape the gate refuses rather than guesses at.
func TestDeclaredArgv_RefusesAnArgvWithNoProgram(t *testing.T) {
	root := t.TempDir()
	for _, value := range []string{`[]`, `[""]`, `"tsc"`, `[["tsc"]]`} {
		write(t, root, "aphrollo.toml", "[aphrollo.lint]\n\".\" = "+value+"\n")
		if argv, declared, err := declaredArgv(root, root, lintTable); !declared || err == nil {
			t.Errorf("%s: declared=%v err=%v argv=%q, want a declaration refused", value, declared, err, argv)
		}
	}
	write(t, root, "aphrollo.toml", "[aphrollo.lint]\n\".\" = [\"eslint\", \"src\",]\n")
	if argv, declared, err := declaredArgv(root, root, lintTable); !declared || err != nil || fmt.Sprint(argv) != "[eslint src]" {
		t.Errorf("declared=%v err=%v argv=%q, want [eslint src]", declared, err, argv)
	}
}

// A bin is found in the package that declares it by name: the package named
// for it, or any other whose "bin" map names it. A package whose single
// "bin" string is its own command does not answer for another name.
func TestNpmToolEntry_FindsThePackageThatDeclaresTheBin(t *testing.T) {
	root := t.TempDir()
	nm := filepath.Join(root, "node_modules")
	write(t, nm, "aaa/package.json", `{"name": "aaa", "bin": "cli.js"}`)
	write(t, nm, "aaa/cli.js", "")
	write(t, nm, "typescript/package.json", `{"bin": {"tsc": "./bin/tsc"}}`)
	write(t, nm, "typescript/bin/tsc", "")
	write(t, nm, "@vue/typecheck/package.json", `{"bin": {"vue-tsc": "./bin/vue-tsc.js"}}`)
	write(t, nm, "@vue/typecheck/bin/vue-tsc.js", "")
	cases := []struct{ bin, want string }{
		{"aaa", filepath.Join(nm, "aaa", "cli.js")},
		{"tsc", filepath.Join(nm, "typescript", "bin", "tsc")},
		{"vue-tsc", filepath.Join(nm, "@vue", "typecheck", "bin", "vue-tsc.js")},
		{"eslint", ""},
	}
	for _, tc := range cases {
		if got := npmToolEntry(nm, tc.bin); got != tc.want {
			t.Errorf("npmToolEntry(%s) = %q, want %q", tc.bin, got, tc.want)
		}
	}
}

// A command that chose its own output keeps it; the gate appends the output
// it reads only where the command left the choice open.
func TestNpmTypecheck_AnOutputTheCommandChoseIsKept(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json":  `{"name": "web", "scripts": {"typecheck": "svelte-check --output=human-verbose"}}`,
		"tsconfig.json": plainTsconfig,
	})
	installSvelteKitTools(t, root, "", "")
	write(t, root, "src/a.ts", "export const a = 1\n")
	gitDo(t, root, "add", "src/a.ts")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := fakeNode + " " + entry(root, "svelte-check", "svelte-check") + " --output=human-verbose"
	if got := runLines(seen); len(got) != 1 || got[0] != want {
		t.Fatalf("typecheck ran %q, want [%s]", got, want)
	}
}

// A step whose tool is not installed ends the typecheck with a NOT RUN
// line: the steps after it read what it would have written. svelte-check
// is a runtime dependency here, which counts the same as a dev one.
func TestNpmTypecheck_AMissingSetupToolStopsTheStepsAfterIt(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json":  `{"name": "web", "dependencies": {"@sveltejs/kit": "2.20.0", "svelte-check": "4.1.0"}}`,
		"tsconfig.json": plainTsconfig,
	})
	installFakePackage(t, root, "svelte-check", "svelte-check", "")
	write(t, root, "src/a.ts", "export const a = 1\n")
	gitDo(t, root, "add", "src/a.ts")

	var seen []Runner
	var res GateResult
	stderr := captureStderr(t, func() { res = Precommit(root, runsAt(&seen, root)) })
	if res.Blocked || len(seen) != 0 {
		t.Fatalf("want no block and no run, got %+v and %v", res, runLines(seen))
	}
	if !strings.Contains(stderr, "svelte-kit in "+root+" → NOT RUN — no package in node_modules declares the bin svelte-kit") {
		t.Fatalf("no NOT RUN line naming the missing bin:\n%s", stderr)
	}
}

// A svelte-check dependency without a tsconfig.json is nothing to check.
func TestNpmTypecheck_SvelteCheckWithoutATsconfigChecksNothing(t *testing.T) {
	withFakeNode(t)
	root := makeTSRepo(t, map[string]string{"package.json": svelteKitManifest})
	installSvelteKitTools(t, root, "", "")
	write(t, root, "src/a.js", "export const a = 1\n")
	gitDo(t, root, "add", "src/a.js")

	var seen []Runner
	if res := Precommit(root, runsAt(&seen, root)); res.Blocked || len(seen) != 0 {
		t.Fatalf("want no block and no run, got %+v and %v", res, runLines(seen))
	}
}

// fakeCheckerScript is a checker the gate cannot read: it prints one line
// per file under src/ holding BAD, and fails when it printed any.
const fakeCheckerScript = `const fs = require("fs");
const bad = fs.readdirSync("src").sort().filter(f => fs.readFileSync("src/" + f, "utf8").includes("BAD"));
for (const f of bad) console.log("problem in src/" + f);
process.exit(bad.length > 0 ? 1 : 0);
`

// A declared tool the gate cannot read is judged by its output lines over
// HEAD's run of the same command: a line HEAD already printed passes, a new
// one refuses.
func TestNpmTypecheck_AnUnreadableToolIsJudgedByTheLinesItAdds(t *testing.T) {
	requireNode(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeTSRepo(t, map[string]string{
		"aphrollo.toml": "[aphrollo.typecheck]\n\".\" = [\"checker\"]\n",
		"package.json":  `{"name": "web"}`,
		"src/old.ts":    "BAD\n",
		"src/new.ts":    "ok\n",
	})
	installFakePackage(t, root, "checker", "checker", fakeCheckerScript)

	write(t, root, "src/new.ts", "fine\n")
	gitDo(t, root, "add", "src/new.ts")
	if res := Precommit(root, RunSuite(precommitTestTimeout)); res.Blocked {
		t.Fatalf("a line HEAD already printed refused the commit:\n%s", res.Message)
	}
	write(t, root, "src/new.ts", "BAD\n")
	gitDo(t, root, "add", "src/new.ts")
	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked || !strings.Contains(res.Message, "problem in src/new.ts") || strings.Contains(res.Message, "problem in src/old.ts") {
		t.Fatalf("want only the new line refused, got %+v", res)
	}
}

// `aphrollo check` runs the same typecheck and lint over the whole root,
// without the output flag the gate adds for itself, and says why a step
// cannot run instead of running something else.
func TestNpmVerifySteps_TheCommitGatesDataOverTheWholeRoot(t *testing.T) {
	withFakeNode(t)
	root := t.TempDir()
	write(t, root, "package.json", `{"name": "web", "devDependencies": {"svelte-check": "4.1.0"}}`)
	write(t, root, "tsconfig.json", plainTsconfig)
	write(t, root, "eslint.config.js", "export default []\n")
	installFakePackage(t, root, "svelte-check", "svelte-check", "")
	installFakePackage(t, root, "eslint", "eslint", "")
	steps, err := NpmVerifySteps(root, root)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprint([]NpmVerifyStep{
		{Name: "typecheck", Argv: []string{fakeNode, entry(root, "svelte-check", "svelte-check"), "--tsconfig", "./tsconfig.json"}},
		{Name: "lint", Argv: []string{fakeNode, entry(root, "eslint", "eslint"), "."}},
	})
	if fmt.Sprint(steps) != want {
		t.Fatalf("steps = %v, want %v", steps, want)
	}

	skips := []struct{ name, manifest, want string }{
		{"a missing tool", `{"name": "web", "scripts": {"typecheck": "vue-tsc --noEmit"}}`, "declares the bin vue-tsc"},
		{"a script needing a shell", `{"name": "web", "scripts": {"typecheck": "tsc > out"}}`, "needs a shell"},
		{"nothing declared", `{"name": "web"}`, "nothing declared or detected"},
	}
	for _, tc := range skips {
		dir := t.TempDir()
		write(t, dir, "package.json", tc.manifest)
		steps, err := NpmVerifySteps(dir, dir)
		if err != nil || len(steps) != 2 || steps[0].Argv != nil || !strings.Contains(steps[0].Skip, tc.want) {
			t.Errorf("%s: steps = %+v, %v; want a typecheck skip saying %q, then the lint", tc.name, steps, err, tc.want)
		}
	}

	write(t, root, "aphrollo.toml", "[aphrollo.lint]\n\".\" = \"eslint\"\n")
	if _, err := NpmVerifySteps(root, root); err == nil || !strings.Contains(err.Error(), "[aphrollo.lint]") {
		t.Fatalf("err = %v, want the unreadable declaration named", err)
	}
}
