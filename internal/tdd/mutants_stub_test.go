package tdd

import (
	"context"
	"io"
	"sync"
	"testing"
)

// measuredCall is one invocation the runner's exec seam received.
type measuredCall struct {
	Dir  string
	Env  []string
	Argv []string
}

// stubMutantsExec replaces the runner's exec seam for one test and records
// every call. reply is asked what that call should do — its exit code, and
// whatever outcomes file it wants to leave behind. It is handed the run's own
// context as well, so a test can stand in for a child that ends when, and
// only when, its caller gives up.
//
// A sharded measurement calls the seam from N goroutines at once, so the
// record is taken under a lock: without one the recorder is a data race, and
// a test that reads the calls back is reading whatever survived it. reply
// itself runs unlocked, because a shard's stand-in has to be able to block
// until its context ends while the others run.
func stubMutantsExec(t *testing.T, reply func(ctx context.Context, n int, c measuredCall) (int, error)) *[]measuredCall {
	t.Helper()
	prev := mutantsExecFn
	calls := &[]measuredCall{}
	var mu sync.Mutex
	mutantsExecFn = func(ctx context.Context, dir string, env, argv []string, log io.Writer) (int, error) {
		mu.Lock()
		*calls = append(*calls, measuredCall{Dir: dir, Env: env, Argv: argv})
		n, call := len(*calls), (*calls)[len(*calls)-1]
		mu.Unlock()
		if reply == nil {
			return 0, nil
		}
		return reply(ctx, n, call)
	}
	t.Cleanup(func() { mutantsExecFn = prev })
	return calls
}
