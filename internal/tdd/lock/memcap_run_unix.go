//go:build !windows

package lock

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// capModeFn is the enforcer this process uses, decided once.
var capModeFn = sync.OnceValue(func() string {
	mode, _ := detectCapMode(os.Getenv, os.Getuid(), os.ReadFile, capPathExists, systemdRunPath() != "", runtime.GOOS)
	return mode
})

// capRuntimeDirFn is where the user manager's sockets live, for the child's
// environment.
var capRuntimeDirFn = func() string {
	_, rt := detectCapMode(os.Getenv, os.Getuid(), os.ReadFile, capPathExists, systemdRunPath() != "", runtime.GOOS)
	return rt
}

func capPathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func capShellPath() string {
	p, err := exec.LookPath("sh")
	if err != nil {
		return ""
	}
	return p
}

func systemdRunPath() string {
	p, err := exec.LookPath("systemd-run")
	if err != nil {
		return ""
	}
	return p
}

// detectCapMode decides between the kernel-enforced scope and the watchdog
// from facts about the box, injected: a user manager whose runtime dir
// carries a socket, and a memory controller delegated to that manager's
// cgroup. Anything short of both is the watchdog, because a scope started
// without the controller silently applies no limit at all.
func detectCapMode(getenv func(string) string, uid int, readFile func(string) ([]byte, error),
	exists func(string) bool, haveSystemdRun bool, goos string) (mode, runtimeDir string) {
	if goos != "linux" || !haveSystemdRun {
		return modeWatchdog, ""
	}
	rt := getenv("XDG_RUNTIME_DIR")
	if rt == "" {
		rt = "/run/user/" + strconv.Itoa(uid)
	}
	if !exists(filepath.Join(rt, "systemd", "private")) && !exists(filepath.Join(rt, "bus")) {
		return modeWatchdog, ""
	}
	ctl := fmt.Sprintf("/sys/fs/cgroup/user.slice/user-%d.slice/user@%d.service/cgroup.controllers", uid, uid)
	data, err := readFile(ctl)
	if err != nil || !slicesContains(strings.Fields(string(data)), "memory") {
		return modeWatchdog, ""
	}
	return modeCgroup, rt
}

func slicesContains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// scopeWrapper runs the command inside the scope and, once it is over, copies
// the scope's own oom_kill counter to the file named by its first argument.
//
// It exists because the kernel's count is only readable while the scope is:
// systemd removes the cgroup the moment its last process exits, and a file
// descriptor opened on memory.events answers ENODEV from then on, so a run
// that was killed and exited within one poll interval left no evidence behind.
// The shell is still in the scope when the command ends, which makes the
// count readable exactly once and exactly then. Its exit status is the
// command's, so a caller sees what it always saw.
const scopeWrapper = `f="$1"; shift
"$@"
rc=$?
p=$(sed -n 's/^0:://p' /proc/self/cgroup)
grep '^oom_kill ' "/sys/fs/cgroup$p/memory.events" >"$f" 2>/dev/null
exit $rc`

// scopeArgv is the command that runs argv in a transient scope held to c,
// reporting the kernel's kill count into countFile. OOMPolicy=continue leaves
// the decision to the monitor, so a suite is ended whole and a mutation run
// loses only the worker the kernel picked, on every systemd version alike.
func scopeArgv(systemdRun string, c MemCap, countFile string, argv []string) []string {
	out := []string{systemdRun, "--user", "--scope", "--quiet",
		"-p", "MemoryMax=" + strconv.FormatInt(c.MB, 10) + "M",
		"-p", "MemorySwapMax=0",
		"-p", "OOMPolicy=continue",
		"--", "sh", "-c", scopeWrapper, "sh", countFile}
	return append(out, argv...)
}

// launchCapped is the unix enforcer.
func launchCapped(cmd *exec.Cmd, c MemCap) (CapResult, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	mode := modeWatchdog
	var final func() int
	if capModeFn() == modeCgroup {
		if sr, shell := systemdRunPath(), capShellPath(); sr != "" && shell != "" && cmd.Err == nil {
			if f, err := os.CreateTemp("", "aphrollo-oomcount-*"); err == nil {
				countFile := f.Name()
				_ = f.Close()
				defer os.Remove(countFile)
				mode = modeCgroup
				argv := scopeArgv(sr, c, countFile, append([]string{cmd.Path}, cmd.Args[1:]...))
				cmd.Path, cmd.Args = argv[0], argv
				cmd.Env = withEnv(cmd.Env, "XDG_RUNTIME_DIR", capRuntimeDirFn())
				final = func() int { n, _ := readOOMKills(countFile); return n }
			}
		}
	}

	m := &capMonitor{cap: c, mode: mode}
	m.probe = capProbe{procs: scanProcRSS}
	// The pid is known only after Start: the probes are built on first use.
	var pid int
	m.probe.killTree = func() { _ = proc.KillTree(pid) }
	m.probe.killPIDs = func(pids []int) {
		for _, p := range pids {
			_ = syscall.Kill(p, syscall.SIGKILL)
		}
	}
	if mode == modeCgroup {
		var events string
		m.probe.oomKills = func() (int, bool) {
			if events == "" {
				events = scopeEventsPath(pid)
				if events == "" {
					return 0, false
				}
			}
			return readOOMKills(events)
		}
	}
	if err := cmd.Start(); err != nil {
		return CapResult{Cap: c, Mode: mode}, err
	}
	pid = cmd.Process.Pid
	m.pgrp = pid
	return waitMonitored(cmd, m, final)
}

// withEnv sets key in env when env names none. A nil env is the parent's own,
// which already has it.
func withEnv(env []string, key, val string) []string {
	if env == nil || val == "" {
		return env
	}
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return env
		}
	}
	return append(env, prefix+val)
}

// scopeEventsPath is the memory.events file of the transient scope pid runs
// in, "" until systemd-run has moved itself into it.
func scopeEventsPath(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return ""
	}
	return eventsPathFromCgroup(string(data))
}

// eventsPathFromCgroup reads /proc/<pid>/cgroup: the unified hierarchy's
// line is `0::<path>`, and a run's own scope is named run-*.scope. Any other
// path is still the manager's, whose events belong to everything else.
func eventsPathFromCgroup(data string) string {
	for line := range strings.SplitSeq(data, "\n") {
		path, ok := strings.CutPrefix(line, "0::")
		if !ok || !strings.HasSuffix(path, ".scope") || !strings.Contains(filepath.Base(path), "run-") {
			continue
		}
		return filepath.Join("/sys/fs/cgroup", path, "memory.events")
	}
	return ""
}

// readOOMKills reads the oom_kill counter of a memory.events file.
func readOOMKills(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	return parseOOMKills(string(data))
}

func parseOOMKills(data string) (int, bool) {
	for line := range strings.SplitSeq(data, "\n") {
		if v, ok := strings.CutPrefix(line, "oom_kill "); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			return n, err == nil
		}
	}
	return 0, false
}

// scanProcRSS samples every process's group and resident set from /proc.
// Empty where procfs is absent, which leaves a watchdog run unenforced.
func scanProcRSS() []procRSS {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	page := int64(os.Getpagesize())
	var out []procRSS
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		if pgrp, rss, ok := parseStatGroupRSS(string(data)); ok {
			out = append(out, procRSS{PID: pid, PGRP: pgrp, Bytes: rss * page})
		}
	}
	return out
}

// parseStatGroupRSS reads a /proc/<pid>/stat line: the process group (field
// 5) and the resident pages (field 24). The command name is parenthesised and
// may hold spaces and parens, so fields are counted from the LAST ')'.
func parseStatGroupRSS(line string) (pgrp int, rssPages int64, ok bool) {
	i := strings.LastIndexByte(line, ')')
	if i < 0 {
		return 0, 0, false
	}
	f := strings.Fields(line[i+1:])
	if len(f) < 22 {
		return 0, 0, false
	}
	pg, err1 := strconv.Atoi(f[2])
	rss, err2 := strconv.ParseInt(f[21], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return pg, rss, true
}
