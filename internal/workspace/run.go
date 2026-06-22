package workspace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Render returns the human-readable plan. With apply=false it is the dry-run
// preview; with apply=true it is the header printed before execution. Output is
// deterministic for a given plan.
func Render(p *Plan, apply bool) string {
	var b strings.Builder
	state := "existing branch"
	if !p.BranchExists {
		state = "new branch"
	}
	fmt.Fprintf(&b, "workspace prepare: %s @ %s (%s)\n", p.RepoName, p.Branch, state)
	fmt.Fprintf(&b, "  worktree: %s\n", p.Worktree)
	if apply {
		// Terse header only — Apply streams each step as it runs, so listing
		// them here too would just double the output.
		fmt.Fprintf(&b, "\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\nsteps (dry-run — pass --apply to execute the [run] steps):\n")
	for i, s := range p.Steps {
		tag := "run"
		note := ""
		if s.Skip != "" {
			tag = "skip"
			note = "  — " + s.Skip
		}
		fmt.Fprintf(&b, "  %d. [%s] %s%s\n", i+1, tag, shellJoin(s.Cmd), note)
		if s.Dir != "" {
			fmt.Fprintf(&b, "         (cwd %s)\n", s.Dir)
		}
	}
	if !apply {
		if p.StartPoint != "" {
			if p.BranchExists {
				fmt.Fprintf(&b, "\nbase: existing branch %s; fetch refreshes %s (rebase target if behind).\n", p.Branch, p.StartPoint)
			} else {
				fmt.Fprintf(&b, "\nbase: %s — the new branch starts here, after the fetch.\n", p.StartPoint)
			}
		}
		fmt.Fprintf(&b, "\nrun again with --apply to execute.\n")
	}
	return b.String()
}

// Apply executes every non-skipped step in order, streaming each command's
// output to stdout/stderr. It stops at the first failure and returns that
// error — order matters (safe.directory must precede the worktree add), so a
// failed step means the rest of the plan is unsafe to run. On success it prints
// the ready worktree path.
func Apply(p *Plan, stdout, stderr io.Writer) error {
	env := append(os.Environ(), "CI=1") // keep pnpm/npm non-interactive
	for i, s := range p.Steps {
		if s.Skip != "" {
			fmt.Fprintf(stdout, "  [skip] %s — %s\n", s.Title, s.Skip)
			continue
		}
		fmt.Fprintf(stdout, "  [run]  %s\n", shellJoin(s.Cmd))
		if len(s.Cmd) == 0 {
			continue
		}
		cmd := exec.Command(s.Cmd[0], s.Cmd[1:]...)
		cmd.Dir = s.Dir
		cmd.Env = env
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			if s.NonFatal {
				// e.g. an offline `fetch origin` — warn, keep going (the worktree
				// falls back to the local tip), don't abort the whole prepare.
				fmt.Fprintf(stdout, "  [warn] %s failed (continuing): %v\n", s.Title, err)
				continue
			}
			return fmt.Errorf("step %d (%s) failed: %w", i+1, s.Title, err)
		}
	}
	fmt.Fprintf(stdout, "\nready: %s\n", p.Worktree)
	reportBase(p, stdout)
	return nil
}

// reportBase prints the worktree's base commit and, when a default remote branch
// resolved, how far behind it the worktree is — computed AFTER the fetch step so
// the count reflects the live upstream. A fresh new branch reads 0; an existing
// branch that has fallen behind gets a rebase nudge. Best-effort: silent on the
// base detail if the worktree or refs can't be read.
func reportBase(p *Plan, stdout io.Writer) {
	out, err := exec.Command("git", "-C", p.Worktree, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return
	}
	base := strings.TrimSpace(string(out))
	n, ok := 0, false
	if p.StartPoint != "" {
		n, ok = gitBehindCount(p.Worktree, "HEAD", p.StartPoint)
	}
	switch {
	case !ok:
		fmt.Fprintf(stdout, "  base: %s\n", base)
	case n == 0:
		fmt.Fprintf(stdout, "  base: %s (up to date with %s)\n", base, p.StartPoint)
	default:
		fmt.Fprintf(stdout, "  base: %s — %d commit(s) behind %s; rebase before working\n", base, n, p.StartPoint)
	}
}

// List returns `git worktree list` output for the repo (resolved to its
// toplevel) — a read-only view of every worktree, prepared or not.
func List(repo string) (string, error) {
	if repo == "" {
		return "", fmt.Errorf("repo path required")
	}
	top, err := resolveMainRepo(repo)
	if err != nil {
		return "", err
	}
	out, err := exec.Command("git", "-C", top, "worktree", "list").Output()
	if err != nil {
		return "", fmt.Errorf("git worktree list: %w", err)
	}
	return string(out), nil
}

// Removal is a single resolved `git worktree remove` command, kept dry-run
// friendly: the caller prints Display, then calls Run on --apply.
type Removal struct {
	Display string
	argv    []string
}

// RemovePlan resolves the worktree path for repo+branch and returns the removal
// command without executing it.
func RemovePlan(repo, branch, into string) (*Removal, error) {
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
	base := into
	if base == "" {
		base = DefaultWorktreeBase(top)
	}
	wt := filepath.Join(base, slug)
	// git worktree remove refuses to drop the cwd with a cryptic error; surface a
	// clear one first, matching the guard in cleanup.go.
	if cwd, err := os.Getwd(); err == nil && pathWithin(cwd, wt) {
		return nil, fmt.Errorf("refusing to remove the worktree you're standing in — cd out first:\n  cd %s && aphrollo workspace remove %s", top, branch)
	}
	argv := []string{"git", "-C", top, "worktree", "remove", wt}
	return &Removal{Display: shellJoin(argv), argv: argv}, nil
}

// Run executes the removal, streaming output.
func (r *Removal) Run(stdout, stderr io.Writer) error {
	cmd := exec.Command(r.argv[0], r.argv[1:]...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// shellJoin renders argv for display, quoting only the args that need it. The
// paths prepare handles are sanitized, so this is for readability, not security.
func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\"'\\") {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
