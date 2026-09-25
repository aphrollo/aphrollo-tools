package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// unstagedDiscardEnv is the LOUDER of the two markers: APHROLLO_DISCARD=1
// covers work the object store still holds, this one covers work that exists
// nowhere but the working tree. Kept as a named constant because the refusal
// line, the override log and the tests all have to spell it identically.
const unstagedDiscardEnv = "APHROLLO_DISCARD_UNSTAGED"

// discardWallRefusal is the discard wall's shim-side half: discardIntent and
// discardCostOf (git_shim_discard.go) classify and measure, this decides
// what to do about it. refuse=true means the caller prints line and stops
// before git runs. refuse=false with a non-empty line is the louder marker's
// NOTICE: git still runs, and the caller prints what it is about to destroy
// first. refuse=false with an empty line covers both "this invocation
// destroys nothing" and "an override consumed itself", and either way the
// caller just runs git normally.
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
	if envOK, unstagedOK := os.Getenv("APHROLLO_DISCARD") == "1", os.Getenv(unstagedDiscardEnv) == "1"; envOK || unstagedOK {
		return markerDecision(cfg, workDir, session, form, paths, unstagedOK)
	}
	if tdd.ConsumeOneShot(tdd.WallDiscard) {
		tdd.LogOverride("override-discard-used", session, workDir)
		return "", false
	}
	// The Bash/PowerShell PreToolUse hook (postedit.DiscardBashDecision)
	// meets this SAME command first — a shell runs `git` directly, never
	// through this shim — and if an armed `gate allow discard` waiver let it
	// through there, ConsumeOneShot above already spent it: the arm is
	// spent by the FIRST check regardless of which side made it, so this
	// invocation, arriving a moment later as its own subprocess, would
	// otherwise find nothing left to consume and refuse a command its own
	// session already approved. markDiscardBashSpent left a one-shot record
	// scoped to this EXACT argv for exactly that case (#857 follow-up: one
	// arm covers one command end to end).
	if tdd.ConsumeDiscardBashSpent(rest) {
		tdd.LogOverride("override-discard-bash-spent", session, workDir)
		return "", false
	}
	suffix := ""
	if cost.Err != nil {
		suffix = "-unmeasured"
	}
	// The hint is empty except for a path-scoped restore of a file this
	// session holds a pre-mutation state for: mid-proof, the refusal that
	// says nothing about the hold is the one that made the field builder
	// assume the restore was simply impossible and mutate on (#650).
	refusal := discardRefusalLine(form, cost) + mutationHoldHint(form, paths, workDir) + probeDiscardHint(form)
	return discardRefused(workDir, form, refusal, suffix)
}

// probeDiscardHint names the sanctioned route back to HEAD on the two
// refusals a lane meets when it strips a refused probe arm: without it the
// refusal offers only overrides, and #836's lane reached for an unaudited
// `git apply -R` instead.
func probeDiscardHint(form string) string {
	if !isPathRestoreForm(form) {
		return ""
	}
	return "; to strip a refused probe arm back to HEAD, aphrollo gate probe discard <files> backs the diff up first"
}

// markerDecision is what an environment marker buys. APHROLLO_DISCARD=1 was a
// blanket yes, which is the whole of issue #650's first half: it restored a
// file to HEAD and took the operator's unstaged edits with it, having shown
// them no number for what they were about to lose. So the marker is now
// bounded by what the object store can still reach — staged, committed and
// stashed work is recoverable with `git fsck`/`git stash`, and an unstaged
// edit is recoverable by nothing at all — and the invocation that would
// destroy the second kind is refused BY NAME, listing the files.
//
// The one-shot `aphrollo gate allow discard` arm is deliberately not bounded
// the same way: it is armed by hand in answer to a refusal that has already
// printed the cost, so the operator has seen the number this check exists to
// show them. A variable exported once in a shell profile never shows anyone
// anything.
//
// unstagedOK is the louder marker, which is strictly wider than the quieter
// one and so implies it: it names every file it is about to destroy on
// stderr, then lets git run.
func markerDecision(cfg gitShimConfig, workDir, session, form string, paths []string, unstagedOK bool) (string, bool) {
	override := "override-discard-env"
	if unstagedOK {
		override = "override-discard-unstaged"
	}
	victims, err := unstagedVictims(cfg.realGit, workDir, form, paths)
	if err != nil {
		// Fail-closed, exactly as a failed cost measurement is: a git that
		// cannot say what is unstaged cannot be trusted to say there is
		// nothing to lose.
		return discardRefused(workDir, form, unstagedUnmeasuredLine(form, err), "-unmeasured")
	}
	if len(victims) == 0 {
		tdd.LogOverride(override, session, workDir)
		return "", false
	}
	if !unstagedOK {
		return discardRefused(workDir, form, unstagedRefusalLine(form, victims), "-unstaged")
	}
	tdd.LogOverride(override, session, workDir)
	return unstagedNoticeLine(form, victims), false
}

// discardRefused records one refusal and returns it in the caller's shape.
// formSuffix separates the three reasons a form is refused in the tally:
// nothing (a refusal over real numbers), "-unmeasured" (fail-closed, git
// could not answer) and "-unstaged" (the marker was set and the invocation
// would have destroyed work nothing can bring back).
func discardRefused(workDir, form, line, formSuffix string) (string, bool) {
	formKey := strings.ReplaceAll(form, " ", "-") + formSuffix
	tdd.AppendGateLog("git", workDir, "git", "git-discard-refused:"+formKey, 0)
	return line, true
}

// unstagedVictims names the files this invocation would destroy work in that
// NOTHING can bring back: a tracked file whose working copy differs from the
// index (the edit exists in no commit and no index entry), and — for `clean`,
// whose entire job is deleting them — an untracked file. Sorted, so two runs
// over one tree print the same line.
//
// Per form, because the forms do not destroy the same things: reset
// --hard/--merge and checkout -f overwrite the whole working tree but leave
// untracked files alone; checkout -- <paths> and restore <paths> overwrite
// only the paths they name; clean deletes untracked files and touches no
// tracked one; worktree remove --force destroys both kinds, inside the target
// worktree rather than this one. `stash drop`/`clear` and `branch -D` are
// empty here and say so: they unlink commits, which the reflog and `git fsck`
// still reach, so the ordinary marker remains the right cover for them.
func unstagedVictims(realGit, workDir, form string, paths []string) ([]string, error) {
	switch {
	case form == "worktree remove --force":
		target := worktreeTarget(workDir, paths)
		if target == "" {
			return nil, nil
		}
		modified, err := modifiedVsIndex(realGit, target, nil)
		if err != nil {
			return nil, err
		}
		untracked, err := untrackedPaths(realGit, target, nil, false)
		if err != nil {
			return nil, err
		}
		return sortedUnique(append(modified, untracked...)), nil
	case strings.HasPrefix(form, "clean "):
		untracked, err := untrackedPaths(realGit, workDir, paths, strings.Contains(form, "x"))
		if err != nil {
			return nil, err
		}
		return sortedUnique(untracked), nil
	case strings.HasPrefix(form, "stash "), strings.HasPrefix(form, "branch -D "):
		return nil, nil
	default:
		scope := paths
		if form == "reset --hard" || form == "reset --merge" || form == "checkout -f" {
			scope = nil
		}
		modified, err := modifiedVsIndex(realGit, workDir, scope)
		if err != nil {
			return nil, err
		}
		return sortedUnique(modified), nil
	}
}

// worktreeTarget resolves the worktree `worktree remove --force` names,
// against workDir when git was given a relative one — the same resolution
// worktreeCost makes, and for the same reason: a relative cmd.Dir would
// resolve against this process's own cwd instead.
func worktreeTarget(workDir string, paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	target := paths[0]
	if !filepath.IsAbs(target) {
		target = filepath.Join(workDir, target)
	}
	return target
}

// modifiedVsIndex lists the tracked files whose working copy differs from the
// index — `git diff --name-only`, the same comparison diffCostIndexOnly
// counts, asked for names instead of numbers.
func modifiedVsIndex(realGit, workDir string, paths []string) ([]string, error) {
	args := append([]string{"diff", "--name-only"}, pathArgs(paths)...)
	out, err := runGitCapture(realGit, workDir, args...)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// untrackedPaths lists what untrackedCost counts.
func untrackedPaths(realGit, workDir string, paths []string, includeIgnored bool) ([]string, error) {
	args := []string{"ls-files", "--others"}
	if !includeIgnored {
		args = append(args, "--exclude-standard")
	}
	args = append(args, pathArgs(paths)...)
	out, err := runGitCapture(realGit, workDir, args...)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// maxNamedVictims bounds the file list a refusal prints. A `clean -fdx` in a
// build tree can name thousands; the count is the measurement, the names are
// so the operator recognises what they are about to lose.
const maxNamedVictims = 10

// namedVictims renders the list, capped.
func namedVictims(victims []string) string {
	if len(victims) <= maxNamedVictims {
		return strings.Join(victims, ", ")
	}
	return strings.Join(victims[:maxNamedVictims], ", ") + fmt.Sprintf(", +%d more", len(victims)-maxNamedVictims)
}

// unstagedRefusalLine is the refusal the quieter marker now earns. It names
// the files, says why this work is different from what the marker covers, and
// gives both ways forward: stage it (which makes it recoverable, and is the
// safe loop the README documents), or say so louder.
func unstagedRefusalLine(form string, victims []string) string {
	return fmt.Sprintf("gate: refused — %s would destroy unstaged work in %d file(s) (%s) that no commit, "+
		"index or stash holds, so nothing can bring it back; APHROLLO_DISCARD=1 does not cover that. "+
		"Stage it (git add) and re-run, or %s=1 to destroy it deliberately",
		form, len(victims), namedVictims(victims), unstagedDiscardEnv)
}

// unstagedNoticeLine is what the louder marker prints BEFORE git runs: the
// marker exists to make a loss deliberate, and a loss nobody was shown is not
// deliberate.
func unstagedNoticeLine(form string, victims []string) string {
	return fmt.Sprintf("gate: %s=1 — %s is destroying unstaged work in %d file(s): %s",
		unstagedDiscardEnv, form, len(victims), namedVictims(victims))
}

// unstagedUnmeasuredLine refuses a marker whose unstaged set could not be
// measured at all.
func unstagedUnmeasuredLine(form string, err error) string {
	return fmt.Sprintf("gate: refused — %s: could not determine which files carry unstaged work (%s); "+
		"retry, or %s=1 to discard without that answer", form, firstErrorLine(err), unstagedDiscardEnv)
}
