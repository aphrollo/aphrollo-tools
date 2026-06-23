package workspace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/dev"
)

// claim puts a prepared worktree on the dev tier — it becomes the tree the dev
// units serve, so the branch is viewable at rlndx (or driven by dev-api).
//
// Almost none of this needs root. The dev units run as the operator and follow
// the .devclaim/<repo> symlink for their WorkingDirectory; that symlink lives in
// a group-writable (aphrollo-dev) directory, so repointing it is unprivileged
// for a group member. Dependency install and vite-cache clearing touch only
// files the invoking user owns. The single privileged atom is restarting the
// systemd unit, which dev.Restart performs via an exact-match `sudo systemctl
// restart` grant (and that restart clears the rlndx vite optimizer cache for the
// freshly-claimed tree as a side effect).
//
// So claim runs its whole sequence here in Go, calling the in-binary dev control
// plane for the one privileged step. It does NOT carry a wildcard sudo grant of
// its own. Dry-run by default; the privileged restart fires only on Apply.

// deriveService maps a repo to its dev unit, mirroring the dev tier's wiring
// (rlndx serves the web repo, api serves the api repo). Returns "" when the repo
// has no dev unit, so the caller can ask for an explicit --svc.
func deriveService(repoName string) string {
	// Match the repo's basename exactly or by its "-<svc>" suffix, NOT a bare
	// substring: "aphrollo-webhooks" / "api-gateway" merely contain "web"/"api"
	// but are not the dev-tier web/api repos.
	name := filepath.Base(repoName)
	switch {
	case name == "web" || strings.HasSuffix(name, "-web"):
		return "rlndx"
	case name == "api" || strings.HasSuffix(name, "-api"):
		return "api"
	default:
		return ""
	}
}

var claimServices = map[string]bool{"rlndx": true, "api": true}

// repoKeyForSvc maps a dev service to its .devclaim symlink name. The dev units
// resolve their WorkingDirectory through .devclaim/<key> (web for rlndx, api for
// api), so the key — not the repo's basename — is what we repoint.
func repoKeyForSvc(svc string) string {
	if svc == "rlndx" {
		return "web"
	}
	return "api"
}

// devclaimDir is the directory holding the per-repo claim symlinks. It sits
// beside the repo (its parent is the spaces root). Overridable for tests.
func devclaimDir(top string) string {
	if d := os.Getenv("APHROLLO_DEVCLAIM_DIR"); d != "" {
		return d
	}
	return filepath.Join(filepath.Dir(top), ".devclaim")
}

// claimStep is one action in the claim sequence. A step with a non-empty skip is
// already satisfied and won't run; its label still shows so the plan is honest.
type claimStep struct {
	label string
	skip  string
	run   func(stdout, stderr io.Writer) error
}

// Claim is a resolved, not-yet-executed dev-tier claim.
type Claim struct {
	Service  string // dev unit: rlndx | api
	RepoKey  string // .devclaim entry: web | api
	Worktree string
	Symlink  string // .devclaim/<key>
	note     string // non-empty => build-before-claim advisory (web tier)
	steps    []claimStep
}

// ClaimPlan resolves the worktree, the dev service, and the claim sequence
// without executing anything. svc may be "" to derive it from the repo name. It
// errors early when the worktree is missing, pointing at prepare. noMigrate
// suppresses the api dev-DB `goose up` step.
func ClaimPlan(repo, branch, svc, into string, noMigrate bool) (*Claim, error) {
	if repo == "" {
		return nil, fmt.Errorf("repo path required")
	}
	slug, err := Slugify(branch)
	if err != nil {
		return nil, err
	}
	top, err := resolveMainRepo(repo)
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

	key := repoKeyForSvc(svc)
	symlink := filepath.Join(devclaimDir(top), key)

	c := &Claim{Service: svc, RepoKey: key, Worktree: wt, Symlink: symlink, note: buildFirstNote(svc, wt)}

	// 1. Install deps if the worktree was never prepared (node-style only; Go
	//    worktrees share the module cache, nothing to do). Skipped when present.
	if rule, ok := detectInstall(wt); ok && rule.present != "" {
		step := claimStep{
			label: shellJoin(rule.argv) + "  (cwd " + wt + ")",
			run: func(stdout, stderr io.Writer) error {
				cmd := exec.Command(rule.argv[0], rule.argv[1:]...)
				cmd.Dir = wt
				cmd.Env = append(os.Environ(), "CI=1")
				cmd.Stdout, cmd.Stderr = stdout, stderr
				return cmd.Run()
			},
		}
		if dirExists(filepath.Join(wt, rule.present)) {
			step.skip = rule.present + " already present"
		}
		c.steps = append(c.steps, step)
	}

	// 2. Repoint the dev-tier symlink at this worktree (unprivileged: group write
	//    on .devclaim). Atomic replace via a temp link + rename.
	c.steps = append(c.steps, claimStep{
		label: "repoint " + symlink + " -> " + wt,
		skip:  symlinkAlready(symlink, wt),
		run: func(stdout, stderr io.Writer) error {
			return repointSymlink(symlink, wt)
		},
	})

	// 3. (api only) Apply the clone's migrations to the isolated dev DB BEFORE the
	//    restart, so the dev-api boots against the migrated schema. Without this a
	//    clone that advanced past the dev DB makes the handlers for new
	//    columns/tables 500 while older endpoints 200. Suppressed by --no-migrate.
	if svc == "api" && !noMigrate {
		step := claimStep{
			label: "goose up (dev db) " + filepath.Join(wt, "migrations"),
			run: func(stdout, stderr io.Writer) error {
				return gooseUp(wt, stdout, stderr)
			},
		}
		if !dirExists(filepath.Join(wt, "migrations")) {
			step.skip = "no migrations/ dir"
		}
		c.steps = append(c.steps, step)
	}

	// 4. Restart the dev unit — the one privileged atom — via the in-binary dev
	//    control plane (dev.Restart bounces aphrollo-dev-<svc> and, for rlndx,
	//    clears the freshly-claimed tree's stale vite optimizer cache). The
	//    privileged step is dev.Restart's own exact-match `sudo systemctl
	//    restart`; claim itself stays unprivileged.
	c.steps = append(c.steps, claimStep{
		label: "restart dev-" + svc + " (aphrollo dev restart " + svc + ")",
		run: func(stdout, stderr io.Writer) error {
			return dev.Restart(svc, stdout, stderr)
		},
	})

	return c, nil
}

// Render returns the claim plan. apply=false is the dry-run preview; apply=true
// is the terse header printed before Apply streams each step.
func (c *Claim) Render(apply bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace claim: %s -> dev-%s\n", c.Worktree, c.Service)
	if c.note != "" {
		fmt.Fprintf(&b, "  note: %s\n", c.note)
	}
	if apply {
		fmt.Fprintf(&b, "\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\nsteps (dry-run — pass --apply to execute the [run] steps):\n")
	for i, s := range c.steps {
		tag, note := "run", ""
		if s.skip != "" {
			tag, note = "skip", "  — "+s.skip
		}
		fmt.Fprintf(&b, "  %d. [%s] %s%s\n", i+1, tag, s.label, note)
	}
	fmt.Fprintf(&b, "\nrun again with --apply to execute (repoints the dev symlink + restarts dev-%s).\n", c.Service)
	return b.String()
}

// Apply runs every non-skipped step in order, stopping at the first failure.
// Order matters (install + repoint must precede the restart), so a failed step
// means the rest is unsafe to run.
func (c *Claim) Apply(stdout, stderr io.Writer) error {
	for i, s := range c.steps {
		if s.skip != "" {
			fmt.Fprintf(stdout, "  [skip] %s — %s\n", s.label, s.skip)
			continue
		}
		fmt.Fprintf(stdout, "  [run]  %s\n", s.label)
		if err := s.run(stdout, stderr); err != nil {
			return fmt.Errorf("step %d (%s) failed: %w", i+1, s.label, err)
		}
	}
	fmt.Fprintf(stdout, "\nclaimed: dev-%s now serves %s\n", c.Service, c.Worktree)
	return nil
}

// symlinkAlready returns a skip reason if symlink already resolves to want.
func symlinkAlready(symlink, want string) string {
	if cur, err := os.Readlink(symlink); err == nil && cur == want {
		return "already points here"
	}
	return ""
}

// repointSymlink atomically replaces symlink with one pointing at target, via a
// temp link + rename so a concurrent reader never sees a missing link.
func repointSymlink(symlink, target string) error {
	tmp := symlink + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, symlink); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
