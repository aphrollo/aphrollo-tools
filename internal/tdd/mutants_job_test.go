package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// optedInLane is a repo that asked for mutation receipts, standing on a lane
// branch with one commit of its own — the state a post-commit hook fires in.
// The commit carries the `Mutants: run` trailer (issue #521): the post-commit
// path only starts a job when the author asked for one, so a fixture meant to
// exercise "a lane commit starts a job" has to say so the same way a real
// commit would.
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
	gitDo(t, root, "commit", "-qm", "lane work\n\nMutants: run")
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
	gitDo(t, root, "commit", "-qm", "second\n\nMutants: run")
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

// A second commit's own prepare must never land on the SAME directory a
// still-running job owns: prepareMutantsWorktree is a `git reset --hard` (or,
// for Go, an os.RemoveAll plus reclone of the deterministic clone dir keyed
// on this same path), and running it against the tree the first job's
// producer is currently mutating and testing in moves the tree out from
// under that measurement mid-run — traced as the likely cause of a signed
// `verdict: pass`, `mutants_total: 0` receipt on a 25-file diff (issue #283).
func TestStartMutantsJob_ASecondCommitGetsADifferentWorktreeWhileTheFirstJobStillRuns(t *testing.T) {
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
	gitDo(t, root, "commit", "-qm", "second\n\nMutants: run")
	second, ok := StartMutantsJob(root)
	if !ok {
		t.Fatal("the second commit must start its own job")
	}

	if second.Worktree == first.Worktree {
		t.Fatalf("both commits share worktree %s while the first job (pid %d) is still running — "+
			"the second job's prepare resets or reclones the tree the first is mutating and testing in",
			first.Worktree, first.PID)
	}
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
	gitDo(t, root, "commit", "-qm", "lane work\n\nMutants: run")

	if _, ok := StartMutantsJob(root); !ok {
		t.Fatal("aphrollo.toml's [aphrollo] mutation-receipt = true must opt a repo in")
	}
}

// A Go mutant is judged by re-running its whole package: 26 s on the Linux
// runner and 207 s on a Windows box for internal/tdd alone, and the analysis
// pass counts 1626 mutants in that one package. A repo whose pipeline runs the
// proof on a runner must therefore not ALSO start a detached run on the
// developer's box. The opt-out sits beside the opt-in it qualifies, and only
// an explicit `false` turns the local run off — every repo that has not said
// anything keeps the behaviour it has.
func TestStartMutantsJob_SkipsTheLocalRunWhenTheRepoRunsMutationInCI(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var started []MutantsJob
	fakeSpawn(t, &started)
	withFreeSpace(t, 200)
	root := makeGoRepo(t)
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutation-receipt = true\nmutants-local = false\n")
	gitDo(t, root, "checkout", "-q", "-b", "lane/x")
	write(t, root, "internal/x/x.go", "package x\n\nfunc X() int { return 1 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane work")

	if _, ok := StartMutantsJob(root); ok {
		t.Fatal("a repo whose mutation runs in CI must not start a local run")
	}
	if len(started) != 0 {
		t.Fatalf("spawned %d local run(s) anyway", len(started))
	}
}

// A Cargo workspace declares the same opt-out in its own table: a Rust repo
// with a runner that can carry the proof moves it off the box the same way.
func TestStartMutantsJob_TheCargoWorkspaceDeclaresTheSameOptOut(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var started []MutantsJob
	fakeSpawn(t, &started)
	withFreeSpace(t, 200)
	root := makeCargoRepo(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"m\"\nversion = \"0.1.0\"\n[workspace]\n"+
		"[workspace.metadata.aphrollo]\nmutation-receipt = true\nmutants-local = false\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "opt in")
	gitDo(t, root, "checkout", "-q", "-b", "lane/x")
	write(t, root, "src/extra.rs", "pub fn two() -> i32 { 2 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane work")

	if _, ok := StartMutantsJob(root); ok {
		t.Fatal("a Cargo workspace that runs mutation in CI must not start a local run")
	}
	if len(started) != 0 {
		t.Fatalf("spawned %d local run(s) anyway", len(started))
	}
}

// One warm worktree per LANE, all of them under one mutants root beside the
// lane worktrees and never inside the checkout: the run needs a tree at the
// tip with its own persistent target dir, and a fresh copy per run is the cold
// build this whole design removes. The root is what every containment check
// keys on.
func TestMutantsWorktree_IsOneDedicatedTreeUnderTheRepoMutantsRoot(t *testing.T) {
	root := makeCargoRepo(t)
	want := filepath.Join(filepath.Dir(root), ".worktrees", filepath.Base(root), "mutants")
	if got := MutantsRootDir(root); got != want {
		t.Fatalf("MutantsRootDir = %q, want %q", got, want)
	}
	if got := MutantsWorktreeDir(root); !strings.HasPrefix(got, want+string(filepath.Separator)) {
		t.Fatalf("MutantsWorktreeDir = %q, want a lane directory under %q", got, want)
	}
	// The target dir lives under that same root — shared by every lane's tree,
	// never inside one — because the root is what the queue bypass is keyed on.
	if got := MutantsTargetDir(root); !strings.HasPrefix(got, want) {
		t.Fatalf("MutantsTargetDir = %q, want it under %q", got, want)
	}
}

// A commit fires the post-commit hook from wherever it was made, and a LANE
// commit fires it from a lane worktree, not the repo's primary checkout.
// The mutants root must resolve from the PRIMARY checkout whichever worktree
// asks: the one `--git-common-dir` names, never a path nested under the lane's
// OWN `.worktrees` entry (issue #114 — a lane under
// `<parent>/.worktrees/borld/mutation-runner` wanted
// `<parent>/.worktrees/borld/.worktrees/mutation-runner/mutants`, a path
// `git worktree add` never had a reason to create).
//
// Under that shared root the LANE gets its own directory — see
// TestMutantsWorktreeDir_GivesTwoLanesOfOneRepoTwoDirectories for why the
// trees themselves must differ.
func TestMutantsWorktree_RootResolvesFromThePrimaryCheckoutNotTheLaneRoot(t *testing.T) {
	root := makeCargoRepo(t)
	parent := filepath.Dir(root)
	lane := filepath.Join(parent, ".worktrees", filepath.Base(root), "some-lane")
	gitDo(t, root, "worktree", "add", "-b", "lane/some-lane", lane)

	want := filepath.Join(parent, ".worktrees", filepath.Base(root), "mutants")
	if got := MutantsRootDir(lane); got != want {
		t.Fatalf("MutantsRootDir(lane) = %q, want %q (the primary's root, not one nested under the lane)", got, want)
	}
	if got := MutantsRootDir(root); got != want {
		t.Fatalf("MutantsRootDir(primary) = %q, want %q", got, want)
	}
	if got := MutantsWorktreeDir(lane); !strings.HasPrefix(got, want+string(filepath.Separator)) {
		t.Fatalf("MutantsWorktreeDir(lane) = %q, want a lane directory under %q", got, want)
	}
}

// A worktree that cannot be prepared — here, its own parent directory is a
// FILE — must not report "started": the failure is caught synchronously,
// before the parent process ever spawns the detached job or claims it did.
func TestStartMutantsJob_WorktreePrepareFailureIsLoggedAndReturned(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	withFreeSpace(t, 200)
	root := optedInLane(t)

	blocked := filepath.Dir(MutantsWorktreeDir(root))
	if err := os.MkdirAll(filepath.Dir(blocked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	j, ok, err := startMutantsJob(root)
	if ok {
		t.Fatalf("job = %+v, want ok=false when the worktree cannot be prepared", j)
	}
	if err == nil {
		t.Fatal("want a non-nil error naming the prepare failure")
	}
	found := false
	for line := range strings.SplitSeq(gateLogText(t, cfg), "\n") {
		e, ok := parseGateLine(line)
		if ok && strings.HasPrefix(e.verdict, "mutants-worktree-failed:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("gate.log has no mutants-worktree-failed entry:\n%s", gateLogText(t, cfg))
	}
}

// The run mutates the warm worktree IN PLACE over the lane's own diff. A tree
// copy is what put 135 MB per run in the OS temp dir and rebuilt the world
// cold each time.
// ratchet: test_removed TestMutantsArgv_MutatesInPlaceOverTheLaneDiff: renamed with the function it tests, MutantsArgv -> mutantsProducerFlags; the assertions are unchanged
func TestMutantsProducerFlags_MutatesInPlaceOverTheLaneDiff(t *testing.T) {
	got := strings.Join(mutantsProducerFlags("D:/tmp/lane.diff", false, nil, nil, ""), " ")
	want := "--in-place --in-diff D:/tmp/lane.diff --test-tool=nextest"
	if got != want {
		t.Fatalf("mutantsProducerFlags = %q, want %q", got, want)
	}
}

// The baseline run re-proves the unmutated tree passes. The gate already
// proved exactly that on this tree seconds earlier, so a green log entry buys
// the run its whole baseline back.
// ratchet: test_removed TestMutantsArgv_SkipsTheBaselineOnlyWhenItWasAlreadyProven: renamed with the function it tests, MutantsArgv -> mutantsProducerFlags; the assertions are unchanged
func TestMutantsProducerFlags_SkipsTheBaselineOnlyWhenItWasAlreadyProven(t *testing.T) {
	if got := strings.Join(mutantsProducerFlags("lane.diff", true, nil, nil, ""), " "); !strings.Contains(got, "--baseline skip") {
		t.Fatalf("mutantsProducerFlags = %q, want the baseline skipped once the suite is proven green", got)
	}
}

// The whole point of issue #251: a lane whose diff only touched crate alpha
// must not carry crate beta's suite into the baseline at all — cargo-mutants
// forwards --package straight into the `cargo test`/`nextest run` it runs for
// both the baseline and the mutants, so a --package list naming only alpha is
// what keeps beta's own tests from ever running, and so from ever being able
// to veto this receipt over a failure that has nothing to do with this lane.
// ratchet: test_removed TestMutantsArgv_ScopesTheBaselineToTouchedPackagesOnly: renamed with the function it tests, MutantsArgv -> mutantsProducerFlags; the assertions are unchanged
func TestMutantsProducerFlags_ScopesTheBaselineToTouchedPackagesOnly(t *testing.T) {
	got := strings.Join(mutantsProducerFlags("lane.diff", false, nil, []string{"alpha"}, ""), " ")
	if !strings.Contains(got, "--package alpha") {
		t.Fatalf("mutantsProducerFlags = %q, want the touched package named", got)
	}
	if strings.Contains(got, "beta") {
		t.Fatalf("mutantsProducerFlags = %q, want no mention of an untouched package", got)
	}
}

// An undeterminable touched-crate set (mutantsTouchedPackages returning nil)
// must fall back to today's whole-workspace behaviour — no --package flag at
// all — rather than silently measuring nothing.
// ratchet: test_removed TestMutantsArgv_OmitsPackageFlagsWhenTouchedSetIsUndeterminable: renamed with the function it tests, MutantsArgv -> mutantsProducerFlags; the assertions are unchanged
func TestMutantsProducerFlags_OmitsPackageFlagsWhenTouchedSetIsUndeterminable(t *testing.T) {
	got := strings.Join(mutantsProducerFlags("lane.diff", false, nil, nil, ""), " ")
	if strings.Contains(got, "--package") {
		t.Fatalf("mutantsProducerFlags = %q, want no --package flag for an undeterminable touched-crate set", got)
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
	saveMutantsJob(MutantsJob{Repo: "borld", Branch: "lane/x", TipTree: laneTip, PID: os.Getpid(), Started: started})

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("a running job is not a receipt: the merge still waits for one")
	}
	want := "gate: mutation receipt missing for tree " + short(laneTip) +
		" — lane/x (pid " + strconv.Itoa(os.Getpid()) + ", started " + started.Format("15:04:05") + ") is measuring"
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

	if _, ok, err := PostCommitHook(root); !ok {
		t.Fatalf("the mutation run did not start, so the note replaced it (err: %v)", err)
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

// saveMutantsJob's append is a read-modify-write with no lock between two
// StartMutantsJob calls registering close together (issue #284 follow-up):
// whichever writes second silently drops the other's job, invisibly to
// chooseMutantsWorktree (#283). This drives the actual contention:
// jobB's own save is stood up while the test itself holds the registry's own
// lock (exactly what saveMutantsJob's critical section would hold mid
// read-modify-write), and a bounded probe of that SAME lock doubles as the
// wait, so an unlocked save has every chance to have already raced ahead and
// finished by the time the probe returns.
func TestSaveMutantsJob_AConcurrentSaveWaitsForOneAlreadyInFlightRatherThanLosingAnEntry(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := "borld"
	path := mutantsJobsPath(repo)

	release, ok := acquirePathLockWithDeadline(path, time.Second)
	if !ok {
		t.Fatal("setup: could not take the jobs registry lock")
	}

	jobB := MutantsJob{Repo: repo, TipTree: "b", PID: os.Getpid(), Started: time.Now()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		saveMutantsJob(jobB)
	}()

	if _, gotLock := acquirePathLockWithDeadline(path, 150*time.Millisecond); gotLock {
		t.Fatal("setup: a probe acquired the jobs registry lock this test still holds")
	}
	select {
	case <-done:
		t.Fatal("saveMutantsJob finished while another still held the registry lock — it never waited " +
			"for it, so its read missed the entry the in-flight save was about to write")
	default:
	}

	// The in-flight save finishes: its own write, exactly what saveMutantsJob's
	// own critical section would have produced for jobA, then its release.
	jobA := MutantsJob{Repo: repo, TipTree: "a", PID: os.Getpid(), Started: time.Now()}
	data, err := json.Marshal([]MutantsJob{jobA})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, data); err != nil {
		t.Fatal(err)
	}
	release()

	<-done
	jobs := RunningMutantsJobs(repo)
	if len(jobs) != 2 {
		t.Fatalf("running jobs = %d, want both jobA and jobB registered after a save that started "+
			"while another was in flight: %+v", len(jobs), jobs)
	}
}
