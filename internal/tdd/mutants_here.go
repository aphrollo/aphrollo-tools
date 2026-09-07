package tdd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	if j.Worktree == "" || j.TargetDir == "" {
		// MutantsRootDir could not resolve root's primary checkout — root's own
		// --git-common-dir failed just now, moments after RepoRoot succeeded on
		// the same directory. Refuse rather than guess where the mutants area
		// lives (issue #515).
		return MutantsJob{}, mutantsRefusal{
			Reason: fmt.Sprintf("could not resolve %s's primary checkout to place the mutants area — git could not answer --git-common-dir there", root),
		}, nil
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
	// Chosen and CLAIMED as one locked step, immediately before the
	// synchronous, possibly expensive prepare below: a still-running job for
	// this SAME lane already owning j.Worktree would otherwise see
	// prepareMutantsWorktree run a `git reset --hard` (or, for Go, an
	// os.RemoveAll plus reclone) against the tree that job's producer is
	// currently mutating and testing in. "One warm directory per lane" and
	// "never cancel the run it supersedes" cannot both hold across two
	// overlapping commits (issue #283); this keeps the second and pays a
	// cold worktree for the overlap. Claiming right here, rather than
	// deciding earlier and claiming only once this whole function returns,
	// is what closes issue #405: the choice and the claim share one lock, so
	// no concurrent caller's own choice can land inside this call's window.
	j.Worktree = chooseMutantsWorktree(j.Repo, j.Worktree, j.TipTree)
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
	// Only a run for THIS TREE is duplicate work. A receipt is keyed on the
	// tip tree (MutationReceiptPathFor), so a job measuring another lane's
	// tree writes a different file and answers a different question — this
	// guard used to compare the REPO, which made one lane's run refuse every
	// other lane in the same checkout for as long as it lasted, with a
	// message claiming the two would write the same receipt. They would not.
	//
	// Two lanes measuring different trees at once is not waste and needs no
	// guard here: the box-wide lock inside the producer call already
	// serializes them, and it QUEUES rather than refusing, so the second lane
	// keeps its place instead of being told to go away.
	//
	// r.Worktree == j.Worktree AND r.PID == os.Getpid() skips exactly one
	// entry: buildMutantsJob's own chooseMutantsWorktree (issue #405)
	// already registered a reservation for j's OWN worktree, under THIS
	// process, before this function ever gets to ask "is anything else
	// measuring this tree" — without the skip every hand-typed run found
	// itself in the registry and refused itself as a duplicate. Worktree
	// name alone is not proof of that: chooseMutantsWorktree now checks
	// every candidate it hands out precisely so two DIFFERENT processes
	// can never be given the same name (issue #436) — but trusting the
	// name alone here would still treat a third caller who somehow landed
	// on this same worktree as this call's own reservation instead of the
	// duplicate it actually is.
	for _, r := range RunningMutantsJobs(j.Repo) {
		if r.Worktree == j.Worktree && r.PID == os.Getpid() {
			continue
		}
		if !strings.EqualFold(r.TipTree, j.TipTree) {
			continue
		}
		logf(out, "aphrollo gate mutants run: a run for this exact tree is already going (%s, pid %d, since %s) — it writes the receipt this one would",
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
