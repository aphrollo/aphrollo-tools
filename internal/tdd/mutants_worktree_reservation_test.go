package tdd

import (
	"sync"
	"testing"
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
