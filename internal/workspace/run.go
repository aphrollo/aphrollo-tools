package workspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
	fmt.Fprintf(&b, "workspace create: %s @ %s (%s)\n", p.RepoName, p.Branch, state)
	fmt.Fprintf(&b, "  worktree: %s\n", p.Worktree)
	if apply {
		// Terse header only — Apply streams each step as it runs, so listing
		// them here too would just double the output.
		fmt.Fprintf(&b, "\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\nsteps (dry-run — run without --dry to execute the [run] steps):\n")
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
		switch {
		case p.RemoteBranchExists:
			fmt.Fprintf(&b, "\nbase: origin/%s — local branch tracks the remote tip (prior work preserved), after the fetch.\n", p.Branch)
		case p.StartPoint == "":
			// nothing to annotate (offline / no remote — falls back to local HEAD)
		case p.BranchExists:
			fmt.Fprintf(&b, "\nbase: existing branch %s; fetch refreshes %s (rebase target if behind).\n", p.Branch, p.StartPoint)
		default:
			fmt.Fprintf(&b, "\nbase: %s — the new branch starts here, after the fetch.\n", p.StartPoint)
		}
		fmt.Fprintf(&b, "\nrun again without --dry to execute.\n")
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

// List renders one line per worktree in repo (resolved to its toplevel,
// including the main clone): path, branch ("detached" for none), whole days
// since the last commit, the dirty-file count, and the branch's PR state
// ("none" when there is no PR, "?" when the lookup failed or timed out) — a
// read-only survey, so a coder can see at a glance which worktrees are live,
// idle, or already merged, without running a per-worktree operation. PR
// state comes from the same seam the prune sweeps use, resolved
// concurrently (resolvePRStates) so one slow/hung lookup cannot hold up
// every other line.
func List(repo string) (string, error) {
	if repo == "" {
		return "", fmt.Errorf("repo path required")
	}
	top, err := resolveMainRepo(repo)
	if err != nil {
		return "", err
	}
	out, err := exec.Command("git", "-C", top, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return "", fmt.Errorf("git worktree list: %w", err)
	}
	entries := parseWorktreeList(string(out))
	states := resolvePRStates(entries)
	var b strings.Builder
	for i, e := range entries {
		branch := e.Branch
		if branch == "HEAD" {
			branch = "detached"
		}
		days := 0
		if t, ok := lastCommitTime(e.Path); ok {
			days = ageDays(now().Sub(t))
		}
		fmt.Fprintf(&b, "%s  %s  %dd  %d dirty  PR %s\n", e.Path, branch, days, dirtyCount(e.Path), states[i])
	}
	return b.String(), nil
}

// listPRLookupConcurrency bounds how many gh PR-state lookups List runs at
// once — the whole point is that a lookup runs while others are still in
// flight, but an unbounded fan-out for a repo with dozens of worktrees would
// just trade "slow" for "gh rate-limited".
const listPRLookupConcurrency = 4

// listPRLookupTimeout caps how long List waits for one worktree's PR-state
// lookup before rendering "?" — a var (not const), like gitNetworkTimeout and
// ghTimeout, so a test can shrink it and prove the deadline actually fires.
var listPRLookupTimeout = 5 * time.Second

// resolvePRStates runs ghPRState for every entry CONCURRENTLY, bounded to
// listPRLookupConcurrency in flight at once, each capped at
// listPRLookupTimeout. Results come back in the SAME order as entries — List
// renders deterministically regardless of which lookup finishes first.
func resolvePRStates(entries []worktreeEntry) []string {
	states := make([]string, len(entries))
	sem := make(chan struct{}, listPRLookupConcurrency)
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, e worktreeEntry) {
			defer wg.Done()
			defer func() { <-sem }()
			states[i] = prStateOrUnknown(e)
		}(i, e)
	}
	wg.Wait()
	return states
}

// prStateOrUnknown resolves one worktree's PR state, racing ghPRState against
// listPRLookupTimeout. "none" means the lookup succeeded and found no PR; "?"
// means the lookup itself failed or did not return in time — a different
// fact, never conflated.
func prStateOrUnknown(e worktreeEntry) string {
	ctx, cancel := context.WithTimeout(context.Background(), listPRLookupTimeout)
	defer cancel()
	result := make(chan string, 1)
	// Capture the seam's CURRENT function value here, on this goroutine, before
	// spawning: ghPRState has no context param, so a lookup that outruns the
	// timeout below keeps running in an abandoned goroutine. If that goroutine
	// read the package var ghPRState directly, it would race a later test's
	// stubPRState reassigning it after THIS test has already returned (real
	// failure caught under -race). Calling the captured value instead means the
	// abandoned goroutine never touches the package var again.
	fn := ghPRState
	go func() {
		state, err := fn(e.Path, prBranchKey(e))
		if err != nil {
			result <- "?"
			return
		}
		if state == "" {
			state = "none"
		}
		result <- state
	}()
	select {
	case s := <-result:
		return s
	case <-ctx.Done():
		return "?"
	}
}

// Removal tears down a ticket's worktree AND its local branch, kept dry-run
// friendly: the caller prints Display, then calls Run on --apply. Both steps are
// idempotent — an already-gone worktree or branch is a [skip], not a failure —
// so a cleanup path can re-run on redelivery without wedging.
type Removal struct {
	KeepBranch bool   // leave the local branch in place (default: delete it)
	Force      bool   // remove even a dirty worktree
	top        string // main clone toplevel the worktree + branch belong to
	worktree   string // the linked worktree dir to remove
	branch     string // the local branch to delete (original name, not the slug)
}

// RemovePlan resolves the worktree path + local branch for repo+branch and
// returns the removal without executing it.
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
	return &Removal{
		top:      top,
		worktree: wt,
		branch:   branch,
	}, nil
}

// Display renders the dry-run preview command line. Read it AFTER setting
// KeepBranch — it reflects that flag rather than baking a stale line in at
// plan time, so a --dry preview never claims a branch delete Run will not
// perform.
func (r *Removal) Display() string {
	if r.KeepBranch {
		return fmt.Sprintf("git -C %s worktree remove %s", r.top, r.worktree)
	}
	return fmt.Sprintf("git -C %s worktree remove %s && git -C %s branch -D %s", r.top, r.worktree, r.top, r.branch)
}

// Run removes the worktree and deletes the local branch, streaming a per-step
// [removed]/[skip] receipt. Each step is idempotent: an already-gone worktree or
// branch is reported as [skip] and is not an error, so re-running the same
// removal (e.g. a redelivered cleanup) is a no-op success. A genuine failure
// (e.g. a dirty worktree that git refuses to drop) is returned.
func (r *Removal) Run(stdout, stderr io.Writer) error {
	if err := r.removeWorktree(stdout); err != nil {
		return err
	}
	// Drop any stale admin record left behind (the dir is gone but git may still
	// list the worktree) before deleting the branch it pointed at.
	_ = exec.Command("git", "-C", r.top, "worktree", "prune").Run()
	if r.KeepBranch {
		fmt.Fprintf(stdout, "[kept] branch %s\n", r.branch)
		return nil
	}
	return r.deleteBranch(stdout)
}

// removeWorktree drops the linked worktree, treating an already-absent tree as a
// skip. git itself errors on a missing worktree, so absence is detected up front
// (dir gone) and also recovered from git's "is not a working tree" message.
func (r *Removal) removeWorktree(stdout io.Writer) error {
	if _, err := os.Stat(r.worktree); os.IsNotExist(err) {
		fmt.Fprintf(stdout, "[skip] worktree %s — already gone\n", r.worktree)
		return nil
	}
	args := []string{"-C", r.top, "worktree", "remove"}
	if r.Force {
		args = append(args, "--force")
	}
	args = append(args, r.worktree)
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		if isNotAWorktree(string(out)) {
			fmt.Fprintf(stdout, "[skip] worktree %s — already gone\n", r.worktree)
			return nil
		}
		return fmt.Errorf("git worktree remove %s: %v\n%s", r.worktree, err, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(stdout, "[removed] worktree %s\n", r.worktree)
	return nil
}

// deleteBranch removes the local branch, treating an already-absent branch as a
// skip. The worktree no longer holds the branch (removed above), so -D never
// fails on "checked out".
func (r *Removal) deleteBranch(stdout io.Writer) error {
	if !localBranchExists(r.top, r.branch) {
		fmt.Fprintf(stdout, "[skip] branch %s — already gone\n", r.branch)
		return nil
	}
	// "--" guards the branch name as a positional (defense in depth behind Slugify,
	// which the branch passed at plan time).
	out, err := exec.Command("git", "-C", r.top, "branch", "-D", "--", r.branch).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git branch -D %s: %v\n%s", r.branch, err, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(stdout, "[removed] branch %s\n", r.branch)
	return nil
}

// localBranchExists reports whether repo has a local branch by this name.
func localBranchExists(repo, branch string) bool {
	return exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

// isNotAWorktree reports whether git's output is the benign "the dir is not a
// registered worktree" message (an absence) rather than a real removal failure.
func isNotAWorktree(out string) bool {
	return strings.Contains(out, "is not a working tree") || strings.Contains(out, "not a working tree")
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
