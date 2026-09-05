package tdd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// mutantsRefusal is why no mutation job was built. Reason is written for
// somebody who typed a command and got no run: it says what stopped it and,
// where there is one, the setting that would let it through.
//
// Routine marks the refusals that are the design working rather than a fault:
// a commit on trunk has no lane to prove, and a repo that never asked for
// receipts is not owed one. The post-commit hook stays silent about those
// because it fires after every commit on the box; a hand-typed run prints them
// all, because there the refusal IS the answer to what was just asked.
type mutantsRefusal struct {
	Reason   string
	Routine  bool
	LogEvent string
}

func (r mutantsRefusal) refused() bool { return r.Reason != "" }

// buildMutantsJob describes the run a checkout needs, without starting it.
// Both entry points go through it — the detached post-commit start and the
// hand-typed foreground run — so the two can never disagree about which
// commits a lane is measured over. stage names the caller in the gate log.
func buildMutantsJob(repoRoot, stage string) (MutantsJob, mutantsRefusal, error) {
	root := RepoRoot(repoRoot)
	if root == "" {
		// The caller's spelling is usually ".", which names nothing to
		// somebody reading the line afterwards in a log.
		where := repoRoot
		if abs, err := filepath.Abs(repoRoot); err == nil {
			where = abs
		}
		return MutantsJob{}, mutantsRefusal{
			Reason:  fmt.Sprintf("%s is not inside a git repository", where),
			Routine: true,
		}, nil
	}
	branch := gitOut(root, "rev-parse", "--abbrev-ref", "HEAD")
	switch {
	case branch == "" || branch == "HEAD":
		// Not routine: a detached HEAD in a repo that wants receipts is
		// somebody mid-rebase or mid-bisect, and a run that quietly does not
		// happen there is a receipt missing at merge time for no stated
		// reason.
		return MutantsJob{}, mutantsRefusal{
			Reason: "HEAD is detached, so there is no lane to measure — check out the lane branch first",
		}, nil
	case isDefaultBranch(branch):
		return MutantsJob{}, mutantsRefusal{
			Reason:  fmt.Sprintf("this checkout is on %s — a mutation run proves a LANE before it merges, and trunk is what it merges into", branch),
			Routine: true,
		}, nil
	case !mutationReceiptOptIn(root):
		return MutantsJob{}, mutantsRefusal{
			Reason:  "this repo has not asked for mutation receipts — set `mutation-receipt = true` under [workspace.metadata.aphrollo] in Cargo.toml, or under [aphrollo] in aphrollo.toml",
			Routine: true,
		}, nil
	case !mutationRunsLocally(root):
		return MutantsJob{}, mutantsRefusal{
			Reason:  "this repo runs its mutants in CI (`mutants-local = false`), so there is nothing for this box to run",
			Routine: true,
		}, nil
	}
	j := MutantsJob{
		Schema: StateSchema, Repo: commonGitDir(root), RepoID: repoIdentity(root), RepoRoot: root, Branch: branch,
		Tip: gitOut(root, "rev-parse", "HEAD"), TipTree: gitOut(root, "rev-parse", "HEAD:"),
		BaseRef: laneBaseRef(root), Worktree: MutantsWorktreeDir(root), TargetDir: MutantsTargetDir(root),
	}
	// Not merge-base(BaseRef, HEAD): BaseRef prefers origin/main, which no
	// fetch ever updates, so a lane that caught up by merging LOCAL main
	// would measure from before that merge and be charged for trunk's own
	// changes (issue #261). laneBaseSHA keeps the newest trunk commit the
	// lane already contains.
	j.BaseSHA = laneBaseSHA(root)
	if j.Tip == "" || j.TipTree == "" || j.BaseSHA == "" {
		// An empty answer from git is not "no work to do": it means the
		// commits this run would measure between could not be resolved, which
		// is a fault worth a line either way.
		return MutantsJob{}, mutantsRefusal{
			Reason: fmt.Sprintf("could not resolve what to measure on %s (tip %q, base %q) — is there a commit on this lane, and a trunk it branched from?", branch, j.Tip, j.BaseSHA),
		}, nil
	}
	j.Diff = filepath.Join(mutantsStateDir(), "lane."+projectKey(root)+".diff")
	// Beside the gate's other state, never inside the worktree cargo-mutants
	// mutates in place: a log file created there before the worktree exists is
	// a log the worktree's own creation can never reach, and one written
	// during a run is an untracked file that makes the run's own dirty check
	// fail the tree it is describing.
	logDir := mutantsLogDir(j.RepoRoot)
	j.Log = filepath.Join(logDir, short(j.TipTree)+".log")
	j.ErrLog = filepath.Join(logDir, short(j.TipTree)+".err.log")
	j.Started = time.Now()
	// Before anything is spawned: a run that cannot fit its copies fills the
	// drive and dies mid-way, taking every verdict it had reached with it.
	jobs, _ := mutantsJobsForThisBox()
	if ok, line := mutantsDiskOK(j.TargetDir, jobs); !ok {
		appendGateLog(stage, logToken(j.Repo), "mutants", "mutants-refused:disk", 0)
		return MutantsJob{}, mutantsRefusal{Reason: line}, nil
	}
	// The worktree is prepared HERE, synchronously, rather than left to the
	// detached child: a bad path (or a drive that cannot create it) is then
	// caught before this process ever reports the run as started, instead of
	// the child discovering it seconds later with nowhere left to say so but
	// a log line nobody watching the hook's own output would see.
	if err := prepareMutantsWorktree(j); err != nil {
		appendGateLog(stage, logToken(j.Repo), "mutants", "mutants-worktree-failed:"+logToken(err.Error()), 0)
		return MutantsJob{}, mutantsRefusal{}, err
	}
	return j, mutantsRefusal{}, nil
}

// mutantsForegroundFn is the in-process run, a seam so a test can prove WHICH
// job a hand-typed command would measure without spending a mutation run to
// find out.
var mutantsForegroundFn = runMutantsJob

// RunMutantsHere is `aphrollo gate mutants run` typed by hand, with no --job:
// it builds the job for the checkout it is standing in and runs it in the
// FOREGROUND, holding the box-wide run lock for the whole producer call.
//
// It exists because there was no such command. `run` is the detached
// wrapper's own entry point, addressed by a job file only the post-commit hook
// writes; typed without one it read an empty path, failed, and returned 0 — a
// command that asked for a mutation run, printed nothing and ran nothing. With
// no hand-runnable entry point at all, a session that needed a receipt reached
// for the producer script directly, which is exactly the unlocked path the
// box-wide lock was added to prevent (issue #253).
func RunMutantsHere(dir string, out io.Writer) int {
	j, refusal, err := buildMutantsJob(dir, "mutants")
	if err != nil {
		logf(out, "aphrollo gate mutants run: %v", err)
		return 1
	}
	if refusal.refused() {
		// Every refusal is printed here, routine or not: somebody typed a
		// command and is owed the reason it measured nothing. Non-zero for
		// the same reason — a caller that scripts this must be able to tell
		// "measured" from "declined to".
		logf(out, "aphrollo gate mutants run: %s", refusal.Reason)
		return 1
	}
	// A run for this repo is ALREADY going in almost every case that brings
	// somebody here: post-commit started one, and this is a session that
	// cannot see it. Starting a second measures the same mutants twice and
	// makes both slower; the lock would serialize them, so the waste would
	// just be quieter.
	if running := RunningMutantsJobs(j.Repo); len(running) > 0 {
		r := running[len(running)-1]
		logf(out, "aphrollo gate mutants run: a run for this repo is already going (%s, pid %d, since %s) — it writes the same receipt this one would",
			r.Branch, r.PID, r.Started.Format("15:04"))
		return 1
	}
	logf(out, "aphrollo: measuring %s (%s..%s) in %s", j.Branch, short(j.BaseSHA), short(j.Tip), j.Worktree)
	return mutantsForegroundFn(j, out)
}

// RunMutantsJobTo is the detached wrapper's body, writing to a named log.
// RunMutantsJob is the same thing against this process's own stdout, which the
// parent has already redirected to the job's log file.
func RunMutantsJobTo(jobPath string, log io.Writer) int {
	lowerOwnPriority()
	j, err := readMutantsJob(jobPath)
	if err != nil {
		// An unreadable job is an ERROR, not an empty result. Returning 0
		// here made a mistyped --job exit clean from the one command whose
		// entire purpose is to measure something.
		logf(log, "aphrollo gate mutants run: %v", err)
		logf(log, "aphrollo: --job addresses a job file written by the post-commit hook; to measure this checkout's own lane, pass no --job at all")
		return 2
	}
	return runMutantsJob(j, log)
}

// RunMutantsJob is the detached wrapper's entry point.
func RunMutantsJob(jobPath string) int { return RunMutantsJobTo(jobPath, os.Stdout) }
