package failfirst

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// installFakeTool lays pkg out in root's node_modules the way npm does: a
// package.json whose "bin" names the tool, and the script it names.
func installFakeTool(t *testing.T, root, pkg, script string) string {
	t.Helper()
	dir := filepath.Join(root, "node_modules", pkg)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "` + pkg + `", "bin": {"` + pkg + `": "./bin/` + script + `"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "bin", script)
	if err := os.WriteFile(entry, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry
}

func nodeAt(path string) func(string) (string, error) {
	return func(name string) (string, error) {
		if name != "node" {
			return "", errors.New("looked up " + name)
		}
		return path, nil
	}
}

// Issue #904: the proof runs the root's own installed vitest or jest as
// `node <the package's bin entry>`, with the narrowed arguments after it —
// never npx, which may fetch from the registry, and never a .cmd shim,
// which on Windows goes through cmd.exe.
func TestNodeTestRunner_RunsTheInstalledPackagesBinEntryUnderNode(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name   string
		runner Runner
		script string
	}{
		{"vitest", Runner{Cmd: "npx", Args: []string{"vitest", "related", "src/a.test.ts", "--run"}}, "vitest.mjs"},
		{"jest", Runner{Cmd: "npx", Args: []string{"jest", "--findRelatedTests", "src/a.test.ts"}}, "jest.js"},
	}
	for _, tc := range cases {
		entry := installFakeTool(t, root, tc.name, tc.script)
		got, why := nodeTestRunner(root, tc.runner, nodeAt("/opt/node/bin/node"))
		if why != "" {
			t.Fatalf("%s: an installed tool was not runnable: %s", tc.name, why)
		}
		want := append([]string{entry}, tc.runner.Args[1:]...)
		if got.Cmd != "/opt/node/bin/node" || !slices.Equal(got.Args, want) {
			t.Errorf("%s: got %s %v, want /opt/node/bin/node %v", tc.name, got.Cmd, got.Args, want)
		}
	}
}

// A root without the tool installed, or a box without node, stays
// inconclusive and says which: there is no fallback to npx.
func TestNodeTestRunner_NamesWhatIsMissingInsteadOfFallingBack(t *testing.T) {
	root := t.TempDir()
	r := Runner{Cmd: "npx", Args: []string{"vitest", "related", "src/a.test.ts", "--run"}}
	if got, why := nodeTestRunner(root, r, nodeAt("/opt/node/bin/node")); !strings.Contains(why, "vitest is not installed") || got.Cmd != "" {
		t.Errorf("no vitest in node_modules: got %+v, %q", got, why)
	}
	installFakeTool(t, root, "vitest", "vitest.mjs")
	noNode := func(string) (string, error) { return "", errors.New("not found") }
	if got, why := nodeTestRunner(root, r, noNode); !strings.Contains(why, "node is not on PATH") || got.Cmd != "" {
		t.Errorf("no node: got %+v, %q", got, why)
	}
}

// Only a runner that goes through npx names an npm test tool.
func TestNpmTestTool_OnlyAnNpxRunnerNamesOne(t *testing.T) {
	cases := []struct {
		r    Runner
		want string
	}{
		{Runner{Cmd: "npx", Args: []string{"vitest", "run"}}, "vitest"},
		{Runner{Cmd: "npx", Args: []string{"jest"}}, "jest"},
		{Runner{Cmd: "npm", Args: []string{"test", "--silent"}}, ""},
		{Runner{Cmd: "go", Args: []string{"test", "./..."}}, ""},
		{Runner{Cmd: "npx"}, ""},
	}
	for _, tc := range cases {
		if got := npmTestTool(tc.r); got != tc.want {
			t.Errorf("npmTestTool(%s %v) = %q, want %q", tc.r.Cmd, tc.r.Args, got, tc.want)
		}
	}
}
