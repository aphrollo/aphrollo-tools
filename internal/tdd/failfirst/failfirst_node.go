package failfirst

import (
	"fmt"
	"os"
	"path/filepath"
)

// Issue #904: on an npm root the proof never measured anything. Its
// worktree at HEAD carried no gitignored node_modules, and it ran
// `npx vitest …`, which found no tool there, may try the registry, and on
// Windows goes through a .cmd shim and cmd.exe. So the proof worktree of a
// vitest or jest root is placed under the root's own node_modules, where
// Node's module resolution walks up to the root's installed packages (the
// same placement precommit_npmbaseline.go uses for tsc and eslint), and the
// tool runs as `node <its package's bin entry>`. A root without the tool
// installed, or a box without node, is inconclusive and says which; there
// is no fallback to npx.

// npmTestTool is the npm package an npx runner runs (vitest, jest), and ""
// for any other runner.
func npmTestTool(r Runner) string {
	if r.Cmd != "npx" || len(r.Args) == 0 {
		return ""
	}
	return r.Args[0]
}

// nodeTestRunner is r, an npx runner, rewritten to run root's installed copy
// of its tool under node with the same arguments, or why that cannot run.
// look finds node on PATH.
func nodeTestRunner(root string, r Runner, look func(string) (string, error)) (Runner, string) {
	tool := npmTestTool(r)
	entry := npmBinEntry(filepath.Join(root, "node_modules", tool), tool)
	if entry == "" {
		return Runner{}, fmt.Sprintf("%s is not installed in %s; run `npm ci` there so fail-first can run it", tool, filepath.Join(root, "node_modules"))
	}
	node, err := look("node")
	if err != nil {
		return Runner{}, "node is not on PATH; install Node.js so fail-first can run " + tool
	}
	return Runner{Cmd: node, Args: append([]string{entry}, r.Args[1:]...), Dir: r.Dir}, ""
}

// npmProofWorktree is a fresh directory for the proof worktree under root's
// node_modules when root runs its tests with an npm tool, and "" otherwise
// or when node_modules cannot take one; the caller then uses its default.
func npmProofWorktree(root string) string {
	r, _ := DetectRunner(root)
	if npmTestTool(r) == "" {
		return ""
	}
	// A root without node_modules has nothing installed; the run is then
	// refused by nodeTestRunner, whichever directory it was placed in.
	wt, err := os.MkdirTemp(filepath.Join(root, "node_modules"), ".aphrollo-head-")
	if err != nil {
		return ""
	}
	return wt
}
