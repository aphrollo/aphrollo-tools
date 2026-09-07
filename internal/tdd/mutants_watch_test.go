package tdd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A file that is still being appended to mid-line must never lose that
// line: tailNewLines is polled while cargo-mutants' own process may be
// mid-write, and advancing the offset past a partial line would drop the
// rest of it forever, since the file is never re-read from its start.
func TestTailNewLines_HoldsBackAPartialLineUntilItIsTerminated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.log")
	if err := os.WriteFile(path, []byte("first line\nsecond li"), 0o600); err != nil {
		t.Fatal(err)
	}
	var offset int64
	got, err := tailNewLines(path, &offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "first line" {
		t.Fatalf("got %v, want only the one complete line", got)
	}

	// The writer finishes the second line and starts a third.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("ne\nthird\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err = tailNewLines(path, &offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "second line" || got[1] != "third" {
		t.Fatalf("got %v, want the completed second line and the new third line, in order", got)
	}
}

// Pasted verbatim from the real cargo-mutants 27.1.0 run this task probed
// (`cargo-mutants mutants` over a two-mutant scratch crate): the exact
// stdout lines watch's transition parser reads.
const (
	realFoundLine    = "Found 6 mutants to test"
	realBaselineLine = "ok       Unmutated baseline in 0s build + 0s test"
)

func TestParseMutantsFoundLine_ReadsTheCountFromCargoMutantsOwnLine(t *testing.T) {
	n, ok := parseMutantsFoundLine(realFoundLine)
	if !ok || n != 6 {
		t.Fatalf("parseMutantsFoundLine(%q) = %d, %v, want 6, true", realFoundLine, n, ok)
	}
	if _, ok := parseMutantsFoundLine("MISSED   src/main.rs:6:5: replace main with () in 0s build + 0s test"); ok {
		t.Fatal("an unrelated line matched the found-count pattern")
	}
}

func TestIsUnmutatedBaselineLine_MatchesCargoMutantsOwnLine(t *testing.T) {
	if !isUnmutatedBaselineLine(realBaselineLine) {
		t.Fatalf("isUnmutatedBaselineLine(%q) = false, want true", realBaselineLine)
	}
	if isUnmutatedBaselineLine(realFoundLine) {
		t.Fatal("the found-count line must not read as the baseline line")
	}
}

// mutants.out grows one status file at a time as cargo-mutants reaches each
// verdict. A poll mid-run must report only what is NEW since the last one,
// in the same order sortOutcomes would put a receipt in, and a survivor
// (a MISSED mutant) gets its own extra line beside the ordinary progress one.
func TestMutantsWatchStep_ReportsNewMutantsOnceAndFlagsSurvivors(t *testing.T) {
	worktree := t.TempDir()
	outDir := filepath.Join(worktree, "mutants.out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(outDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("caught.txt", strings.Join(realCaughtLines, "\n")+"\n")

	j := MutantsJob{Worktree: worktree, Log: filepath.Join(t.TempDir(), "job.log")}
	st := &mutantsWatchState{seen: map[mutantKey]bool{}}

	lines, advanced := mutantsWatchStep(j, st)
	if !advanced {
		t.Fatal("advanced = false, want true — caught.txt gained lines")
	}
	if len(lines) != len(realCaughtLines) {
		t.Fatalf("lines = %v, want one progress line per caught mutant (%d)", lines, len(realCaughtLines))
	}
	for _, l := range lines {
		if strings.Contains(l, "survivor:") {
			t.Fatalf("a caught mutant must never be reported as a survivor: %q", l)
		}
	}

	// A second poll with nothing new must report nothing, not re-announce the
	// same mutants.
	lines, advanced = mutantsWatchStep(j, st)
	if advanced || len(lines) != 0 {
		t.Fatalf("second poll with no change: lines = %v, advanced = %v, want none", lines, advanced)
	}

	// Now a survivor lands.
	write("missed.txt", strings.Join(realMissedLines, "\n")+"\n")
	lines, advanced = mutantsWatchStep(j, st)
	if !advanced {
		t.Fatal("advanced = false, want true — missed.txt gained a line")
	}
	var sawProgress, sawSurvivor bool
	for _, l := range lines {
		if strings.HasPrefix(l, "mutant ") && strings.Contains(l, "missed") {
			sawProgress = true
		}
		if strings.HasPrefix(l, "survivor: ") && strings.Contains(l, realMissedLines[0]) {
			sawSurvivor = true
		}
	}
	if !sawProgress || !sawSurvivor {
		t.Fatalf("lines = %v, want both a %q progress line and a survivor line for the new MISSED mutant", lines, "missed")
	}
}

// The receipt is read BEFORE anything about the job record — mirroring
// ComputeMutantsStatus's own precedence — so watch never blocks polling a
// run whose proof already exists.
func TestWatchMutantsHere_ReportsDoneWhenAReceiptAlreadyExists(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := initRepoWithCommit(t, "hello")
	tip := gitOut(root, "rev-parse", "HEAD:")
	writeReceiptFile(MutationReceiptPathFor(tip), MutationReceipt{TipTree: tip, Verdict: "pass", FinishedAt: time.Now()})

	var buf bytes.Buffer
	code := WatchMutantsHere(root, "", &buf)
	if code != ExitMutantsWatchDone {
		t.Fatalf("code = %d, want ExitMutantsWatchDone (%d); output: %s", code, ExitMutantsWatchDone, buf.String())
	}
	if !strings.Contains(buf.String(), MutationReceiptPathFor(tip)) {
		t.Fatalf("output = %q, want it to name the receipt path", buf.String())
	}
}

// A run that ended without ever writing a receipt is DIED, naming the exit
// code the same way status already does.
func TestWatchMutantsHere_ReportsDiedWhenTheRunEndedWithNoReceipt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := initRepoWithCommit(t, "hello")
	tip := gitOut(root, "rev-parse", "HEAD:")
	errLog := filepath.Join(t.TempDir(), "run.err")
	if err := os.WriteFile(errLog, []byte("boom\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	j := MutantsJob{Repo: commonGitDir(root), TipTree: tip, ErrLog: errLog}
	recordMutantsDeath(j, 17, mutantsDeathTail(j))

	var buf bytes.Buffer
	code := WatchMutantsHere(root, "", &buf)
	if code != ExitMutantsWatchDied {
		t.Fatalf("code = %d, want ExitMutantsWatchDied (%d); output: %s", code, ExitMutantsWatchDied, buf.String())
	}
	if !strings.Contains(buf.String(), "died (exit 17)") {
		t.Fatalf("output = %q, want it to name the exit code", buf.String())
	}
}

// Nothing has ever measured this tree, and nothing is running for it now:
// there is nothing to block on.
func TestWatchMutantsHere_ReportsNoneWhenNothingHasEverBeenStarted(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := initRepoWithCommit(t, "hello")

	var buf bytes.Buffer
	code := WatchMutantsHere(root, "", &buf)
	if code != ExitMutantsWatchNone {
		t.Fatalf("code = %d, want ExitMutantsWatchNone (%d); output: %s", code, ExitMutantsWatchNone, buf.String())
	}
}

// The platform note issue #524 raises directly: liveness must be judged by
// jobProcessLives (pidRunningFn), never a bare kill-0-style check that would
// report every live native process gone under Git Bash's MSYS pid
// namespace. This is one half of a negative control: a pid the stub reports
// GONE must read as killed even though nothing ever recorded a death. The
// companion test below proves the other half with a genuinely alive pid, so
// a liveness check that only ever answers one way fails one of the two.
//
// Addressed with --job rather than the checkout default: the default path
// resolves a running job through matchingRunningJob, which already filters
// out a dead pid before watch ever sees it (the registry's own liveness
// check, mutants_job.go's liveJobsAt) — so a pid reported dead from the
// start reads as "no run has been started", not "killed", by design. --job
// reads the job file directly, with no such pre-filter, which is the one
// path that can actually reach the loop's own liveness check with a pid
// already gone.
func TestWatchMutantsHere_ReportsKilledWhenTheProcessIsGoneWithNoDeathRecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const tip = "cafecafecafecafecafecafecafecafecafecafe"
	const fakePID = 999999
	jobPath := filepath.Join(t.TempDir(), "job.json")
	if err := writeMutantsJobFile(jobPath, MutantsJob{Repo: "some-repo", TipTree: tip, Worktree: t.TempDir(), Tip: tip, PID: fakePID}); err != nil {
		t.Fatal(err)
	}

	prev := pidRunningFn
	pidRunningFn = func(pid int) bool { return false }
	t.Cleanup(func() { pidRunningFn = prev })

	// Milliseconds, not the 30-minute default: a mutation that turned the
	// liveness check into a no-op would otherwise only surface as this
	// test's whole binary getting killed by the outer timeout (CI's 600s,
	// or a bare `go test`'s 10-minute default) rather than as a clean,
	// single-test failure — the same reasoning the stalled test above
	// already applies to mutantsWatchStallTimeout.
	prevPoll, prevStall := mutantsWatchPollInterval, mutantsWatchStallTimeout
	mutantsWatchPollInterval = time.Millisecond
	mutantsWatchStallTimeout = 20 * time.Millisecond
	t.Cleanup(func() { mutantsWatchPollInterval, mutantsWatchStallTimeout = prevPoll, prevStall })

	var buf bytes.Buffer
	code := WatchMutantsHere(t.TempDir(), jobPath, &buf)
	if code != ExitMutantsWatchKilled {
		t.Fatalf("code = %d, want ExitMutantsWatchKilled (%d); output: %s", code, ExitMutantsWatchKilled, buf.String())
	}
	if !strings.Contains(buf.String(), "killed") {
		t.Fatalf("output = %q, want it to say the process was likely killed", buf.String())
	}
}

// The negative control's other half: a genuinely alive process (this test's
// own pid, checked for real — no pidRunningFn stub) must not be read as
// killed. Combined with the test above, a liveness check that only ever
// answers "alive" fails that one, and one that only ever answers "gone"
// fails this one — only jobProcessLives' real, platform-correct contract
// passes both. mutantsWatchPolledFn lands the receipt deterministically
// right after the loop has proven it polled (and so checked liveness) at
// least once, with no real-time sleep or background goroutine race.
func TestWatchMutantsHere_DoesNotReportKilledWhileTheProcessIsStillAlive(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := initRepoWithCommit(t, "hello")
	tip := gitOut(root, "rev-parse", "HEAD:")
	worktree := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "job.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	saveMutantsJob(MutantsJob{Repo: commonGitDir(root), Branch: "main", TipTree: tip,
		PID: os.Getpid(), Started: time.Now(), Worktree: worktree, Log: logPath})

	prevPoll := mutantsWatchPollInterval
	mutantsWatchPollInterval = time.Millisecond
	prevHook := mutantsWatchPolledFn
	mutantsWatchPolledFn = func() {
		writeReceiptFile(MutationReceiptPathFor(tip), MutationReceipt{TipTree: tip, Verdict: "pass", FinishedAt: time.Now()})
	}
	t.Cleanup(func() { mutantsWatchPollInterval = prevPoll; mutantsWatchPolledFn = prevHook })

	var buf bytes.Buffer
	code := WatchMutantsHere(root, "", &buf)
	if code != ExitMutantsWatchDone {
		t.Fatalf("code = %d, want ExitMutantsWatchDone (%d) once the receipt lands after the first poll — this test's own (genuinely alive) pid must never read as killed; output: %s",
			code, ExitMutantsWatchDone, buf.String())
	}
}

// A live process that never advances — neither its log nor its mutants.out
// gains anything — is read as stalled once mutantsWatchStallTimeout passes,
// even though nothing killed it and no death was recorded.
func TestWatchMutantsHere_ReportsStalledWhenALiveProcessNeverAdvances(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := initRepoWithCommit(t, "hello")
	tip := gitOut(root, "rev-parse", "HEAD:")
	worktree := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "job.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	saveMutantsJob(MutantsJob{Repo: commonGitDir(root), Branch: "main", TipTree: tip,
		PID: os.Getpid(), Started: time.Now(), Worktree: worktree, Log: logPath})

	prevPoll, prevStall := mutantsWatchPollInterval, mutantsWatchStallTimeout
	mutantsWatchPollInterval = time.Millisecond
	mutantsWatchStallTimeout = 20 * time.Millisecond
	t.Cleanup(func() { mutantsWatchPollInterval, mutantsWatchStallTimeout = prevPoll, prevStall })

	var buf bytes.Buffer
	code := WatchMutantsHere(root, "", &buf)
	if code != ExitMutantsWatchStalled {
		t.Fatalf("code = %d, want ExitMutantsWatchStalled (%d); output: %s", code, ExitMutantsWatchStalled, buf.String())
	}
	if !strings.Contains(buf.String(), "stalled") {
		t.Fatalf("output = %q, want it to say the run appears stalled", buf.String())
	}
}

// --job addresses an EXACT job record, the same way `run --job` does, and
// needs no git repository at dir at all: Repo/TipTree come from the file.
func TestWatchMutantsHere_JobFlagNeedsNoGitRepository(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const tip = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	writeReceiptFile(MutationReceiptPathFor(tip), MutationReceipt{TipTree: tip, Verdict: "pass", FinishedAt: time.Now()})

	jobPath := filepath.Join(t.TempDir(), "job.json")
	if err := writeMutantsJobFile(jobPath, MutantsJob{Repo: "some-repo", TipTree: tip, Worktree: t.TempDir(), Tip: tip}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	// dir is a path that is NOT a git repository at all: --job must never
	// consult it.
	code := WatchMutantsHere(t.TempDir(), jobPath, &buf)
	if code != ExitMutantsWatchDone {
		t.Fatalf("code = %d, want ExitMutantsWatchDone (%d); output: %s", code, ExitMutantsWatchDone, buf.String())
	}
}
