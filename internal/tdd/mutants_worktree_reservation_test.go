package tdd

import (
	"os"
	"sync"
	"testing"
	"time"
)

// chooseMutantsWorktree decides AND claims a worktree in one locked step
// (mutants_job.go), closing the window issue #405 traces: before this fix, a
// job existed and held a worktree from the moment buildMutantsJob started
// preparing it until saveMutantsJob registered it with a spawned pid — an
// unlocked read of RunningMutantsJobs in between saw no live job at all, so a
// second concurrent caller could choose and start preparing the exact same
// directory the first was still resetting or recloning.
//
// Two real goroutines drive it rather than an artificial delay: whichever
// wins the lock always decides FIRST and writes its claim before releasing,
// so the other — whenever it runs — reads that claim rather than an empty
// registry. The two must therefore never agree on base, regardless of which
// one the scheduler happened to run first.
func TestChooseMutantsWorktree_TwoConcurrentCallsForTheSameBaseNeverBothChooseIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := "borld"
	base := "C:/mutants/borld-base"

	var wg sync.WaitGroup
	results := make([]string, 2)
	tips := []string{"tip-a", "tip-b"}
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = chooseMutantsWorktree(repo, base, tips[i])
		}(i)
	}
	wg.Wait()

	if results[0] == results[1] {
		t.Fatalf("both concurrent callers chose worktree %q — one of them will run "+
			"prepareMutantsWorktree against the exact directory the other is still using (issue #405)",
			results[0])
	}
	if results[0] != base && results[1] != base {
		t.Fatalf("results = %v, want exactly one caller to keep the shared base %q", results, base)
	}
}

// The base-vs-base case above cannot catch a picker that only ever checks
// base: the first fix to #405 picked base+"-"+short(tipTree) as its ONE
// alternate with no check of its own, a pure function of (base, tipTree),
// so two callers at the SAME tip who both found base occupied computed the
// identical alternate and collided on it exactly as #283 collided on base
// (issue #436). Different tips, as above, can never exercise that: the
// alternate names differ by construction. This drives two callers at ONE
// tip against a base a third job already holds, so both must look past it.
func TestChooseMutantsWorktree_TwoConcurrentCallsAtTheSameTipNeverChooseTheSameAlternate(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := "borld"
	base := "C:/mutants/borld-base"
	tip := "tip-a"

	// A job already holds base, so both concurrent callers below must pick
	// an alternate rather than reusing it.
	saveMutantsJob(MutantsJob{Repo: repo, Worktree: base, PID: os.Getpid(), Started: time.Now()})

	var wg sync.WaitGroup
	results := make([]string, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = chooseMutantsWorktree(repo, base, tip)
		}(i)
	}
	wg.Wait()

	if results[0] == results[1] {
		t.Fatalf("both concurrent callers at the same tip chose worktree %q — one of them will run "+
			"prepareMutantsWorktree (or cloneMutantsRunTree) against the exact directory the other is "+
			"still using (issue #436)", results[0])
	}
	for _, w := range results {
		if w == base {
			t.Fatalf("results = %v, want neither to be base %q — a live job already holds it", results, base)
		}
	}
}

// The picker above now checks every candidate before handing it out, so two
// DIFFERENT processes should never legitimately be given the same worktree
// name. saveMutantsJobLocked's own supersede rule is the second line of
// defense the issue asked for regardless: matching on the worktree name
// alone used to treat ANY entry at that name as "my own earlier
// reservation" and drop it, which is exactly backwards for a second
// process's still-live claim (issue #436). This constructs that collision
// directly, bypassing the picker, to prove the registry's own rule holds it
// even so.
func TestSaveMutantsJob_NeverDropsAnotherProcessesLiveClaimAtTheSameWorktreeName(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withFakeProcessLiveness(t, map[int]string{
		4242:        `C:\bin\aphrollo.exe`,
		os.Getpid(): `C:\bin\aphrollo.exe`,
	})
	repo := "borld"
	worktree := "C:/mutants/borld-base"

	saveMutantsJob(MutantsJob{Repo: repo, Worktree: worktree, PID: 4242, Started: time.Now()})
	saveMutantsJob(MutantsJob{Repo: repo, Worktree: worktree, PID: os.Getpid(), Started: time.Now()})

	jobs := RunningMutantsJobs(repo)
	if len(jobs) != 2 {
		t.Fatalf("running jobs = %d, want both processes' claims at %q kept: %+v", len(jobs), worktree, jobs)
	}
}
