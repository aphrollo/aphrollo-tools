// Package runtest is what a test needs to prove that a process tree is gone:
// the OS's own answer to "is this pid running", a shell chain whose
// grandchildren survive `taskkill /T`, and the pids such a chain records.
// internal/run's tests and every package that starts a child through run use
// it, so the proof is one piece of code and not a copy per package.
package runtest

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// BashChain is the script of the MSYS chain: an outer shell starts an inner
// shell, which starts a sleep, and every shell records its own pid and its
// child's, as the OS knows them, in the file named by its first argument.
// Given the second argument "wait" it holds until ended; given "leave" it
// returns once all three are up, leaving the rest running.
var BashChain = "pid() { " + TreePidExpr + "; }\n" + `
out="$1"
bash -c 'pid() { ` + TreePidExpr + `; }; sleep 600 & pid $! >> "$0"; pid $$ >> "$0"; wait' "$out" &
pid $$ >> "$out"
pid $! >> "$out"
if [ "$2" = wait ]; then
  wait
else
  until [ "$(sort -u "$out" | wc -l)" -ge 3 ]; do sleep 0.1; done
fi
`

// ReadPids is the distinct pids the tree has recorded so far in file.
func ReadPids(file string) []int {
	data, _ := os.ReadFile(file)
	var pids []int
	for _, f := range strings.Fields(string(data)) {
		if pid, err := strconv.Atoi(f); err == nil && pid > 0 && !slices.Contains(pids, pid) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// ForceKill ends one process, for a test's cleanup: it is usually gone
// already, and a refusal says only that.
func ForceKill(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// AllGone waits up to within for every pid to stop running and reports whether
// they did.
func AllGone(pids []int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		gone := true
		for _, pid := range pids {
			if Alive(pid) {
				gone = false
				break
			}
		}
		if gone {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}
