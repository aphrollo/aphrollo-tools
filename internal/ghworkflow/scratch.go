package ghworkflow

import (
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
)

// live is the scratch directories of the runs in progress in this process.
var live struct {
	sync.Mutex
	dirs map[string]bool
}

func registerScratch(dir string) {
	live.Lock()
	defer live.Unlock()
	if live.dirs == nil {
		live.dirs = map[string]bool{}
	}
	live.dirs[dir] = true
}

func unregisterScratch(dir string) {
	live.Lock()
	defer live.Unlock()
	delete(live.dirs, dir)
}

// liveScratchDirs is the scratch directories of the runs still in progress.
func liveScratchDirs() []string {
	live.Lock()
	defer live.Unlock()
	var out []string
	for d := range live.dirs {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// scratchBase is where a run makes its scratch directory. A variable so a test
// points it at a directory of its own.
var scratchBase = func() string {
	if testing.Testing() {
		// A sweep or a run under test must never reach the real root of the
		// system drive; the test binary's temp dir is walled off by gitiso.
		return os.TempDir()
	}
	return shortScratchBase()
}

// ScratchBase is the directory every run's scratch is made directly under, for
// a sweep that looks for the ones a killed run left.
func ScratchBase() string { return scratchBase() }

// SetScratchBaseForTest makes runs put their scratch under dir and returns the
// function that puts it back.
func SetScratchBaseForTest(dir string) (restore func()) {
	prev := scratchBase
	scratchBase = func() string { return dir }
	return func() { scratchBase = prev }
}

// pollSleep is the wait between looks at the live runs. A variable so a test
// ends one without a real wait.
var pollSleep = time.Sleep

// RemoveLiveScratch is for a process that is about to exit without letting Run
// return: a signal ends it before Run's own removal. The caller cancels the
// runs' context first; this waits up to grace for each run to remove its own
// scratch, then removes what is left. Best effort: a directory a step still
// holds open stays for `gate gc`.
func RemoveLiveScratch(grace time.Duration) {
	deadline := time.Now().Add(grace)
	for len(liveScratchDirs()) > 0 && time.Now().Before(deadline) {
		pollSleep(25 * time.Millisecond)
	}
	for _, d := range liveScratchDirs() {
		_ = removeScratch(d) // best effort: the process is exiting and gc sweeps what stays
		unregisterScratch(d)
	}
}

// removeScratch removes the run's scratch directory: a go module cache is
// read-only by design, which RemoveTree handles.
func removeScratch(dir string) error {
	return depinstall.RemoveTree(dir)
}
