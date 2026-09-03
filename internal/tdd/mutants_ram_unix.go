//go:build !windows

package tdd

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// machineRAMGB reads this box's physical memory in whole gigabytes from
// /proc/meminfo, 0 when it cannot be read (a non-Linux unix, a container
// without procfs) — which makes the jobs cap fall back to the core count
// alone rather than guessing high.
func machineRAMGB() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok || key != "MemTotal" {
			continue
		}
		fields := strings.Fields(val)
		if len(fields) == 0 {
			return 0
		}
		kb, err := strconv.Atoi(fields[0])
		if err != nil {
			return 0
		}
		return kb / (1 << 20)
	}
	return 0
}

// freeSpaceGB reports the free space in whole gigabytes on the filesystem
// holding path, false when it cannot be read.
func freeSpaceGB(path string) (int, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	return int(st.Bavail * uint64(st.Bsize) / (1 << 30)), true
}

// buildToolPids lists the live build processes that could own a target dir.
// false means the question could not be asked (no pgrep on the box), which
// every caller treats as "assume live".
func buildToolPids() ([]int, bool) {
	var pids []int
	asked := false
	for _, name := range []string{"cargo", "rustc", "cargo-nextest", "cargo-mutants"} {
		out, err := exec.Command("pgrep", "-x", name).Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == 1 {
				asked = true // nothing matched, which IS an answer
			}
			continue
		}
		asked = true
		for line := range strings.SplitSeq(string(out), "\n") {
			if n, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
				pids = append(pids, n)
			}
		}
	}
	return pids, asked
}
