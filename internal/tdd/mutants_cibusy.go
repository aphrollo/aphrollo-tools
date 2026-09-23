package tdd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A measurement shares this box with its self-hosted GitHub runners. One that
// overlapped two PRs' CI jobs reported `Killed: 14, Lived: 0 ... Timed out:
// 21`, while the same tree measured with no CI running caught 35 of 35.
// gremlins derives each mutant's timeout from how long its coverage run took,
// and cargo-mutants from its baseline, so load that arrives after that step
// turns healthy mutants into timeouts — which the gate then refuses as
// unmeasured.
//
// Raising the timeout multiplier would hide that and every real hang with it.
// Instead a measurement does not start while a runner job is busy here: it
// waits for the jobs to finish, printing what it waits for, and gives up
// waiting after mutantsCIWaitMax so a runner that is never idle cannot hold a
// merge forever. The CI jobs themselves take neither the box-wide
// mutation-run lock nor a build slot — the pipeline runs `go test` directly —
// so nothing else keeps the two apart.
//
// A runner job is its Runner.Worker process: the actions runner's listener is
// always running, and it spawns one Worker per job, which exits with the job.
// Detection is a read of /proc, no network and no GitHub API. A box without
// /proc (Windows, macOS) or without runners finds none and runs exactly as it
// always has.

// ciRunnerWorkerName is the executable the actions runner starts per job.
const ciRunnerWorkerName = "Runner.Worker"

var (
	// mutantsCIWaitMax bounds the wait. Measured on this box, a Pipeline run
	// takes 4-7 min and the nightly mutants job 10-11 min inside a 30 min job
	// timeout, so 15 min covers two back-to-back pipeline runs and still
	// leaves the nightly job, which waits on its sibling runners too, room to
	// finish. A var, not a const, only so a test can shrink it.
	mutantsCIWaitMax = 15 * time.Minute
	// mutantsCIWaitPoll is how often the wait looks again.
	mutantsCIWaitPoll = 10 * time.Second
	// mutantsCIWaitNoticeEvery is how often a continuing wait says so.
	mutantsCIWaitNoticeEvery = 60 * time.Second
)

// ciRunnerJobsFn answers the pids of the runner jobs busy on this box right
// now, leaving out any job this process runs inside. A seam, so no test
// depends on what the box running it has going on.
var ciRunnerJobsFn = func() []int { return busyCIRunnerJobs("/proc", os.Getpid()) }

// SetCIRunnerJobsForTest replaces the runner-job probe for one test and
// answers the restore. Exported because internal/cli measures through the
// same path, and a suite that ran inside a CI job beside a busy sibling
// runner must not wait on it.
func SetCIRunnerJobsForTest(fn func() []int) (restore func()) {
	prev := ciRunnerJobsFn
	ciRunnerJobsFn = fn
	return func() { ciRunnerJobsFn = prev }
}

// setCIRunnerWaitForTest shrinks the wait's poll interval and bound.
func setCIRunnerWaitForTest(poll, maxWait time.Duration) (restore func()) {
	prevPoll, prevMax := mutantsCIWaitPoll, mutantsCIWaitMax
	mutantsCIWaitPoll, mutantsCIWaitMax = poll, maxWait
	return func() { mutantsCIWaitPoll, mutantsCIWaitMax = prevPoll, prevMax }
}

// busyCIRunnerJobs scans procRoot for Runner.Worker processes, by the
// basename of argv[0] so a shell whose command line merely mentions the name
// does not count. A Worker that is an ancestor of self is left out: the
// nightly mutants job measures from inside a runner job, and waiting for
// itself to finish would wait out the whole bound for nothing. Unreadable
// entries are skipped; an unreadable procRoot is no runners.
func busyCIRunnerJobs(procRoot string, self int) []int {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	ancestors := procAncestors(procRoot, self)
	var busy []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || ancestors[pid] {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		argv0, _, _ := strings.Cut(string(cmdline), "\x00")
		if filepath.Base(argv0) == ciRunnerWorkerName {
			busy = append(busy, pid)
		}
	}
	sort.Ints(busy)
	return busy
}

// procAncestors is pid and every process above it, read from each one's
// stat. The parent pid is the second field after the parenthesised command
// name, which may itself hold spaces and parentheses, so the line is read
// from its LAST ')'.
func procAncestors(procRoot string, pid int) map[int]bool {
	seen := map[int]bool{}
	for pid > 1 && !seen[pid] {
		seen[pid] = true
		stat, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
		if err != nil {
			break
		}
		i := strings.LastIndexByte(string(stat), ')')
		if i < 0 {
			break
		}
		fields := strings.Fields(string(stat)[i+1:])
		if len(fields) < 2 {
			break
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			break
		}
		pid = ppid
	}
	return seen
}

// waitForCIRunnerJobs holds a measurement until no runner job is busy on this
// box, for at most mutantsCIWaitMax, and says what it is doing throughout. A
// box with no busy job returns at once and prints nothing. Called with the
// box-wide mutation-run lock already held, immediately before the tool
// starts, so nothing can slip in between the wait and the run's own timing
// step except a CI job that starts after it.
func waitForCIRunnerJobs(ctx context.Context, root string, log io.Writer) {
	busy := ciRunnerJobsFn()
	if len(busy) == 0 {
		return
	}
	start := time.Now()
	logf(log, "mutants: %s busy on this box — waiting up to %s for %s before measuring, "+
		"because load that starts after the run times its baseline turns healthy mutants into timeouts",
		ciRunnerJobsText(busy), mutantsCIWaitMax, pronounFor(busy))
	nextNotice := mutantsCIWaitNoticeEvery
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(mutantsCIWaitPoll):
		}
		waited := time.Since(start)
		busy = ciRunnerJobsFn()
		if len(busy) == 0 {
			logf(log, "mutants: CI runner jobs finished after %s — measuring", waited.Round(time.Second))
			return
		}
		if waited >= mutantsCIWaitMax {
			logf(log, "mutants: %s still busy after %s — measuring anyway; a timeout in this run may be the box's load, not a hang",
				ciRunnerJobsText(busy), waited.Round(time.Second))
			AppendGateLog("mutants", measureLogRoot(root), "mutants", "mutants-ci-busy", waited)
			return
		}
		if waited >= nextNotice {
			logf(log, "mutants: still waiting for %s (waited %s)", ciRunnerJobsText(busy), waited.Round(time.Second))
			nextNotice += mutantsCIWaitNoticeEvery
		}
	}
}

// ciRunnerJobsText names the busy jobs by count and pid, the pid being what
// `ps` on the box answers to.
func ciRunnerJobsText(pids []int) string {
	parts := make([]string, len(pids))
	for i, p := range pids {
		parts[i] = strconv.Itoa(p)
	}
	return fmt.Sprintf("%d CI runner job%s (%s pid %s)", len(pids), plural(len(pids)), ciRunnerWorkerName,
		strings.Join(parts, ", "))
}

// pronounFor is "it" or "them", so the wait line reads as a sentence.
func pronounFor(pids []int) string {
	if len(pids) == 1 {
		return "it"
	}
	return "them"
}
