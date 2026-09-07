package cli

import (
	"os"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// discardWallRefusal is the discard wall's shim-side half: discardIntent and
// discardCostOf (git_shim_discard.go) classify and measure, this decides
// what to do about it. line is non-empty only when refuse is true — the
// caller prints it and stops before git runs; refuse=false covers both "this
// invocation destroys nothing" and "an override consumed itself", and either
// way the caller just runs git normally.
func discardWallRefusal(cfg gitShimConfig, rest []string, workDir string) (line string, refuse bool) {
	form, paths, ok := discardIntent(rest)
	if !ok {
		return "", false
	}
	cost := discardCostOf(cfg.realGit, workDir, form, paths)
	if cost.zero() {
		return "", false
	}
	session := tdd.SessionID()
	if os.Getenv("APHROLLO_DISCARD") == "1" {
		tdd.LogOverride("override-discard-env", session, workDir)
		return "", false
	}
	if tdd.ConsumeOneShot(tdd.WallDiscard) {
		tdd.LogOverride("override-discard-used", session, workDir)
		return "", false
	}
	formKey := strings.ReplaceAll(form, " ", "-")
	if cost.Err != nil {
		formKey += "-unmeasured"
	}
	tdd.AppendGateLog("git", workDir, "git", "git-discard-refused:"+formKey, 0)
	return discardRefusalLine(form, cost), true
}
