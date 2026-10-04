package run

import (
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// started counts the children this process has started, by program, so a
// caller and a test can see what a verb's work cost. A child that failed to
// start is counted too: the spawn was asked for.
var started sync.Map // program name -> *atomic.Int64

// programName is the name a program is counted under: its base name without a
// Windows executable suffix.
func programName(name string) string {
	base := filepath.Base(name)
	return strings.TrimSuffix(strings.ToLower(base), ".exe")
}

func noteStart(name string) {
	n, _ := started.LoadOrStore(programName(name), new(atomic.Int64))
	n.(*atomic.Int64).Add(1)
}

// Started is how many children of program name (git, gh, a path to either)
// this process has started since it began.
func Started(name string) int {
	if n, ok := started.Load(programName(name)); ok {
		return int(n.(*atomic.Int64).Load())
	}
	return 0
}
