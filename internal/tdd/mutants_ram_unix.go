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

// processStartToken is the OS's own record of when the process under pid
// started, "" when it cannot be read. Recorded beside a pid so a recycled pid
// — routine on a busy box, and certain across a reboot — cannot read as the
// job that was started. On Linux it is field 22 of /proc/<pid>/stat, the start
// time in clock ticks since boot; elsewhere `ps -o lstart=` answers.
func processStartToken(pid int) string {
	if data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		// The second field is the comm, parenthesised and free to contain
		// spaces, so the fields are counted from after its closing paren.
		if i := strings.LastIndex(string(data), ") "); i >= 0 {
			rest := strings.Fields(string(data)[i+2:])
			// state is field 3, so start time (field 22) is index 19 here.
			if len(rest) > 19 {
				return rest[19]
			}
		}
		return ""
	}
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// processExePath is the live image path the OS reports for pid right now,
// "" when it cannot be read (a permissions problem, or a non-Linux unix with
// no /proc). /proc/<pid>/exe is a symlink the kernel keeps pointed at the
// running image's CURRENT path even after the file on disk is renamed or
// unlinked, which is exactly the case this exists to catch: aphrollo's own
// self-install renames the running binary aside and lets it keep executing.
func processExePath(pid int) (string, bool) {
	p, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return "", false
	}
	return p, true
}
