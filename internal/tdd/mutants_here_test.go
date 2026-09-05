package tdd

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// foregroundRuns replaces the in-process run with a recorder, so these tests
// can prove WHICH job a hand-typed run would measure without spending a
// mutation run to find out.
func foregroundRuns(t *testing.T, ran *[]MutantsJob) {
	t.Helper()
	prev := mutantsForegroundFn
	mutantsForegroundFn = func(j MutantsJob, log io.Writer) int {
		*ran = append(*ran, j)
		return 0
	}
	t.Cleanup(func() { mutantsForegroundFn = prev })
}

// The whole point of the verb: typed by hand in a lane, with no --job, it must
// build the job for the checkout it is standing in and run it. It used to
// parse an empty --job, fail to read it and return 0 — a command that asked
// for a mutation run, printed nothing, and ran nothing.
func TestRunMutantsHere_BuildsTheCurrentLanesJobInsteadOfExitingSilently(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var ran []MutantsJob
	foregroundRuns(t, &ran)
	root := optedInLane(t)

	var out bytes.Buffer
	if code := RunMutantsHere(root, &out); code != 0 {
		t.Fatalf("RunMutantsHere = %d, want 0 on an opted-in lane\noutput: %s", code, out.String())
	}
	if len(ran) != 1 {
		t.Fatalf("ran %d jobs, want exactly the current lane's one\noutput: %s", len(ran), out.String())
	}
	if ran[0].Branch != "lane/x" {
		t.Fatalf("ran job for branch %q, want the checkout's own lane/x", ran[0].Branch)
	}
}

// The hand-typed run and the merge gate must agree about what the lane is
// measured against, or a receipt earned here is refused there for a base
// mismatch. Both derive it from laneBaseSHA — the newest trunk commit the lane
// already contains — rather than from the ref name.
func TestRunMutantsHere_MeasuresFromTheSameLaneBaseTheMergeGateExpects(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var ran []MutantsJob
	foregroundRuns(t, &ran)
	root := optedInLane(t)
	want := laneBaseSHA(root)

	var out bytes.Buffer
	RunMutantsHere(root, &out)
	if len(ran) != 1 {
		t.Fatalf("ran %d jobs, want 1\noutput: %s", len(ran), out.String())
	}
	if ran[0].BaseSHA != want {
		t.Fatalf("base = %q, want laneBaseSHA %q — a run measured from another base earns a receipt the merge refuses", ran[0].BaseSHA, want)
	}
}

// The case that brings most sessions here: post-commit already started a run
// they cannot see. A second one measures the same mutants twice for the same
// receipt — the lock would serialize it, which makes the waste quiet rather
// than absent.
func TestRunMutantsHere_RefusesASecondRunWhileThisRepoIsAlreadyMeasuring(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	noKills(t)
	var started []MutantsJob
	fakeSpawn(t, &started)
	var ran []MutantsJob
	foregroundRuns(t, &ran)
	root := optedInLane(t)
	if _, ok := StartMutantsJob(root); !ok {
		t.Fatal("an opted-in lane commit must start a job")
	}

	var out bytes.Buffer
	if code := RunMutantsHere(root, &out); code == 0 {
		t.Fatalf("RunMutantsHere = 0 while a run is already going\noutput: %s", out.String())
	}
	if len(ran) != 0 {
		t.Fatalf("ran %d duplicate jobs, want none", len(ran))
	}
	if !strings.Contains(out.String(), "already") {
		t.Fatalf("output %q never says a run is already going", out.String())
	}
}

// Two lanes in one checkout measure two different trees, and a receipt is
// keyed on the tree — so the run already going answers a question this lane
// never asked. Scoping the guard to the REPO refused every other lane for as
// long as any one run lasted (observed: one lane blocked from 03:48 to 06:19,
// with a message claiming the going run "writes the same receipt this one
// would", which was false), and it refused rather than queuing, so the blocked
// lane held no place either.
func TestRunMutantsHere_RunsWhenTheGoingRunIsMeasuringADifferentTree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	noKills(t)
	var started []MutantsJob
	fakeSpawn(t, &started)
	var ran []MutantsJob
	foregroundRuns(t, &ran)
	root := optedInLane(t)
	if _, ok := StartMutantsJob(root); !ok {
		t.Fatal("an opted-in lane commit must start a job")
	}
	// A second commit moves this checkout to a different tree; the job started
	// above is still running against the old one.
	write(t, root, "src/extra.rs", "pub fn two() -> i32 { 3 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "different tree")

	var out bytes.Buffer
	if code := RunMutantsHere(root, &out); code != 0 {
		t.Fatalf("RunMutantsHere = %d for a tree nothing is measuring, want 0\noutput: %s", code, out.String())
	}
	if len(ran) != 1 {
		t.Fatalf("ran %d jobs, want the one for this tree\noutput: %s", len(ran), out.String())
	}
}

// A refusal is an ANSWER, and the hand-typed caller is owed it. Standing on
// trunk is a perfectly good reason to run nothing, and saying nothing about it
// is what sends a session back to the unlocked script.
func TestRunMutantsHere_OnTrunkNamesTheBranchRatherThanSayingNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var ran []MutantsJob
	foregroundRuns(t, &ran)
	root := optedInLane(t)
	trunk := strings.TrimSpace(gitOut(root, "for-each-ref", "--format=%(refname:short)", "refs/heads/main", "refs/heads/master"))
	if trunk == "" {
		t.Fatal("the fixture has no main or master to stand on")
	}
	gitDo(t, root, "checkout", "-q", trunk)

	var out bytes.Buffer
	code := RunMutantsHere(root, &out)
	if code == 0 {
		t.Fatalf("RunMutantsHere = 0 on %s, want a non-zero exit: nothing was measured", trunk)
	}
	if len(ran) != 0 {
		t.Fatalf("ran %d jobs on trunk, want none", len(ran))
	}
	if !strings.Contains(out.String(), trunk) {
		t.Fatalf("output %q never names the branch it refused for (%s)", out.String(), trunk)
	}
}

// The other reason a run does not happen, and the one a session cannot work
// out for itself: the repo never asked for receipts. Naming the setting is the
// difference between a dead end and a fix.
func TestRunMutantsHere_WithoutOptInNamesTheSettingThatEnablesIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var ran []MutantsJob
	foregroundRuns(t, &ran)
	root := makeCargoRepo(t)
	gitDo(t, root, "checkout", "-q", "-b", "lane/y")
	write(t, root, "src/extra.rs", "pub fn two() -> i32 { 2 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane work")

	var out bytes.Buffer
	if code := RunMutantsHere(root, &out); code == 0 {
		t.Fatal("RunMutantsHere = 0 with no opt-in, want a non-zero exit: nothing was measured")
	}
	if len(ran) != 0 {
		t.Fatalf("ran %d jobs without an opt-in, want none", len(ran))
	}
	if !strings.Contains(out.String(), "mutation-receipt") {
		t.Fatalf("output %q never names the mutation-receipt setting that would enable a run", out.String())
	}
}

// An unreadable --job is an ERROR, not an empty result. Swallowing it made a
// mistyped path exit 0 from a command whose whole purpose is to measure
// something.
func TestRunMutantsJob_UnreadableJobPathIsAnErrorNotAQuietZero(t *testing.T) {
	var out bytes.Buffer
	code := RunMutantsJobTo(filepath.Join(t.TempDir(), "no-such-job.json"), &out)
	if code == 0 {
		t.Fatalf("RunMutantsJob = 0 for a job file that does not exist, want non-zero\noutput: %s", out.String())
	}
	if !strings.Contains(out.String(), "no-such-job.json") {
		t.Fatalf("output %q never names the job path it could not read", out.String())
	}
}

// The duplicate-tip guard's skip used to trust worktree-name equality alone
// to mean "my own reservation" — the same invariant chooseMutantsWorktree's
// own picker fix (issue #436) protects, but the guard here must not ALSO
// depend on that picker being correct: a different process's live claim at
// the exact name this call ends up choosing must still read as a duplicate,
// never as itself.
//
// A pid that reads as dead the FIRST time (chooseMutantsWorktree's own read
// inside buildMutantsJob, so it never influences this call's choice of
// worktree — it lands on the SAME base a normal, uncontested run would) and
// alive from then on (this function's own duplicate-tip read) constructs
// that state directly, without a picker bug or a race, by making the same
// pid answer differently to the two separate reads that happen either side
// of this call's own reservation being written.
func TestRunMutantsHere_ADifferentProcessAtTheSameWorktreeNameIsNeverTreatedAsSelf(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var ran []MutantsJob
	foregroundRuns(t, &ran)
	root := optedInLane(t)
	repo := commonGitDir(root)
	base := MutantsWorktreeDir(root)
	tip := gitOut(root, "rev-parse", "HEAD:")

	const impostorPID = 918273
	saveMutantsJob(MutantsJob{Repo: repo, Worktree: base, TipTree: tip, PID: impostorPID, Started: time.Now()})
	seenImpostor := 0
	prevPidRunning := pidRunningFn
	pidRunningFn = func(pid int) bool {
		if pid == impostorPID {
			seenImpostor++
			return seenImpostor > 1
		}
		return prevPidRunning(pid)
	}
	t.Cleanup(func() { pidRunningFn = prevPidRunning })

	var out bytes.Buffer
	if code := RunMutantsHere(root, &out); code == 0 {
		t.Fatalf("RunMutantsHere = 0 while a different process's job already measures this exact tree\noutput: %s", out.String())
	}
	if len(ran) != 0 {
		t.Fatalf("ran %d jobs, want none — a genuinely different process already claims this tree", len(ran))
	}
}
