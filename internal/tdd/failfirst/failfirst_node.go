package failfirst

import (
	"fmt"
	"path/filepath"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
)

// Issue #904: on an npm root the proof never measured anything. Its
// worktree at HEAD carried no gitignored node_modules, and it ran
// `npx vitest …`, which found no tool there, may try the registry, and on
// Windows goes through a .cmd shim and cmd.exe. So the proof worktree of a
// vitest or jest root gets a node_modules link to the root's own (a symlink
// on Unix, a junction on Windows), and the tool runs as
// `node <its package's bin entry>`. A root without the tool installed, or a
// box without node, is inconclusive and says which; there is no fallback to
// npx.
//
// Issue #932: the worktree itself lives in the gate state dir with every
// other root's, never inside the root's node_modules. That directory may be
// shared with another checkout, and a tree placed inside it is a dependency
// to the tools resolving from there, so a module new in the commit was not
// found. The link is removed as a link before the worktree goes, so no
// removal reaches the packages it points at.

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

// linkRootNodeModules gives execRoot, root's place in the proof worktree, a
// node_modules link to root's installed one, recorded in links, or says why
// it could not.
func linkRootNodeModules(links *depinstall.Links, root, execRoot string) string {
	nm := depinstall.NodeModules
	if err := links.Make(filepath.Join(root, nm), filepath.Join(execRoot, nm)); err != nil {
		return fmt.Sprintf("the proof worktree could not link %s: %v", filepath.Join(root, nm), err)
	}
	return ""
}
