package tdd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// `aphrollo gate mutants status` answers "what is the state right now" and
// returns at once — the shape a statusline and every other point-in-time
// caller needs. Nothing could SUBSCRIBE to a run, so every consumer that
// wanted to know when one finished built its own poll loop around `status`,
// and each one reinvented the same two mistakes (issue #524):
//
//	liveness checked with `kill -0 <pid>` under Git Bash, which reports every
//	   live NATIVE process as gone — MSYS keeps its own pid namespace.
//	   jobProcessLives (mutants_job.go), via pidRunningFn, is the platform-
//	   correct check this file reuses rather than re-deriving.
//	a filter that matches only the success marker, silent through a crash, a
//	   kill, or a hang — indistinguishable from "still running".
//
// `watch` is a SECOND verb over the same job record, never a flag on
// `status`: status has callers (the statusline) that depend on it returning
// immediately, and making it block would break them.

// Exit codes for `aphrollo gate mutants watch`. Terminal covers every
// ending, not just success, so a caller never has to infer an outcome from
// silence.
const (
	// ExitMutantsWatchDone: a receipt for this tree was written.
	ExitMutantsWatchDone = 0
	// ExitMutantsWatchError: the checkout itself could not be read, or the
	// named --job file could not be.
	ExitMutantsWatchError = 1
	// ExitMutantsWatchUsage: bad flags.
	ExitMutantsWatchUsage = 2
	// ExitMutantsWatchNone: no run has ever been started for this tree, and
	// none is running now — there is nothing to watch.
	ExitMutantsWatchNone = 3
	// ExitMutantsWatchDied: the run ended with a recorded exit code and no
	// receipt.
	ExitMutantsWatchDied = 4
	// ExitMutantsWatchKilled: the run's own process is gone with NO recorded
	// exit and no receipt — nothing in this box's own lifecycle wrote that
	// down, which is what an external kill looks like from here.
	ExitMutantsWatchKilled = 5
	// ExitMutantsWatchStalled: the run's process is still alive, but neither
	// its log nor its mutants.out gained anything for longer than
	// mutantsWatchStallTimeout.
	ExitMutantsWatchStalled = 6
)

// mutantsWatchPrefix mirrors mutantsStatusLinePrefix: one function renders
// every line this verb prints, so a transition and the final terminal line
// can never drift into two different voices.
const mutantsWatchPrefix = "aphrollo gate mutants watch: "

// mutantsWatchPollInterval is how often watch re-checks the job's own files
// between transition lines. A var so a test can shrink it rather than wait
// on a real mutation run.
var mutantsWatchPollInterval = 2 * time.Second

// mutantsWatchStallTimeout is how long a live process may go with NEITHER
// its log NOR its mutants.out gaining anything before watch calls it
// stalled. This is NOT cargo-mutants' own per-mutant `--timeout` — a mutant
// that times out still reaches a TIMEOUT verdict and the run keeps going,
// which this file sees as ordinary progress. This is the watcher's own
// bound on the RUN making no progress AT ALL, the case a per-mutant timeout
// never catches because it never even gets there.
var mutantsWatchStallTimeout = 30 * time.Minute

// mutantsWatchPolledFn is a test-only synchronization hook: called once per
// poll, right after mutantsWatchStep and before the between-polls sleep.
// nil (its zero value, and every production run's value) does nothing. A
// test sets it to land a receipt or a death record exactly after the loop
// has proven it polled at least once, instead of a real-time sleep racing a
// background goroutine.
var mutantsWatchPolledFn func()

func callMutantsWatchPolledFn() {
	if mutantsWatchPolledFn != nil {
		mutantsWatchPolledFn()
	}
}

// WatchMutantsHere is `aphrollo gate mutants watch [--job <path>]`. jobPath
// names a job file the way `run --job` addresses one; empty falls back to
// the tree the checkout at dir stands in — the same default ComputeMutantsStatus
// uses. It blocks until that tree reaches a terminal state (or already has),
// printing one line per transition, and returns the exit code for that
// state.
func WatchMutantsHere(dir, jobPath string, out io.Writer) int {
	if jobPath != "" {
		j, err := readMutantsJob(jobPath)
		if err != nil {
			fmt.Fprintf(out, "%s%v\n", mutantsWatchPrefix, err)
			return ExitMutantsWatchError
		}
		return watchMutantsTip(j.Repo, j.TipTree, &j, out)
	}
	root := RepoRoot(dir)
	if root == "" {
		where := dir
		if abs, err := filepath.Abs(dir); err == nil {
			where = abs
		}
		fmt.Fprintf(out, "%s%s is not inside a git repository\n", mutantsWatchPrefix, where)
		return ExitMutantsWatchError
	}
	tipTree := gitOut(root, "rev-parse", "HEAD:")
	if tipTree == "" {
		fmt.Fprintf(out, "%scould not resolve %s's own tree — is there a commit on HEAD?\n", mutantsWatchPrefix, root)
		return ExitMutantsWatchError
	}
	return watchMutantsTip(commonGitDir(root), tipTree, nil, out)
}

// watchMutantsTip is WatchMutantsHere's body once repo/tipTree are resolved.
// known is the exact job record when the caller named one with --job; nil
// means "find whatever is running for this tree", the matchingRunningJob
// lookup ComputeMutantsStatus's own MutantsRunGoing case already makes.
func watchMutantsTip(repo, tipTree string, known *MutantsJob, out io.Writer) int {
	if r, ok := readReceiptFile(MutationReceiptPathFor(tipTree)); ok {
		return printMutantsWatchDone(r, out)
	}
	if d, ok := loadMutantsDeath(tipTree); ok {
		return printMutantsWatchDied(d, out)
	}
	j := known
	if j == nil {
		if running, ok := matchingRunningJob(repo, tipTree); ok {
			j = &running
		}
	}
	if j == nil {
		fmt.Fprintf(out, "%sno run has been started for tree %s — nothing to watch\n", mutantsWatchPrefix, short(tipTree))
		return ExitMutantsWatchNone
	}
	return runMutantsWatchLoop(*j, out)
}

// mutantsWatchState is what the loop remembers between polls: how far into
// the job's log it has already read, and which mutants it has already
// reported on (by identity, so a mutant already printed is never printed
// twice even if mutants.out is re-read whole on every poll).
type mutantsWatchState struct {
	logOffset int64
	seen      map[mutantKey]bool
	total     int
}

// runMutantsWatchLoop polls j until it reaches a terminal state, printing one
// line per transition as it goes. The three terminal checks run BEFORE every
// poll, in the same order watchMutantsTip already checked once: a receipt
// or a death record can land between two polls, and the liveness check is
// what turns a silent external kill into a line instead of a hang.
func runMutantsWatchLoop(j MutantsJob, out io.Writer) int {
	st := &mutantsWatchState{seen: map[mutantKey]bool{}}
	lastProgress := time.Now()
	for {
		if r, ok := readReceiptFile(MutationReceiptPathFor(j.TipTree)); ok {
			return printMutantsWatchDone(r, out)
		}
		if d, ok := loadMutantsDeath(j.TipTree); ok {
			return printMutantsWatchDied(d, out)
		}
		if !jobProcessLives(j) {
			fmt.Fprintf(out, "%sno receipt — the run's process (pid %d) is gone with no recorded exit; it was likely killed\n",
				mutantsWatchPrefix, j.PID)
			return ExitMutantsWatchKilled
		}

		lines, advanced := mutantsWatchStep(j, st)
		for _, line := range lines {
			fmt.Fprintln(out, mutantsWatchPrefix+line)
		}
		callMutantsWatchPolledFn()
		if advanced {
			lastProgress = time.Now()
		} else if time.Since(lastProgress) > mutantsWatchStallTimeout {
			fmt.Fprintf(out, "%sno receipt — no progress for over %s; the run appears stalled\n",
				mutantsWatchPrefix, mutantsWatchStallTimeout)
			return ExitMutantsWatchStalled
		}
		time.Sleep(mutantsWatchPollInterval)
	}
}

func printMutantsWatchDone(r MutationReceipt, out io.Writer) int {
	fmt.Fprintf(out, "%sreceipt written — %s\n", mutantsWatchPrefix, MutationReceiptPathFor(r.TipTree))
	return ExitMutantsWatchDone
}

func printMutantsWatchDied(d MutantsDeath, out io.Writer) int {
	fmt.Fprintf(out, "%sno receipt — %s\n", mutantsWatchPrefix, mutantsDeathRemedyLine(d))
	return ExitMutantsWatchDied
}

// mutantsFoundLineRe matches cargo-mutants' own "Found N mutants to test" —
// the stable, undocumented-flag-free stdout line that names the run's total,
// pasted verbatim from a real 27.1.0 run (mutants_argswarning_test.go already
// pins the same text).
var mutantsFoundLineRe = regexp.MustCompile(`^Found (\d+) mutants? to test$`)

// parseMutantsFoundLine reads the total mutant count from one of cargo-mutants'
// own stdout lines.
func parseMutantsFoundLine(line string) (int, bool) {
	m := mutantsFoundLineRe.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// isUnmutatedBaselineLine matches cargo-mutants' own baseline-result line
// ("ok       Unmutated baseline" or, with timings on, "ok       Unmutated
// baseline in 12s build + 3s test"). cargo-mutants reports the baseline as
// ONE combined event on its stable stdout surface — the build/test split
// only exists in its internal debug tracing, which is not a surface this
// file depends on — so watch emits its "baseline build done" and "baseline
// test done" lines together the moment this appears: both are true by then,
// even though only one line ever said so.
func isUnmutatedBaselineLine(line string) bool {
	return strings.Contains(line, "Unmutated baseline")
}

// mutantsWatchStep reads whatever newly appeared since the last call and
// returns the transition lines to print, in a fixed order: whatever the
// job's log gained, then whatever mutants.out gained. advanced is true when
// EITHER source moved — what resets the stall clock.
func mutantsWatchStep(j MutantsJob, st *mutantsWatchState) (lines []string, advanced bool) {
	if logLines, err := tailNewLines(j.Log, &st.logOffset); err == nil {
		for _, line := range logLines {
			advanced = true
			if n, ok := parseMutantsFoundLine(line); ok {
				st.total = n
				continue
			}
			if isUnmutatedBaselineLine(line) {
				lines = append(lines, "baseline build done", "baseline test done")
			}
		}
	}
	outcomes := readMutantsOut(j.Worktree)
	sortOutcomes(outcomes)
	for _, m := range outcomes {
		k := m.key()
		if st.seen[k] {
			continue
		}
		st.seen[k] = true
		advanced = true
		lines = append(lines, mutantsWatchProgressLine(len(st.seen), st.total, m))
		if m.Status == "missed" {
			lines = append(lines, "survivor: "+mutantsWatchName(m))
		}
	}
	return lines, advanced
}

func mutantsWatchProgressLine(n, total int, m MutantOutcome) string {
	totalStr := "?"
	if total > 0 {
		totalStr = strconv.Itoa(total)
	}
	return fmt.Sprintf("mutant %d of %s: %s %s", n, totalStr, m.Status, mutantsWatchName(m))
}

// mutantsWatchName is the tool's own spelling of m when it has one (the
// verbatim mutants.out line), rebuilt from its parts otherwise — the same
// fallback mutantNames (mutants_run.go) already uses.
func mutantsWatchName(m MutantOutcome) string {
	if m.Name != "" {
		return m.Name
	}
	return mutantLineOf(m.File, m.Line, m.Col, m.Mutation)
}

// tailNewLines reads every COMPLETE line appended to path since *offset,
// advancing *offset past only what it actually returns. A trailing partial
// line — the file still being written mid-line at poll time — is left
// unconsumed: advancing offset past it would drop the rest of that line the
// next time it is read, since the file is never re-read from its start.
func tailNewLines(path string, offset *int64) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(*offset, io.SeekStart); err != nil {
		return nil, err
	}
	r := bufio.NewReader(f)
	pos := *offset
	var lines []string
	for {
		line, err := r.ReadString('\n')
		if strings.HasSuffix(line, "\n") {
			pos += int64(len(line))
			lines = append(lines, strings.TrimRight(line, "\r\n"))
		}
		if err != nil {
			break
		}
	}
	*offset = pos
	return lines, nil
}
