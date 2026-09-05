package workspace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// now is the clock seam behind --stale's age comparisons and `list`'s age
// column — a package var so a test pins an exact instant instead of racing
// the wall clock across fixture setup and assertion.
var now = time.Now

// ParseStaleDuration parses --stale's duration, accepting a plain Go duration
// (time.ParseDuration's own syntax, e.g. "72h") OR a trailing-"d" day count
// (e.g. "3d" => 72h) that time.ParseDuration itself rejects ("unknown unit
// d"). Zero and negative are rejected outright: a non-positive bar would make
// every age comparison in decide() pass immediately, sweeping trees that are
// not idle at all.
func ParseStaleDuration(s string) (time.Duration, error) {
	d, err := parseStaleDurationValue(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --stale duration %q: %w (want e.g. 3d or 72h)", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid --stale duration %q: stale must be a positive duration such as 3d or 36h", s)
	}
	return d, nil
}

// parseStaleDurationValue does the actual parse, with no positivity check —
// split out so ParseStaleDuration can wrap both failure modes (unparseable,
// non-positive) in one consistent error shape.
func parseStaleDurationValue(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		if n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64); err == nil {
			return time.Duration(n * float64(24*time.Hour)), nil
		}
	}
	return time.ParseDuration(s)
}

// StaleSweep removes DETACHED, PR-less worktrees that have sat idle past a
// duration bar — a separate sweep from Prune's merged-PR rule, which only
// ever considers a worktree still on a branch. A candidate must be ALL of:
// detached (no branch checked out), no PR for the directory's slug, no
// <tree>.lane marker beside it (an operator's explicit "still working this"
// flag), clean, its last commit older than Stale, AND every file's mtime
// older than Stale. Reuses removeWorktree.
type StaleSweep struct {
	Repo  string
	Stale time.Duration
}

// StaleSweepPlan resolves the repo (cwd when repoArg is "") without
// executing.
func StaleSweepPlan(repoArg string, stale time.Duration) (*StaleSweep, error) {
	path := repoArg
	if path == "" {
		path = "."
	}
	top, err := resolveMainRepo(path)
	if err != nil {
		return nil, err
	}
	return &StaleSweep{Repo: top, Stale: stale}, nil
}

// Run sweeps the repo's worktrees and removes the stale-detached ones.
// apply=false lists what WOULD be swept without mutating; apply=true removes
// them and folds in a `git worktree prune` of stale admin records.
func (s *StaleSweep) Run(apply bool, stdout, stderr io.Writer) error {
	entries, err := linkedWorktrees(s.Repo)
	if err != nil {
		return err
	}
	swept := 0
	for _, e := range entries {
		remove, reason := s.decide(e)
		if !remove {
			fmt.Fprintf(stdout, "skip: %s (%s)\n", e.Path, reason)
			continue
		}
		if !apply {
			fmt.Fprintf(stdout, "would prune: %s (%s)\n", e.Path, reason)
			swept++
			continue
		}
		if err := removeWorktree(s.Repo, e.Path, false); err != nil {
			fmt.Fprintf(stderr, "could not remove %s: %v\n", e.Path, err)
			continue
		}
		fmt.Fprintf(stdout, "pruned: %s (%s)\n", e.Path, reason)
		swept++
	}
	verb := "would prune"
	if apply {
		verb = "pruned"
		_ = exec.Command("git", "-C", s.Repo, "worktree", "prune").Run()
	}
	fmt.Fprintf(stdout, "%s %d stale worktree(s)\n", verb, swept)
	return nil
}

// decide resolves the verdict for one worktree against the --stale rule; see
// StaleSweep's doc comment for the full candidate definition.
func (s *StaleSweep) decide(e worktreeEntry) (remove bool, reason string) {
	if e.Branch != "HEAD" {
		return false, "on branch " + e.Branch
	}
	if laneMarked(e.Path) {
		return false, "lane marker present"
	}
	if !worktreeClean(e.Path) {
		return false, "dirty"
	}
	state, err := ghPRState(e.Path, prBranchKey(e))
	if err != nil {
		return false, "could not check PR state (gh unavailable)"
	}
	if state != "" {
		return false, "has a PR"
	}
	commitTime, ok := lastCommitTime(e.Path)
	if !ok {
		return false, "could not read last commit"
	}
	if age := now().Sub(commitTime); age < s.Stale {
		return false, fmt.Sprintf("last commit %dd old", ageDays(age))
	}
	fileTime, ok := newestFileMTime(e.Path)
	if !ok {
		return false, "could not read file times"
	}
	if age := now().Sub(fileTime); age < s.Stale {
		return false, fmt.Sprintf("files touched %dd old", ageDays(age))
	}
	return true, "stale"
}

// prBranchKey is the branch name to look up a worktree's PR under: the
// checked-out branch when there is one, else the worktree directory's own
// name — a detached worktree's directory is still named for the ticket
// branch it was prepared for (Slugify), so a PR opened for that branch is
// still discoverable after the tree is later left detached.
func prBranchKey(e worktreeEntry) string {
	if e.Branch != "HEAD" {
		return e.Branch
	}
	return filepath.Base(e.Path)
}

// ageDays is the whole number of days a duration spans, floored — "1d" means
// at least a full day has passed, never rounded up from a few hours.
func ageDays(d time.Duration) int {
	return int(d.Hours() / 24)
}

// laneMarked reports whether a "<tree>.lane" marker file sits beside the
// worktree — an operator's explicit flag that they are still using a
// detached tree, kept regardless of age.
func laneMarked(wt string) bool {
	_, err := os.Stat(wt + ".lane")
	return err == nil
}

// lastCommitTime returns the worktree's HEAD commit time.
func lastCommitTime(wt string) (time.Time, bool) {
	out, err := exec.Command("git", "-C", wt, "log", "-1", "--format=%ct").Output()
	if err != nil {
		return time.Time{}, false
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(sec, 0), true
}

// trackedAndUntrackedFiles lists the worktree-relative paths `git` itself
// considers part of the tree: everything tracked, plus untracked files git
// has NOT been told to ignore. `git status --porcelain` already treats this
// set as the tree's real content; walking the raw filesystem instead (the
// previous approach) picked up ignored build output — target/, node_modules/,
// vendor/, .venv/ — whose mtimes churn on every build/install regardless of
// whether the tracked files changed, which meant a --stale sweep never saw a
// tree as idle as long as something kept rebuilding it.
func trackedAndUntrackedFiles(wt string) ([]string, bool) {
	out, err := exec.Command("git", "-C", wt, "ls-files", "--cached", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return nil, false
	}
	raw := strings.TrimRight(string(out), "\x00")
	if raw == "" {
		return nil, true
	}
	return strings.Split(raw, "\x00"), true
}

// newestFileMTime returns the most recent modification time among the
// worktree's tracked-or-untracked-unignored files (trackedAndUntrackedFiles)
// — never an ignored build dir, whose churn would otherwise mask genuine
// idleness.
func newestFileMTime(wt string) (time.Time, bool) {
	files, ok := trackedAndUntrackedFiles(wt)
	if !ok {
		return time.Time{}, false
	}
	var newest time.Time
	found := false
	for _, f := range files {
		info, err := os.Stat(filepath.Join(wt, f))
		if err != nil {
			continue // deleted-but-not-yet-staged, or a race — skip, don't fail the whole scan
		}
		if !found || info.ModTime().After(newest) {
			newest = info.ModTime()
			found = true
		}
	}
	if !found {
		return time.Time{}, false
	}
	return newest, true
}

// dirtyCount returns the number of uncommitted paths `git status --porcelain`
// reports, 0 for a clean worktree or when git can't be asked.
func dirtyCount(wt string) int {
	out, err := exec.Command("git", "-C", wt, "status", "--porcelain").Output()
	if err != nil {
		return 0
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}
