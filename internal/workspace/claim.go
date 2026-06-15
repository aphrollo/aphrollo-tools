package workspace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Claiming a prepared worktree onto the dev tier — repointing the shared
// .devclaim/<repo> symlink the dev units follow and restarting the systemd
// unit — is privileged. That privileged operation already lives behind a
// tightly argv-validated sudoers fence: `aphrollo-dev claim <svc> <worktree>`
// (the sudoers grant is `/usr/local/bin/aphrollo-dev *`, and the script
// whitelists svc to {api,rlndx} and confines the worktree under $SPACES).
//
// `aphrollo workspace claim` does NOT reimplement any of that — it would mean
// granting this general binary sudo, a far wider surface than the audited
// script. It is a thin wrapper that resolves the worktree from <repo> <branch>
// (symmetric with prepare, so you never paste a .worktrees/… path), derives the
// dev service, and shells out to the existing fence. Dry-run by default; the
// privileged call only happens on --apply.

// devBin is the privileged dev-tier control script. Overridable for tests.
func devBin() string {
	if b := os.Getenv("APHROLLO_DEV_BIN"); b != "" {
		return b
	}
	return "/usr/local/bin/aphrollo-dev"
}

// useSudo reports whether to prefix sudo. Root doesn't need it; tests can force
// it off via APHROLLO_DEV_SUDO=0 to exercise Run against a fake dev script.
func useSudo() bool {
	switch strings.ToLower(os.Getenv("APHROLLO_DEV_SUDO")) {
	case "0", "false", "no":
		return false
	}
	return os.Geteuid() != 0
}

// deriveService maps a repo to its dev unit, mirroring aphrollo-dev's claim
// mapping (rlndx serves the web repo, api serves the api repo). Returns "" when
// the repo has no dev unit, so the caller can ask for an explicit --svc.
func deriveService(repoName string) string {
	switch {
	case strings.Contains(repoName, "web"):
		return "rlndx"
	case strings.Contains(repoName, "api"):
		return "api"
	default:
		return ""
	}
}

var claimServices = map[string]bool{"rlndx": true, "api": true}

// Claim is a resolved, not-yet-executed dev-tier claim.
type Claim struct {
	Service  string // dev unit: rlndx | api
	Worktree string
	Display  string // human-readable command (what --apply will run)
	argv     []string
}

// ClaimPlan resolves the worktree for repo+branch and the dev service, and
// builds the privileged claim command — without executing it. svc may be ""
// to derive it from the repo name. It errors early when the worktree does not
// exist, pointing at `prepare`, since claim cannot put a missing tree on the
// dev tier.
func ClaimPlan(repo, branch, svc, into string) (*Claim, error) {
	if repo == "" {
		return nil, fmt.Errorf("repo path required")
	}
	slug, err := Slugify(branch)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return nil, err
	}
	top, err := gitToplevel(abs)
	if err != nil {
		return nil, err
	}

	if svc == "" {
		svc = deriveService(filepath.Base(top))
		if svc == "" {
			return nil, fmt.Errorf("cannot derive a dev service from repo %q; pass --svc rlndx|api", filepath.Base(top))
		}
	}
	if !claimServices[svc] {
		return nil, fmt.Errorf("service not allowed: %s (want rlndx|api)", svc)
	}

	base := into
	if base == "" {
		base = DefaultWorktreeBase(top)
	}
	wt := filepath.Join(base, slug)
	if !dirExists(wt) {
		return nil, fmt.Errorf("worktree not found: %s\n  run: aphrollo workspace prepare %s %s --apply", wt, repo, branch)
	}

	argv := []string{devBin(), "claim", svc, wt}
	if useSudo() {
		argv = append([]string{"sudo"}, argv...)
	}
	return &Claim{Service: svc, Worktree: wt, Display: shellJoin(argv), argv: argv}, nil
}

// Run executes the claim, streaming output. This repoints the dev-tier symlink
// and restarts the dev unit, so it should only be called on --apply.
func (c *Claim) Run(stdout, stderr io.Writer) error {
	cmd := exec.Command(c.argv[0], c.argv[1:]...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
