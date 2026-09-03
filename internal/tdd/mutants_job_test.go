package tdd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// optedInLane is a repo that asked for mutation receipts, standing on a lane
// branch with one commit of its own — the state a post-commit hook fires in.
func optedInLane(t *testing.T) string {
	t.Helper()
	// Pin the disk answer: these tests are about the job lifecycle, and a CI
	// runner with 13 GB free would otherwise have the start refused by the
	// free-space guard for a reason none of them is asking about.
	withFreeSpace(t, 200)
	root := makeCargoRepo(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\n[workspace]\n[workspace.metadata.aphrollo]\nmutation-receipt = true\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "opt in")
	gitDo(t, root, "checkout", "-q", "-b", "lane/x")
	write(t, root, "src/extra.rs", "pub fn two() -> i32 { 2 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane work")
	return root
}

// fakeSpawn replaces the detached start with a recorder, and hands back a pid
// that is alive for certain — this test process's own.
func fakeSpawn(t *testing.T, started *[]MutantsJob) {
	t.Helper()
	prev := mutantsSpawnFn
	mutantsSpawnFn = func(j MutantsJob) (int, error) {
		*started = append(*started, j)
		return os.Getpid(), nil
	}
	t.Cleanup(func() { mutantsSpawnFn = prev })
}

// noKills fails the test if anything tries to end a process while it runs.
func noKills(t *testing.T) {
	t.Helper()
	prev := killTreeFn
	killTreeFn = func(pid int) error {
		t.Errorf("a running job was killed (pid %d): a superseded run finishes and its outcomes carry", pid)
		return nil
	}
	t.Cleanup(func() { killTreeFn = prev })
}

// A second commit on the same lane starts a second job; the first one is left
// alone. Cancel-and-restart throws away every outcome the older run had
// already measured, which is exactly the work the incremental plan is there
// to reuse.
func TestStartMutantsJob_NeverCancelsTheRunItSupersedes(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	noKills(t)
	var started []MutantsJob
	fakeSpawn(t, &started)
	root := optedInLane(t)

	first, ok := StartMutantsJob(root)
	if !ok {
		t.Fatal("an opted-in lane commit must start a job")
	}
	write(t, root, "src/extra.rs", "pub fn two() -> i32 { 3 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "second")
	second, ok := StartMutantsJob(root)
	if !ok {
		t.Fatal("the second commit must start its own job")
	}
	if first.TipTree == second.TipTree {
		t.Fatal("the two commits must be two trees")
	}
	running := RunningMutantsJobs(first.Repo)
	if len(running) != 2 {
		t.Fatalf("running jobs = %d, want the superseded run still going alongside the new one", len(running))
	}
	requireLoggedVerdict(t, cfg, "mutants-started:"+short(second.TipTree))
}

// main is not a lane: nothing is being prepared for a merge, so nothing is
// measured. A repo that never asked for receipts is left alone entirely.
func TestStartMutantsJob_OnlyForAnOptedInLane(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var started []MutantsJob
	fakeSpawn(t, &started)

	optedOut := makeCargoRepo(t)
	gitDo(t, optedOut, "checkout", "-q", "-b", "lane/x")
	write(t, optedOut, "src/extra.rs", "pub fn two() -> i32 { 2 }\n")
	gitDo(t, optedOut, "add", "-A")
	gitDo(t, optedOut, "commit", "-qm", "lane work")
	if _, ok := StartMutantsJob(optedOut); ok {
		t.Fatal("a repo that never opted in must not have a job started for it")
	}

	onMain := optedInLane(t)
	gitDo(t, onMain, "checkout", "-q", "-")
	if _, ok := StartMutantsJob(onMain); ok {
		t.Fatal("a commit on the default branch is not a lane")
	}
	if len(started) != 0 {
		t.Fatalf("spawned %d jobs, want none", len(started))
	}
}

// A Go or Python repo has no Cargo.toml to declare anything in, so the same
// opt-in is spelled in a repo-root aphrollo.toml.
func TestStartMutantsJob_OptsInThroughAphrolloToml(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var started []MutantsJob
	fakeSpawn(t, &started)
	// This test is about the opt-in, so it states the disk it assumes: a CI
	// runner with 13 GB free would otherwise refuse the start for a reason it
	// is not asking about.
	withFreeSpace(t, 200)
	root := makeGoRepo(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutation-receipt = true\n")
	gitDo(t, root, "checkout", "-q", "-b", "lane/x")
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane work")

	if _, ok := StartMutantsJob(root); !ok {
		t.Fatal("aphrollo.toml's [aphrollo] mutation-receipt = true must opt a repo in")
	}
}

// One warm worktree per repo, beside the lane worktrees and never inside the
// checkout: the run needs a tree at the tip with its own persistent target
// dir, and a fresh copy per run is the cold build this whole design removes.
func TestMutantsWorktree_IsOneDedicatedTreePerRepo(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "borld")
	want := filepath.Join(parent, ".worktrees", "borld", "mutants")
	if got := MutantsWorktreeDir(repo); got != want {
		t.Fatalf("MutantsWorktreeDir = %q, want %q", got, want)
	}
	// The target dir lives INSIDE that worktree: it is what makes the build
	// warm across runs, and what the queue bypass is keyed on.
	if got := MutantsTargetDir(repo); !strings.HasPrefix(got, want) {
		t.Fatalf("MutantsTargetDir = %q, want it under %q", got, want)
	}
}

// The run mutates the warm worktree IN PLACE over the lane's own diff. A tree
// copy is what put 135 MB per run in the OS temp dir and rebuilt the world
// cold each time.
func TestMutantsArgv_MutatesInPlaceOverTheLaneDiff(t *testing.T) {
	got := strings.Join(MutantsArgv("D:/tmp/lane.diff", false, nil), " ")
	want := "--in-place --in-diff D:/tmp/lane.diff --test-tool=nextest"
	if got != want {
		t.Fatalf("MutantsArgv = %q, want %q", got, want)
	}
}

// The baseline run re-proves the unmutated tree passes. The gate already
// proved exactly that on this tree seconds earlier, so a green log entry buys
// the run its whole baseline back.
func TestMutantsArgv_SkipsTheBaselineOnlyWhenItWasAlreadyProven(t *testing.T) {
	if got := strings.Join(MutantsArgv("lane.diff", true, nil), " "); !strings.Contains(got, "--baseline skip") {
		t.Fatalf("MutantsArgv = %q, want the baseline skipped once the suite is proven green", got)
	}
}

// The proof is the gate's own log: the LAST run for this checkout, inside the
// window around the commit, must be green. A red or timed-out last run means
// the baseline is not proven and the mutation run must take it itself.
func TestTipSuiteGreen_ReadsTheLastGateRunForThisCheckout(t *testing.T) {
	at := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	logLines := func(verdicts ...string) string {
		var b strings.Builder
		for i, v := range verdicts {
			b.WriteString(at.Add(time.Duration(i)*time.Minute).Format(time.RFC3339) +
				" precommit D:/repo cargo_nextest " + v + " 12.0s\n")
		}
		return b.String()
	}
	since := at.Add(-30 * time.Minute)
	if !tipSuiteGreen(strings.NewReader(logLines("red", "green")), "D:/repo", since) {
		t.Fatal("a green last run must let the baseline be skipped")
	}
	if tipSuiteGreen(strings.NewReader(logLines("green", "timeout")), "D:/repo", since) {
		t.Fatal("a timed-out last run proves nothing about the baseline")
	}
	if tipSuiteGreen(strings.NewReader(logLines("green")), "D:/other", since) {
		t.Fatal("another checkout's green says nothing about this one")
	}
	if tipSuiteGreen(strings.NewReader(logLines("green")), "D:/repo", at.Add(time.Hour)) {
		t.Fatal("a run older than the window is not this tip's")
	}
}

// A merge blocked for a missing receipt when the run is ALREADY GOING needs
// to know that, or the session starts a second one on top of the first.
func TestMutationReceipt_MissingReceiptNamesTheRunningJob(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	started := time.Now().Add(-9 * time.Minute)
	saveMutantsJob(MutantsJob{Repo: "borld", TipTree: laneTip, PID: os.Getpid(), Started: started})

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("a running job is not a receipt: the merge still waits for one")
	}
	want := "gate: mutation receipt missing for tree " + short(laneTip) +
		" — running since " + started.Format("15:04") + " (pid " + strconv.Itoa(os.Getpid()) + ")"
	if got.Message != want {
		t.Fatalf("message = %q, want %q", got.Message, want)
	}
}

// A job whose process is gone is not a running job: the reject line would
// otherwise point a session at a run that died hours ago.
func TestRunningMutantsJobs_ForgetsAJobWhoseProcessIsGone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	prev := pidRunningFn
	pidRunningFn = func(pid int) bool { return pid == 4242 }
	t.Cleanup(func() { pidRunningFn = prev })

	saveMutantsJob(MutantsJob{Repo: "borld", TipTree: laneTip, PID: 4242, Started: time.Now()})
	saveMutantsJob(MutantsJob{Repo: "borld", TipTree: "abc", PID: 9, Started: time.Now()})

	running := RunningMutantsJobs("borld")
	if len(running) != 1 || running[0].PID != 4242 {
		t.Fatalf("running = %+v, want only the live job", running)
	}
}

// The liveness probe must agree with reality on the box it runs on: this
// process is alive, and a pid nothing can be running under is not.
func TestPidRunning_SeesThisProcessAndNotPidZero(t *testing.T) {
	if !pidRunning(os.Getpid()) {
		t.Fatal("this process must read as running")
	}
	if pidRunning(0) {
		t.Fatal("pid 0 is not a job")
	}
}

// The statusline is the one surface a session sees every render: a mutation
// run going in the background is a fact that changes what to do next (wait,
// rather than start another).
func TestStatusLine_SaysMutantsWhileAJobRunsForThisProject(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeGoRepo(t)
	if got := plain(StatusLine(statusPayload(t, "s1", root))); got != "[aphrollo]" {
		t.Fatalf("StatusLine = %q, want a quiet badge before any job", got)
	}
	saveMutantsJob(MutantsJob{
		Repo: commonGitDir(root), RepoRoot: root, TipTree: laneTip,
		PID: os.Getpid(), Started: time.Now(),
	})
	if got := plain(StatusLine(statusPayload(t, "s1", root))); got != "[aphrollo:mutants]" {
		t.Fatalf("StatusLine = %q, want the mutants tag", got)
	}
}

// A mutation run in ANOTHER project is not this project's business: the badge
// would send a session waiting for a receipt no commit here is owed. Projects
// commonly SHARE a build directory (CARGO_TARGET_DIR), so the run's build slot
// is not evidence about which project it measures — only the job record is.
func TestStatusLine_AMutantsJobInAnotherProjectLeavesThisBadgeQuiet(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	other := makeGoRepo(t)
	shared := t.TempDir()
	t.Setenv("CARGO_TARGET_DIR", shared)
	defer SetLockDirForTest(t.TempDir())()
	t.Setenv("CLAUDE_SESSION_ID", "s1")
	writeBuildLockOwnerAt(ReadBuildSlotOwnerPath(resolveTargetDir(os.Getenv, other)),
		"cargo mutants --in-place", other)
	saveMutantsJob(MutantsJob{
		Repo: commonGitDir(other), RepoRoot: other, TipTree: laneTip,
		PID: os.Getpid(), Started: time.Now(),
	})

	if got := plain(StatusLine(statusPayload(t, "s1", root))); got != "[aphrollo]" {
		t.Fatalf("StatusLine = %q, want a quiet badge — the run is another project's", got)
	}
}

// Two lanes of one repo share a job registry, keyed on the common git dir. A
// run in the lane beside this one is not a fact about THIS working tree.
func TestStatusLine_AMutantsJobInASiblingLaneLeavesThisBadgeQuiet(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	sibling := filepath.Join(filepath.Dir(root), "lane-beside")
	saveMutantsJob(MutantsJob{
		Repo: commonGitDir(root), RepoRoot: sibling, TipTree: laneTip,
		PID: os.Getpid(), Started: time.Now(),
	})

	if got := plain(StatusLine(statusPayload(t, "s1", root))); got != "[aphrollo]" {
		t.Fatalf("StatusLine = %q, want a quiet badge — the run is another lane's", got)
	}
}

// Two features landed on the same git hook, and git runs exactly one
// post-commit: the gate note and the lane's mutation run. One verb does both,
// and neither may swallow the other.
func TestPostCommitHook_WritesTheNoteAndStartsTheRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var started []MutantsJob
	fakeSpawn(t, &started)
	root := optedInLane(t)
	stampGreenSuiteForTest(t, root)

	if _, ok := PostCommitHook(root); !ok {
		t.Fatal("the mutation run did not start, so the note replaced it")
	}
	if len(started) != 1 {
		t.Fatalf("runs started = %d, want 1", len(started))
	}
	note, err := git(root, "notes", "--ref=gate", "show", "HEAD")
	if err != nil {
		t.Fatalf("no gate note on the commit: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(note), "green ") {
		t.Fatalf("gate note = %q, want the green note the commit earned", note)
	}
}

// stampGreenSuiteForTest leaves the record a green pre-commit run leaves, which
// is what the note is written from.
func stampGreenSuiteForTest(t *testing.T, root string) {
	t.Helper()
	tree, ok := revTree(root, "HEAD")
	if !ok {
		t.Fatal("setup: no tree for HEAD")
	}
	path := greenSuiteStampFile(root)
	if path == "" {
		t.Fatal("setup: no stamp path")
	}
	if err := os.WriteFile(path, []byte(tree), 0o600); err != nil {
		t.Fatal(err)
	}
}
