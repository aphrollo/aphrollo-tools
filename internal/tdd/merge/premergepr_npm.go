package merge

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
)

// The throwaway checkout prGateMergedCheckout builds is a fresh `git
// worktree add`, and a worktree carries none of the gitignored
// node_modules its lane installed. Node resolves a package by walking up
// from the importing file, finds nothing, and vitest or jest dies at startup
// with ERR_MODULE_NOT_FOUND — so every PR touching an npm root was refused
// on a tree CI had just passed (issue #909).
//
// prGateProvisionNode closes that for exactly the roots Mechanical is about
// to test, with the install rule `workspace create` uses (depinstall). When
// the lane already installed what the merged tree pins — the same lockfile,
// byte for byte — its node_modules is linked in rather than installed twice;
// otherwise the rule's install runs in the checkout, through the gate's own
// SuiteRunner so it is bounded by the same timeout as the suites. A link is
// recorded in links, which the checkout's cleanup removes as a link before
// it deletes the checkout, so a removal can never reach through it into the
// lane's real node_modules.

// prGateProvisionNode provisions every npm root Mechanical groups wt's
// merged staged set into. An install that fails refuses the merge naming the root and
// what the install said.
func prGateProvisionNode(laneWorktree, wt string, run SuiteRunner, links *depinstall.Links, log io.Writer) error {
	for _, root := range mechanicalRoots(wt) {
		rule, _ := depinstall.Detect(root)
		if !rule.IsNode() {
			continue
		}
		// root lies under wt by construction: mechanicalRoots walks wt's
		// own staged files.
		rel, _ := filepath.Rel(wt, root)
		laneRoot := filepath.Join(laneWorktree, rel)
		if depinstall.Reusable(rule, laneRoot, root) {
			target := filepath.Join(laneRoot, depinstall.NodeModules)
			if err := links.Make(target, filepath.Join(root, depinstall.NodeModules)); err == nil {
				fmt.Fprintf(log, "gate %s: %s — %s unchanged, linked the lane's node_modules\n", premergeDisplayName, rel, rule.Marker)
				continue
			}
		}
		fmt.Fprintf(log, "gate %s: %s — installing with %s\n", premergeDisplayName, rel, strings.Join(rule.Argv, " "))
		res := run(Runner{Cmd: rule.Argv[0], Args: rule.Argv[1:], Dir: root}, root)
		if res.Passed {
			continue
		}
		how := "failed"
		if res.TimedOut {
			how = "timed out"
		}
		return prGateRefusal(laneWorktree, "npm-install",
			"npm root %s: `%s` %s in the merge checkout, so its suite never ran and the merge was never judged"+
				" — this is an install failure, not a test failure\n%s",
			rel, strings.Join(rule.Argv, " "), how, strings.TrimSpace(res.Err+"\n"+res.Output))
	}
	return nil
}
