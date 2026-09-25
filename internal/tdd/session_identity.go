package tdd

import (
	"fmt"

	"github.com/aphrollo/aphrollo-tools/internal/undercover"
)

// identityAlarmLine is the session start's first line when the repo keeps its
// history undercover and git's global or repo-level identity carries a tell:
// every commit this session makes would carry it, and the commit gate would
// refuse each one. "" when there is nothing to say.
func identityAlarmLine(cwd string) string {
	root := RepoRoot(cwd)
	if root == "" {
		return ""
	}
	tells, on := undercover.Load(root)
	if !on {
		return ""
	}
	id, tell, hit := tells.ConfiguredIdentityTell(root)
	if !hit {
		return ""
	}
	flag := ""
	if id.Scope == "global" {
		flag = "--global "
	}
	return fmt.Sprintf("gate: UNDERCOVER IDENTITY — the %s git identity %s carries %q; this repo keeps its history undercover, so the commit gate refuses every commit made with it. Fix it before the first commit: git config %suser.name \"<name>\" && git config %suser.email \"<address>\"",
		id.Scope, id, tell, flag, flag)
}
