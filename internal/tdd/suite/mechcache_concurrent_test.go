package suite

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// Gates running side by side in one process (a parallel test run, a hook that
// judges several roots at once) each record their green into the one cache
// file. The read-modify-write must not lose an entry: a green dropped here is
// a suite re-run for nothing at the next commit.
func TestMechCacheAdd_ConcurrentGreensAreAllKept(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const writers = 32

	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(writers)
	done.Add(writers)
	for i := range writers {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			mechCacheAdd(fmt.Sprintf("root-%d\x00h\x00go test ./...", i))
		}()
	}
	ready.Wait()
	close(start)
	finished := make(chan struct{})
	go func() { done.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("the concurrent cache writers did not finish within 30s")
	}

	for i := range writers {
		if key := fmt.Sprintf("root-%d\x00h\x00go test ./...", i); !mechCacheHit(key) {
			t.Fatalf("the green recorded for writer %d was lost to a concurrent writer", i)
		}
	}
}
