package workspace

import (
	"io"
	"os"
	"sync"
	"time"
)

// stderrDrainWait is how long restoring waits for the routed stderr to run dry.
// A child that outlives the gate and still holds the pipe must not hold the
// merge up with it.
var stderrDrainWait = 2 * time.Second

// RouteProcessStderr sends everything the process writes to os.Stderr to w
// until the returned restore runs. The pre-merge gate runs in this process
// and speaks on os.Stderr (its stage lines, the ratchet's, a suite's output),
// not on the writer the merge wait was handed, so a stamp on that writer alone
// leaves the gate's lines bare. Routed through StampLines, every line has the
// one prefix and none is dropped: restore closes the pipe and waits, up to
// stderrDrainWait, for what it still holds. If the pipe cannot be made, stderr
// is left as it is.
//
// restore is idempotent: it may be called explicitly and again from a defer.
// After a drain timeout it closes the read end with a copy still pending; the
// Windows test of that (a child holding the write end) returns within the
// bound, so the close is kept.
func RouteProcessStderr(w io.Writer) (restore func()) {
	r, pw, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	orig := os.Stderr
	os.Stderr = pw
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(w, r)
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			os.Stderr = orig
			_ = pw.Close()
			select {
			case <-done:
			case <-time.After(stderrDrainWait):
			}
			_ = r.Close()
		})
	}
}
