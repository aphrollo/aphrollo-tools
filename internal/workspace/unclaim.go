package workspace

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/dev"
)

// Unclaim is the inverse of Claim: it repoints the dev-tier .devclaim/<key>
// symlink back at the repo's MAIN clone and restarts the dev unit, so the dev
// tier stops serving a worktree and returns to the canonical checkout. Like
// claim, the only privileged atom is the systemd restart (via dev.Restart's
// exact-match sudoers grant); the symlink lives in the group-writable .devclaim
// dir, so repointing it is unprivileged.
type Unclaim struct {
	Service  string // dev unit: rlndx | api
	RepoKey  string // .devclaim entry: web | api
	MainRepo string // canonical clone the symlink is restored to
	Symlink  string // .devclaim/<key>
	skip     string // non-empty => symlink already points at MainRepo
}

// UnclaimPlan resolves the dev service and the .devclaim symlink for a target,
// pointing it back at the target's MAIN clone. svc may be "" to derive it from
// the repo name (mirrors ClaimPlan).
func UnclaimPlan(t *Target, svc string) (*Unclaim, error) {
	if svc == "" {
		svc = deriveService(t.RepoName)
		if svc == "" {
			return nil, fmt.Errorf("cannot derive a dev service from repo %q; pass --svc rlndx|api", t.RepoName)
		}
	}
	if !claimServices[svc] {
		return nil, fmt.Errorf("service not allowed: %s (want rlndx|api)", svc)
	}
	key := repoKeyForSvc(svc)
	symlink := filepath.Join(devclaimDir(t.MainRepo), key)
	u := &Unclaim{Service: svc, RepoKey: key, MainRepo: t.MainRepo, Symlink: symlink}
	u.skip = symlinkAlready(symlink, t.MainRepo)
	return u, nil
}

// Render previews the unclaim.
func (u *Unclaim) Render(apply bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace unclaim: dev-%s -> %s\n", u.Service, u.MainRepo)
	if apply {
		return b.String()
	}
	fmt.Fprintf(&b, "\nsteps (dry-run — run without --dry to execute the [run] steps):\n")
	tag, note := "run", ""
	if u.skip != "" {
		tag, note = "skip", "  — "+u.skip
	}
	fmt.Fprintf(&b, "  1. [%s] repoint %s -> %s%s\n", tag, u.Symlink, u.MainRepo, note)
	fmt.Fprintf(&b, "  2. [run]  restart dev-%s\n", u.Service)
	fmt.Fprintf(&b, "\nrun again without --dry to restore the dev tier to the main clone.\n")
	return b.String()
}

// Apply repoints the symlink (unless already restored) and restarts the dev
// unit. The restart runs even when the symlink was already correct, so unclaim
// reliably drops a stale-tree process.
func (u *Unclaim) Apply(stdout, stderr io.Writer) error {
	if u.skip != "" {
		fmt.Fprintf(stdout, "  [skip] repoint %s — %s\n", u.Symlink, u.skip)
	} else {
		fmt.Fprintf(stdout, "  [run]  repoint %s -> %s\n", u.Symlink, u.MainRepo)
		if err := repointSymlink(u.Symlink, u.MainRepo); err != nil {
			return fmt.Errorf("repoint %s: %w", u.Symlink, err)
		}
	}
	fmt.Fprintf(stdout, "  [run]  restart dev-%s\n", u.Service)
	if err := dev.Restart(u.Service, stdout, stderr); err != nil {
		return fmt.Errorf("restart dev-%s: %w", u.Service, err)
	}
	fmt.Fprintf(stdout, "\nunclaimed: dev-%s now serves %s\n", u.Service, u.MainRepo)
	return nil
}
