package tdd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// A session that wants to know whether ITS OWN mutation receipt is done used
// to hand-roll a poll loop over the receipt path template, the tree-keyed
// filename, the sibling died.<tip>.json, and a fixed timeout — every one of
// those a private detail nothing here ever promised to keep stable (issue
// #272). ComputeMutantsStatus answers the same question through the exact
// calls the merge gate already makes (RunningMutantsJobs, loadMutantsDeath,
// MutationReceiptPathFor/readReceiptFile): the two can never disagree about
// the same tree, because they read the same files the same way.

// MutantsRunState is what a mutation run for one tree is doing right now.
// Exactly one of these is true of any (repo, tree) pair at a given instant.
type MutantsRunState int

const (
	// MutantsRunNone: nothing has ever measured this tree — no receipt, no
	// running job, no death record.
	MutantsRunNone MutantsRunState = iota
	// MutantsRunGoing: a job for this repo is alive. WaitingOnLock says
	// whether it is queued behind the box-wide mutation-run lock rather than
	// actually measuring.
	MutantsRunGoing
	// MutantsRunDied: the run ended without ever writing a receipt.
	MutantsRunDied
	// MutantsRunDone: a receipt for this exact tree is on disk.
	MutantsRunDone
)

func (s MutantsRunState) String() string {
	switch s {
	case MutantsRunNone:
		return "none"
	case MutantsRunGoing:
		return "going"
	case MutantsRunDied:
		return "died"
	case MutantsRunDone:
		return "done"
	default:
		return "unknown"
	}
}

// MutantsStatusReport is the answer for exactly the checkout it was computed
// from. Only the fields the State names are meaningful; the rest are zero.
type MutantsStatusReport struct {
	RepoRoot string
	Repo     string
	Branch   string
	TipTree  string

	State MutantsRunState

	// MutantsRunGoing
	JobPID        int
	JobStarted    time.Time
	WaitingOnLock bool
	// LockHolder names who holds the box-wide mutation-run lock instead,
	// when WaitingOnLock is true — "" when the holder could not be named
	// (the owner file is best-effort, same as everywhere else it is read).
	LockHolder string

	// MutantsRunDied
	DiedExit int
	DiedAt   time.Time
	DiedLog  string

	// MutantsRunDone
	Receipt MutationReceipt
}

// matchingRunningJob returns the running mutation job actually measuring
// tipTree, if any. RunningMutantsJobs(repo) holds every LANE's live job
// sharing this repo's common git dir — taking "the last one" (as both this
// function's callers used to) named whichever lane's run happened to be
// newest in the registry, not the one that would write THIS tree's receipt
// (issue #431). RunningMutantsJobs is already newest-first, so the first
// TipTree match is the newest job actually for tipTree — the same rule
// RunMutantsHere applies (mutants_here.go) before refusing a duplicate run.
func matchingRunningJob(repo, tipTree string) (MutantsJob, bool) {
	for _, j := range RunningMutantsJobs(repo) {
		if strings.EqualFold(j.TipTree, tipTree) {
			return j, true
		}
	}
	return MutantsJob{}, false
}

// ComputeMutantsStatus answers for the checkout dir stands in, never for any
// other lane: it names dir's own HEAD tree and looks up only that tree's own
// receipt, job and death records.
func ComputeMutantsStatus(dir string) (MutantsStatusReport, error) {
	root := RepoRoot(dir)
	if root == "" {
		where := dir
		if abs, err := filepath.Abs(dir); err == nil {
			where = abs
		}
		return MutantsStatusReport{}, fmt.Errorf("%s is not inside a git repository", where)
	}
	tipTree := gitOut(root, "rev-parse", "HEAD:")
	if tipTree == "" {
		return MutantsStatusReport{}, fmt.Errorf("could not resolve %s's own tree — is there a commit on HEAD?", root)
	}
	branch := gitOut(root, "rev-parse", "--abbrev-ref", "HEAD")
	repo := commonGitDir(root)
	rep := MutantsStatusReport{RepoRoot: root, Repo: repo, Branch: branch, TipTree: tipTree}

	// The receipt is read FIRST, exactly as checkMutationReceipt does: once
	// a tree's proof exists, a stale running-job record must never mask it.
	if r, ok := readReceiptFile(MutationReceiptPathFor(tipTree)); ok {
		rep.State = MutantsRunDone
		rep.Receipt = r
		return rep, nil
	}
	// Same lookup, matchingRunningJob, as missingReceiptRemedy — the merge
	// gate's own answer for "no receipt, is something running" — so the two
	// never name a different job for the same tree.
	if j, ok := matchingRunningJob(repo, tipTree); ok {
		rep.State = MutantsRunGoing
		rep.JobPID = j.PID
		rep.JobStarted = j.Started
		if owner, held := readBuildLockOwnerAt(mutantsRunLockOwnerPath()); held && owner.PID != j.PID {
			rep.WaitingOnLock = true
			rep.LockHolder = describeOwner(owner)
		}
		return rep, nil
	}
	if d, ok := loadMutantsDeath(tipTree); ok {
		rep.State = MutantsRunDied
		rep.DiedExit = d.Exit
		rep.DiedAt = d.At
		rep.DiedLog = mutantsDeathLogPath(d)
		return rep, nil
	}
	rep.State = MutantsRunNone
	return rep, nil
}

// mutantsDeathLogPath picks which of a death's two logs actually explains
// it, mirroring missingReceiptRemedy's own pick exactly: stderr wins unless
// it came up empty and stdout is where the tail actually came from.
func mutantsDeathLogPath(d MutantsDeath) string {
	logPath := d.ErrLog
	if len(d.Tail) > 0 && stderrTail(d.ErrLog, 1) == nil && d.Log != "" {
		logPath = d.Log
	}
	return logPath
}

// receiptWouldMerge judges a receipt the same three ways the merge gate's
// checkMutationReceipt does — worktree_dirty, verdict, unaccepted survivors,
// timeouts — without repeating its repo-identity or base-sha checks, which
// are about which MERGE the receipt is for rather than what it says. "" ok
// means the merge gate has nothing further to say about the counts alone.
func receiptWouldMerge(r MutationReceipt) (bool, string) {
	if r.WorktreeDirty {
		return false, "worktree_dirty: it measured uncommitted work"
	}
	if r.Verdict != receiptVerdictPass {
		return false, fmt.Sprintf("verdict %q", r.Verdict)
	}
	if len(r.Unaccepted) > 0 {
		return false, fmt.Sprintf("%d unaccepted survivor(s), starting with %s", len(r.Unaccepted), firstUnaccepted(r.Unaccepted))
	}
	if r.Timeout > 0 {
		return false, mutantsTimedOutLine(r.Timeout)
	}
	return true, ""
}

// Exit codes for `aphrollo gate mutants status`, distinguishing every state
// a script needs to tell apart. Documented verbatim in mutantsUsage and in
// docs/mutation-runner.md — keep the three in agreement.
const (
	// ExitMutantsStatusPass: a receipt for this tree exists and would merge
	// (verdict pass, no unaccepted survivors, no timeouts).
	ExitMutantsStatusPass = 0
	// ExitMutantsStatusError: the checkout itself could not be read (not a
	// git repository, or HEAD names no tree).
	ExitMutantsStatusError = 1
	// ExitMutantsStatusUsage: bad flags.
	ExitMutantsStatusUsage = 2
	// ExitMutantsStatusNone: no run has ever been started for this tree.
	ExitMutantsStatusNone = 3
	// ExitMutantsStatusGoing: a run is going right now (`status` only —
	// `--wait` never returns this code, since it blocks until the state is
	// terminal).
	ExitMutantsStatusGoing = 4
	// ExitMutantsStatusDied: the run ended without ever writing a receipt.
	ExitMutantsStatusDied = 5
	// ExitMutantsStatusFail: a receipt for this tree exists but would NOT
	// merge (bad verdict, unaccepted survivors, or a timeout).
	ExitMutantsStatusFail = 6
)

// mutantsStatusLinePrefix is FormatMutantsStatus's own prefix, exported as a
// constant (not just a literal inside that function) so a caller that wants
// its BODY — missingReceiptRemedy embeds the MutantsRunGoing line rather than
// composing a second description of the same state — can strip it without
// guessing at the string FormatMutantsStatus happens to use today.
const mutantsStatusLinePrefix = "aphrollo gate mutants status: "

// FormatMutantsStatus renders rep the way `aphrollo gate mutants status`
// prints it, and picks the exit code that goes with it — the one function
// both `status` and `status --wait` print through, so the two can never say
// the same state two different ways.
func FormatMutantsStatus(rep MutantsStatusReport) (string, int) {
	const prefix = mutantsStatusLinePrefix
	switch rep.State {
	case MutantsRunNone:
		return fmt.Sprintf("%sno run has been started for %s (tree %s)", prefix, rep.Branch, short(rep.TipTree)), ExitMutantsStatusNone
	case MutantsRunGoing:
		if rep.WaitingOnLock {
			holder := rep.LockHolder
			if holder == "" {
				holder = "another mutation run (holder unknown)"
			}
			return fmt.Sprintf("%s%s (pid %d, started %s) is queued behind %s for the box-wide mutation-run lock",
				prefix, rep.Branch, rep.JobPID, rep.JobStarted.Format("15:04:05"), holder), ExitMutantsStatusGoing
		}
		return fmt.Sprintf("%s%s (pid %d, started %s) is measuring", prefix, rep.Branch, rep.JobPID, rep.JobStarted.Format("15:04:05")), ExitMutantsStatusGoing
	case MutantsRunDied:
		return fmt.Sprintf("%sthe run for %s (tree %s) died (exit %d) at %s — see %s",
			prefix, rep.Branch, short(rep.TipTree), rep.DiedExit, rep.DiedAt.Format("15:04:05"), rep.DiedLog), ExitMutantsStatusDied
	case MutantsRunDone:
		r := rep.Receipt
		line := fmt.Sprintf("%sreceipt for %s (tree %s) — verdict %s, %d mutant(s) (caught %d, timeout %d, unviable %d, not_covered %d, excluded %d), %d accepted, %d unaccepted",
			prefix, rep.Branch, short(rep.TipTree), r.Verdict, r.MutantsTotal, r.Caught, r.Timeout, r.Unviable, r.NotCovered, r.Excluded, r.Accepted, len(r.Unaccepted))
		if ok, reason := receiptWouldMerge(r); !ok {
			return line + " — would not merge: " + reason, ExitMutantsStatusFail
		}
		return line, ExitMutantsStatusPass
	default:
		return prefix + "unknown state", ExitMutantsStatusError
	}
}

// waitForPIDExitFn blocks until pid's process has exited. A var so a test can
// prove WaitMutantsStatus's blocking behaviour without needing an actual
// hours-long mutation run to wait for; production always uses
// waitForPIDExit, the platform-specific real wait.
var waitForPIDExitFn = waitForPIDExit

// WaitMutantsStatus is `status --wait`: it blocks the CALLING PROCESS on the
// running job's own pid — never a poll loop sampling a file — then re-reads
// and returns the same report Status would. A tree that is already at a
// terminal state (none, died, done) returns at once: there is nothing to
// wait for.
func WaitMutantsStatus(dir string) (MutantsStatusReport, error) {
	rep, err := ComputeMutantsStatus(dir)
	if err != nil || rep.State != MutantsRunGoing {
		return rep, err
	}
	waitForPIDExitFn(rep.JobPID)
	return ComputeMutantsStatus(dir)
}
