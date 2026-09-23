package tdd

import (
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

// The machine-wide lock dir is live on every box this suite runs on: the CI
// runner and other sessions hold real locks in it while the suite runs. A test
// that resolves it can take one of those locks for an instant, sweep its
// owner records, or leave files behind, and nothing in the test's own result
// says so. The guard makes reaching it a failure of the whole package run.
//
// It does not compare the directory's entries before and after the run: other
// processes change that directory all the time, so such a check fails on a
// busy box for reasons that are not this suite's. It intercepts the one place
// the live dir is resolved instead, so a test that reaches it gets a decoy
// directory under the package's temp dir and the run is reported red with the
// stack that got there.
func guardLiveLockDir(decoy string) (check func() error) {
	var reached atomic.Int64
	var once sync.Once
	var firstStack string
	prev := sharedLockDirName
	sharedLockDirName = func() string {
		reached.Add(1)
		once.Do(func() { firstStack = string(debug.Stack()) })
		return decoy
	}
	return func() error {
		sharedLockDirName = prev
		if n := reached.Load(); n > 0 {
			return fmt.Errorf("live lock dir guard: tests resolved the machine-wide lock dir %d time(s); every test must use a temp lock dir (SetLockDirForTest). First caller:\n%s", n, firstStack)
		}
		return nil
	}
}
