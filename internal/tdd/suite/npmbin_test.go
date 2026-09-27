package suite

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stubLookNode names the node binary for a test, or reports none.
func stubLookNode(t *testing.T, path string, err error) {
	t.Helper()
	t.Cleanup(SetLookNodeForTest(func() (string, error) { return path, err }))
}

// installFakeNpmTool lays pkg out in root's node_modules the way npm does:
// a package.json whose "bin" names the tool, and the script it names.
func installFakeNpmTool(t *testing.T, root, pkg, script string) string {
	t.Helper()
	write(t, root, "node_modules/"+pkg+"/package.json", `{"name": "`+pkg+`", "bin": {"`+pkg+`": "./bin/`+script+`"}}`)
	write(t, root, "node_modules/"+pkg+"/bin/"+script, "")
	return filepath.Join(root, "node_modules", pkg, "bin", script)
}

// Issue #929: the edit and merge runners run the root's installed vitest or
// jest as `node <its bin entry>` with the same arguments, never through npx,
// which may reach the registry and on Windows goes through a .cmd shim.
func TestNodeToolRunner_RunsTheInstalledToolUnderNode(t *testing.T) {
	root := t.TempDir()
	stubLookNode(t, "/opt/node/bin/node", nil)
	cases := []struct {
		runner Runner
		script string
	}{
		{Runner{Cmd: "npx", Args: []string{"vitest", "related", "src/a.ts", "--run"}, Dir: "/w"}, "vitest.mjs"},
		{Runner{Cmd: "npx", Args: []string{"jest", "--findRelatedTests", "src/a.js"}, Dir: "/w"}, "jest.js"},
	}
	for _, tc := range cases {
		entry := installFakeNpmTool(t, root, tc.runner.Args[0], tc.script)

		got, missing := nodeToolRunner(root, tc.runner)

		if missing != "" {
			t.Fatalf("%v: an installed tool was not runnable: %s", tc.runner.Args, missing)
		}
		want := Runner{Cmd: "/opt/node/bin/node", Args: append([]string{entry}, tc.runner.Args[1:]...), Dir: "/w"}
		if got.Cmd != want.Cmd || !slices.Equal(got.Args, want.Args) || got.Dir != want.Dir {
			t.Errorf("%v: got %+v, want %+v", tc.runner.Args, got, want)
		}
	}
}

// Only an npx vitest or jest runner is rewritten. A root whose runner is the
// generic `npm test --silent` script keeps that invocation, and so does
// every other runner — a runner already rewritten to node included, which
// the merge gate hands back in to ask whether anything is missing.
func TestNodeToolRunner_LeavesEveryOtherRunnerAsItIs(t *testing.T) {
	root := t.TempDir()
	stubLookNode(t, "/opt/node/bin/node", nil)
	entry := installFakeNpmTool(t, root, "vitest", "vitest.mjs")
	for _, r := range []Runner{
		{Cmd: "npm", Args: []string{"test", "--silent"}},
		{Cmd: "go", Args: []string{"test", "./..."}},
		{Cmd: "npx", Args: []string{"tsc", "--noEmit"}},
		{Cmd: "npx"},
		{Cmd: "/opt/node/bin/node", Args: []string{entry, "related", "src/a.ts", "--run"}},
	} {
		got, missing := nodeToolRunner(root, r)
		if missing != "" || got.Cmd != r.Cmd || !slices.Equal(got.Args, r.Args) {
			t.Errorf("nodeToolRunner(%+v) = %+v, %q; want it unchanged", r, got, missing)
		}
	}
}

// A root without the tool installed, or a box without node, says which, and
// hands back the runner it could not rewrite: the caller decides how loud
// the missing run is.
func TestNodeToolRunner_NamesWhatIsMissing(t *testing.T) {
	root := t.TempDir()
	r := Runner{Cmd: "npx", Args: []string{"vitest", "related", "src/a.ts", "--run"}}
	stubLookNode(t, "/opt/node/bin/node", nil)
	if got, missing := nodeToolRunner(root, r); !strings.Contains(missing, "vitest is not installed") || got.Cmd != "npx" {
		t.Errorf("no vitest in node_modules: got %+v, %q", got, missing)
	}
	installFakeNpmTool(t, root, "vitest", "vitest.mjs")
	stubLookNode(t, "", errors.New("not found"))
	if got, missing := nodeToolRunner(root, r); !strings.Contains(missing, "node is not on PATH") || got.Cmd != "npx" {
		t.Errorf("no node: got %+v, %q", got, missing)
	}
}

// A vitest run under node that executed zero tests is as vacuous as the same
// run through npx: the rewrite must not blind the judge that refuses it.
func TestVacuousNames_JudgesVitestUnderNodeLikeVitestThroughNpx(t *testing.T) {
	zero := SuiteResult{Passed: true, Output: " Test Files  1 passed (1)\n      Tests  no tests (0)\n"}
	cases := []struct {
		runner Runner
		want   []string
	}{
		{Runner{Cmd: "npx", Args: []string{"vitest", "related", "src/a.ts", "--run"}}, []string{"vitest"}},
		{Runner{Cmd: "/opt/node/bin/node", Args: []string{"/w/node_modules/vitest/vitest.mjs", "related", "src/a.ts", "--run"}}, []string{"vitest"}},
		{Runner{Cmd: `C:\node\node.exe`, Args: []string{`C:\w\node_modules\vitest\vitest.mjs`, "run"}}, []string{"vitest"}},
		{Runner{Cmd: "/opt/node/bin/node", Args: []string{"/w/node_modules/jest/bin/jest.js"}}, nil},
		{Runner{Cmd: "/opt/node/bin/node"}, nil},
	}
	for _, tc := range cases {
		got, err := vacuousNames(tc.runner, zero)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("vacuousNames(%+v) = %v, %v; want %v", tc.runner, got, err, tc.want)
		}
	}
}
