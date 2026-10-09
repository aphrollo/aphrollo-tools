package merge

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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
		nm := filepath.Join(root, depinstall.NodeModules)
		if depinstall.Reusable(rule, laneRoot, root) {
			// One dependency source per merge: the lane's install serves only
			// when every package of it stays inside the lane. A package that
			// resolves into another lane would load beside the rest of the
			// install as a second copy of a module.
			if esc := depinstall.Escapes(filepath.Join(laneRoot, depinstall.NodeModules), laneWorktree); len(esc) == 0 {
				if err := links.Make(filepath.Join(laneRoot, depinstall.NodeModules), nm); err == nil {
					fmt.Fprintf(log, "gate %s: %s — %s unchanged, linked the lane's node_modules\n", premergeDisplayName, rel, rule.Marker)
					continue
				}
			} else {
				fmt.Fprintf(log, "gate %s: %s — the lane's node_modules is not self-contained (%s), so it is not linked\n",
					premergeDisplayName, rel, describeEscapes(esc))
			}
		}
		cacheRoot := prGateDepCacheRoot(laneWorktree)
		key, keyed := depinstall.CacheKey(rule, root)
		keyed = keyed && cacheRoot != ""
		if keyed {
			if dir, ok := depinstall.Cached(cacheRoot, key); ok && links.Make(dir, nm) == nil {
				fmt.Fprintf(log, "gate %s: %s — linked the install already made for this %s\n", premergeDisplayName, rel, rule.Marker)
				continue
			}
		}
		fmt.Fprintf(log, "gate %s: %s — installing with %s\n", premergeDisplayName, rel, strings.Join(rule.Argv, " "))
		res := run(Runner{Cmd: rule.Argv[0], Args: rule.Argv[1:], Dir: root}, root)
		if res.Passed {
			if keyed {
				keepInstall(cacheRoot, key, nm, links, log, rel)
			}
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

// prGateDepCacheRoot is where installs made for a merge are kept, keyed by
// their lockfile: beside the throwaway checkouts, on the repo's own disk. ""
// when no such place exists.
func prGateDepCacheRoot(laneWorktree string) string {
	parent := prGateCheckoutParent(laneWorktree)
	if parent == "" {
		return ""
	}
	return filepath.Join(parent, prGateDepCacheName)
}

// prGateDepCacheName starts with a dot and not with gate-prmerge-, so no sweep
// takes it for a checkout.
const prGateDepCacheName = ".depcache"

// prGateDepCacheMaxAge is how long an install unused by any merge is kept.
const prGateDepCacheMaxAge = 14 * 24 * time.Hour

// keepInstall moves the install just made in the checkout into the cache and
// links it back, when it is self-contained; an install that is not stays where
// it is and is used as made. Entries unused past the age are swept.
func keepInstall(cacheRoot, key, nm string, links *depinstall.Links, log io.Writer, rel string) {
	dir, ok := depinstall.Store(cacheRoot, key, nm)
	if !ok {
		return
	}
	if err := links.Make(dir, nm); err != nil {
		_ = os.Rename(dir, nm) // put it back: the suite needs it where it was
		fmt.Fprintf(log, "gate %s: %s — the install could not be linked from the cache (%v), kept in the checkout\n", premergeDisplayName, rel, err)
		return
	}
	depinstall.Sweep(cacheRoot, prGateDepCacheMaxAge, time.Now())
}

// describeEscapes names the packages that resolve outside the lane and where.
func describeEscapes(esc []depinstall.Escape) string {
	parts := make([]string, 0, len(esc))
	for i, e := range esc {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("and %d more", len(esc)-3))
			break
		}
		parts = append(parts, e.Name+" -> "+e.Real)
	}
	return strings.Join(parts, "; ")
}
