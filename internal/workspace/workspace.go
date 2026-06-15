// Package workspace folds the deterministic "get me a clean place to work"
// dance into one command. An agent that needs an isolated worktree to run tests
// or build a branch otherwise spends a fistful of tool calls (and tokens) on
// the same mechanical steps every time:
//
//  1. git config --global --add safe.directory <repo>   (cross-owner repos)
//  2. git worktree add [-b] <branch> <wt>
//  3. git config --global --add safe.directory <wt>
//  4. pnpm install (or npm / go mod download) inside the fresh worktree
//
// `aphrollo workspace prepare` computes that sequence as a Plan, prints it
// dry-run by default (the repo's design contract: show exactly what changes
// before it changes), and runs it on --apply. Every step is idempotent — an
// already-marked safe.directory, an existing worktree, or an installed
// node_modules is reported as skipped rather than redone, so re-running prepare
// on a half-built workspace finishes the job without clobbering it.
package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Request is the parsed input to BuildPlan.
type Request struct {
	Repo      string // path to the main working tree (resolved to its toplevel)
	Branch    string // branch to create or check out in the worktree
	Into      string // base dir for worktrees; "" => <repo-parent>/.worktrees/<repo-name>
	NoInstall bool   // skip the dependency-install step
	NoSafeDir bool   // skip the safe.directory marking steps
	Reinstall bool   // run the install step even if its marker dir already exists
}

// Step is one unit of the plan. A Step with a non-empty Skip is already
// satisfied and will not run on --apply; its Cmd is still shown so the plan is a
// complete, honest record of what prepare considered.
type Step struct {
	Title string   // human label
	Cmd   []string // argv; nil for a purely informational step
	Dir   string   // working directory for Cmd ("" => inherit)
	Skip  string   // non-empty => already satisfied, won't execute
}

// Plan is the fully-resolved prepare sequence for one repo+branch.
type Plan struct {
	Repo         string
	RepoName     string
	Branch       string
	Slug         string
	Worktree     string
	BranchExists bool
	Steps        []Step
}

var slugOK = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Slugify collapses a branch name into a single safe path component (slashes
// become dashes), rejecting traversal and any character outside [A-Za-z0-9._-]
// so the worktree path can never escape its base dir. Mirrors the slugify in
// the aphrollo-dev shell helper so the two agree on worktree layout.
func Slugify(branch string) (string, error) {
	if branch == "" {
		return "", fmt.Errorf("branch required")
	}
	if strings.Contains(branch, "..") || strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") {
		return "", fmt.Errorf("bad branch name: %s", branch)
	}
	s := strings.ReplaceAll(branch, "/", "-")
	if !slugOK.MatchString(s) {
		return "", fmt.Errorf("bad branch name: %s", branch)
	}
	return s, nil
}

// installRule maps a marker file in the worktree root to the install command and
// the directory whose presence means "already installed". Order matters: the
// first matching marker wins, so a pnpm-lock beats a bare package.json.
type installRule struct {
	marker  string   // file whose presence selects this rule
	argv    []string // install command, run with --dir/worktree-relative cwd
	present string   // dir under the worktree that means deps are already there ("" => never skip)
}

var installRules = []installRule{
	{marker: "pnpm-lock.yaml", argv: []string{"pnpm", "install"}, present: "node_modules"},
	{marker: "yarn.lock", argv: []string{"yarn", "install"}, present: "node_modules"},
	{marker: "package-lock.json", argv: []string{"npm", "ci"}, present: "node_modules"},
	{marker: "package.json", argv: []string{"npm", "install"}, present: "node_modules"},
	{marker: "go.mod", argv: []string{"go", "mod", "download"}, present: ""},
}

// detectInstall picks the install rule for a worktree root by probing for marker
// files in priority order. Returns ok=false when nothing matches (no install
// step is added).
func detectInstall(root string) (installRule, bool) {
	for _, r := range installRules {
		if fileExists(filepath.Join(root, r.marker)) {
			return r, true
		}
	}
	return installRule{}, false
}

// DefaultWorktreeBase returns the worktrees base dir for a repo:
// <repo-parent>/.worktrees/<repo-name>. For a repo at
// /home/debian/spaces/aphrollo/aphrollo-web this is
// /home/debian/spaces/aphrollo/.worktrees/aphrollo-web — the same layout the
// aphrollo-dev helper uses, so a prepared worktree can later be `claim`ed.
func DefaultWorktreeBase(repo string) string {
	return filepath.Join(filepath.Dir(repo), ".worktrees", filepath.Base(repo))
}

// BuildPlan inspects the repo (read-only) and computes the prepare sequence. It
// resolves the repo to its git toplevel, decides whether the branch already
// exists, where the worktree goes, and which steps are already satisfied. No
// state is mutated here — that is Apply's job.
func BuildPlan(req Request) (*Plan, error) {
	if req.Repo == "" {
		return nil, fmt.Errorf("repo path required")
	}
	slug, err := Slugify(req.Branch)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(req.Repo)
	if err != nil {
		return nil, err
	}
	top, err := gitToplevel(abs)
	if err != nil {
		return nil, err
	}

	base := req.Into
	if base == "" {
		base = DefaultWorktreeBase(top)
	}
	wt := filepath.Join(base, slug)
	branchExists := gitBranchExists(top, req.Branch)
	wtExists := dirExists(wt)

	p := &Plan{
		Repo:         top,
		RepoName:     filepath.Base(top),
		Branch:       req.Branch,
		Slug:         slug,
		Worktree:     wt,
		BranchExists: branchExists,
	}

	// 1. Mark the main repo git-safe so cross-owner worktree ops don't trip
	//    "dubious ownership". Must precede the worktree add.
	if !req.NoSafeDir {
		p.Steps = append(p.Steps, safeDirStep(top))
	}

	// 2. Create the worktree (reuse the branch if it exists, else create it).
	add := Step{
		Title: "create worktree",
		Cmd:   []string{"git", "-C", top, "worktree", "add"},
	}
	if branchExists {
		add.Cmd = append(add.Cmd, wt, req.Branch)
	} else {
		add.Cmd = append(add.Cmd, "-b", req.Branch, wt)
	}
	if wtExists {
		add.Skip = "worktree already exists"
	}
	p.Steps = append(p.Steps, add)

	// 3. Mark the worktree itself git-safe for subsequent ops inside it.
	if !req.NoSafeDir {
		p.Steps = append(p.Steps, safeDirStep(wt))
	}

	// 4. Install dependencies for a fresh worktree (git worktrees do NOT share
	//    the main tree's gitignored node_modules). Skipped when the marker dir
	//    already exists, unless --reinstall.
	if !req.NoInstall {
		if rule, ok := detectInstall(top); ok {
			step := Step{
				Title: "install dependencies",
				Cmd:   rule.argv,
				Dir:   wt,
			}
			if rule.present != "" && !req.Reinstall && dirExists(filepath.Join(wt, rule.present)) {
				step.Skip = rule.present + " already present (pass --reinstall to force)"
			}
			p.Steps = append(p.Steps, step)
		}
	}

	return p, nil
}

// --- git / fs helpers -------------------------------------------------------

func gitToplevel(path string) (string, error) {
	out, err := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("%s is not a git repository", path)
	}
	return strings.TrimSpace(string(out)), nil
}

func gitBranchExists(repo, branch string) bool {
	// show-ref exits 0 iff the ref resolves; quiet keeps it silent.
	return exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

// safeDirSet returns true if path is already a global safe.directory entry.
func safeDirSet(path string) bool {
	out, err := exec.Command("git", "config", "--global", "--get-all", "safe.directory").Output()
	if err != nil {
		return false // no entries (or no global config) => not set
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.TrimSpace(line) == path || strings.TrimSpace(line) == "*" {
			return true
		}
	}
	return false
}

func safeDirStep(path string) Step {
	s := Step{
		Title: "mark git-safe",
		Cmd:   []string{"git", "config", "--global", "--add", "safe.directory", path},
	}
	if safeDirSet(path) {
		s.Skip = "already a safe.directory"
	}
	return s
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
