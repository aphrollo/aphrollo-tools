package install

import (
	"fmt"

	"github.com/aphrollo/aphrollo-tools/internal/undercover"
)

// doctorIdentity is the commit-msg gate's identity check, asked of the box
// rather than of one commit: a global or repo-level git identity set to a
// tool's own name or address signs every commit made there, and the gate
// then refuses each of them one at a time. ok=false means the check does not
// apply: no repo, or one that never set `undercover = true`.
func doctorIdentity(in DoctorInput) (DoctorCheck, bool) {
	c := DoctorCheck{Name: "git identity"}
	if in.Repo == "" {
		return c, false
	}
	tells, on := undercover.Load(in.Repo)
	if !on {
		return c, false
	}
	if id, tell, hit := tells.ConfiguredIdentityTell(in.Repo); hit {
		c.Detail = fmt.Sprintf("the %s identity %s carries %q, and this repo keeps its history undercover — set your own: %s",
			id.Scope, id, tell, identityFix(id.Scope))
		return c, true
	}
	c.OK = true
	return c, true
}

// identityFix is the command that replaces the identity at its own scope.
func identityFix(scope string) string {
	flag := ""
	if scope == "global" {
		flag = "--global "
	}
	return fmt.Sprintf("git config %suser.name \"<name>\" && git config %suser.email \"<address>\"", flag, flag)
}
