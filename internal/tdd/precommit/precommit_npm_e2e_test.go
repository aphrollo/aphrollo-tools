package precommit

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// installRealTsc makes the TypeScript compiler on PATH the root's local one,
// the way `npm ci` would have: a symlink to the real script, or on Windows
// the .cmd shim npm writes there.
func installRealTsc(t *testing.T, root, tsc string) {
	t.Helper()
	bin := localBin(root, "tsc")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if err := os.WriteFile(bin, []byte("@\""+tsc+"\" %*\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return
	}
	real, err := filepath.EvalSymlinks(tsc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, bin); err != nil {
		t.Fatal(err)
	}
}

// Issue #890 against the real compiler: in a Vite + strict-TS root, whose
// root tsconfig.json checks nothing itself, appending
// `export const probe: number = "not a number"` to a .ts file must be
// refused with tsc's own TS2322, where the same root without the probe
// commits. The clean half is what shows the refusal is about the probe.
func TestNpmChecksE2E_RealTscRefusesTheIssue890TypeError(t *testing.T) {
	tsc, err := exec.LookPath("tsc")
	if err != nil {
		t.Skip("tsc not on PATH; skipping e2e") // skip-ok: the real compiler is not installed on this box
	}
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
	})
	installRealTsc(t, root, tsc)

	write(t, root, "src/widget.ts", "export const widget: number = 1\n")
	gitDo(t, root, "add", "src/widget.ts")
	if res := Precommit(root, RunSuite(precommitTestTimeout)); res.Blocked {
		t.Fatalf("the clean root was refused: %s", res.Message)
	}

	write(t, root, "src/widget.ts", "export const widget: number = 1\nexport const probe: number = \"not a number\"\n")
	gitDo(t, root, "add", "src/widget.ts")
	res := Precommit(root, RunSuite(precommitTestTimeout))
	if !res.Blocked {
		t.Fatalf("the TS2322 probe committed cleanly: %+v", res)
	}
	if !strings.Contains(res.Message, "TS2322") {
		t.Fatalf("the refusal does not carry tsc's TS2322:\n%s", res.Message)
	}
}
