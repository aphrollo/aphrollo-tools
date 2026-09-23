//go:build !windows

package lock

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
	return meminfoGB("MemTotal")
}

// machineAvailGB is how much memory this box can hand a new process right
// now, in whole gigabytes, 0 when it cannot be read.
//
// MemAvailable rather than MemFree: the kernel's own estimate of what a
// workload could allocate without swapping, which counts the reclaimable page
// cache a build's own reads have just filled. MemFree on a box that has been
// compiling reads near zero and would hold every run to the floor. It is the
// Linux counterpart of the Windows reader's available commit — not the same
// quantity, but the same question, and the same answer shape: what is
// obtainable, not what is installed.
func machineAvailGB() int {
	return meminfoGB("MemAvailable")
}

// meminfoGB reads one /proc/meminfo key in whole gigabytes, 0 when the file,
// the key or its number cannot be read (a non-Linux unix, a container without
// procfs, a kernel too old for MemAvailable) — which every caller treats as
// unknown rather than as zero bytes.
func meminfoGB(want string) int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok || key != want {
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

// processExePath is the live image path the OS reports for pid right now,
// "" when it cannot be read (a permissions problem, or a non-Linux unix with
// no /proc). /proc/<pid>/exe is a symlink the kernel keeps pointed at the
// running image's CURRENT path even after the file on disk is renamed or
// unlinked, which is exactly the case this exists to catch: aphrollo's own
// the installer renames the running binary aside and lets it keep executing.
func processExePath(pid int) (string, bool) {
	p, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return "", false
	}
	return p, true
}
