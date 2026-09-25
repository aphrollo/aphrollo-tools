package precommit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// installRealTypescript installs the TypeScript package that provides the
// tsc on PATH into root's node_modules, as `npm ci` would have: a link to
// the package directory, which the gate runs as node <its bin/tsc>.
func installRealTypescript(t *testing.T, root string) {
	t.Helper()
	tsc, err := exec.LookPath("tsc")
	if err != nil {
		t.Skip("tsc not on PATH; skipping e2e") // skip-ok: the real compiler is not installed on this box
	}
	real, err := filepath.EvalSymlinks(tsc)
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Dir(filepath.Dir(real)) // <typescript>/bin/tsc
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pkg, filepath.Join(root, "node_modules", "typescript")); err != nil {
		t.Skipf("cannot link the TypeScript package here: %v", err) // skip-ok: a Windows box without symlink rights
	}
}

// ratchet: test_removed TestNpmChecksE2E_RealTscRefusesTheIssue890TypeError: widened into the test below, which runs the same Vite layout and TS2322 probe and adds the HEAD-baseline cases

// Issue #890 against the real compiler, in a Vite + strict-TS root whose
// root tsconfig.json checks nothing itself and whose src/a.ts already carries
// an error at HEAD. Touching only b.ts commits; the issue's
// `export const probe: number = "not a number"` in b.ts is refused with
// tsc's TS2322; and a signature change in b.ts that breaks its caller in
// a.ts is refused too, though a.ts was never staged.
func TestNpmChecksE2E_RealTscJudgesOnlyWhatTheCommitAdds(t *testing.T) {
	requireNode(t)
	root := makeTSRepo(t, map[string]string{
		"package.json": `{"name": "fancrm-probe", "private": true, "type": "module"}`,
		"tsconfig.json": `{
  "files": [],
  "references": [
    { "path": "./tsconfig.app.json" },
    { "path": "./tsconfig.node.json" }
  ]
}`,
		"tsconfig.app.json": `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "bundler",
    /* Linting */
    "strict": true,
    "noEmit": true,
  },
  "include": ["src"]
}`,
		"tsconfig.node.json": `{
  "compilerOptions": {"target": "ES2023", "module": "ESNext", "moduleResolution": "bundler", "strict": true, "noEmit": true},
  "include": ["vite.config.ts"]
}`,
		"vite.config.ts": "export default {}\n",
		"src/a.ts":       "import { f } from \"./b\"\nexport const n: number = f()\nexport const old = undefinedName\n",
		"src/b.ts":       "export function f(): number { return 1 }\n",
	})
	installRealTypescript(t, root)
	gate := func(b string) GateResult {
		t.Helper()
		write(t, root, "src/b.ts", b)
		gitDo(t, root, "add", "src/b.ts")
		return Precommit(root, RunSuite(precommitTestTimeout))
	}

	if res := gate("export function f(): number { return 2 }\n"); res.Blocked {
		t.Fatalf("a clean change to b.ts was refused over a.ts's HEAD error:\n%s", res.Message)
	}
	res := gate("export function f(): number { return 2 }\nexport const probe: number = \"not a number\"\n")
	if !res.Blocked || !strings.Contains(res.Message, "src/b.ts(2,14): error TS2322") {
		t.Fatalf("want the TS2322 probe in b.ts refused, got %+v", res)
	}
	if strings.Contains(res.Message, "TS2304") {
		t.Fatalf("a.ts's HEAD error was held against the commit:\n%s", res.Message)
	}
	res = gate("export function f(): string { return \"s\" }\n")
	if !res.Blocked || !strings.Contains(res.Message, "src/a.ts(2,14): error TS2322") {
		t.Fatalf("want the caller broken in a.ts refused, got %+v", res)
	}
}
