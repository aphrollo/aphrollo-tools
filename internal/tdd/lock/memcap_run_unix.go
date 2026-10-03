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
	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// capModeFn is the enforcer this process uses, decided once.
var capModeFn = sync.OnceValue(func() string {
	mode, _ := detectCapMode(os.Getenv, os.Getuid(), os.ReadFile, capPathExists, systemdRunPath, runtime.GOOS)
	return mode
})

// capRuntimeDirFn is where the user manager's sockets live, for the child's
// environment.
var capRuntimeDirFn = func() string {
	_, rt := detectCapMode(os.Getenv, os.Getuid(), os.ReadFile, capPathExists, systemdRunPath, runtime.GOOS)
	return rt
}

func capPathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// capLookPath is where name is on PATH, "" when it is not.
func capLookPath(name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

func capShellPath() string { return capLookPath("sh") }

func systemdRunPath() string { return capLookPath("systemd-run") }

// detectCapMode decides between the kernel-enforced scope and the watchdog
// from facts about the box, injected: a user manager whose runtime dir
// carries a socket, and a memory controller delegated to that manager's
// cgroup. Anything short of both is the watchdog, because a scope started
// without the controller silently applies no limit at all.
func detectCapMode(getenv func(string) string, uid int, readFile func(string) ([]byte, error),
	exists func(string) bool, systemdRun func() string, goos string) (mode, runtimeDir string) {
	if goos != "linux" || systemdRun() == "" {
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

// launchCapped is the unix enforcer: a CapRun that starts and waits for cmd
// itself.
func launchCapped(cmd *exec.Cmd, c MemCap) (CapResult, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	r := NewCapRun(c)
	r.Before(cmd)
	if err := cmd.Start(); err != nil {
		r.Ended()
		return CapResult{Cap: c, Mode: r.mode}, err
	}
	r.Started(cmd.Process.Pid)
	err := cmd.Wait()
	r.Ended()
	return r.Result(nil), err
}

// CapRun holds one run to its cap while internal/run starts and ends it: it is
// the run.Hook that carries this package's enforcers (the kernel scope or the
// watchdog) into a child run owns. The command must lead a process group of
// its own, as run's children do.
type CapRun struct {
	cap     MemCap
	mode    string
	m       *capMonitor
	events  *scopeEvents
	final   func() int
	cleanup func()
	stop    chan struct{}
	done    chan struct{}
}

// NewCapRun is the hook that holds a run to c.
func NewCapRun(c MemCap) *CapRun {
	return &CapRun{cap: c, mode: modeWatchdog, cleanup: func() {}}
}

// Before wraps cmd in the cap's scope where the kernel enforces it.
func (r *CapRun) Before(cmd *exec.Cmd) {
	if capModeFn() == modeCgroup {
		if sr, shell := systemdRunPath(), capShellPath(); sr != "" && shell != "" && cmd.Err == nil {
			if f, err := os.CreateTemp("", "aphrollo-oomcount-*"); err == nil {
				countFile := f.Name()
				_ = f.Close()
				r.cleanup = func() { _ = os.Remove(countFile) }
				r.mode = modeCgroup
				argv := scopeArgv(sr, r.cap, countFile, append([]string{cmd.Path}, cmd.Args[1:]...))
				cmd.Path, cmd.Args = argv[0], argv
				cmd.Env = withEnv(cmd.Env, "XDG_RUNTIME_DIR", capRuntimeDirFn())
				r.final = func() int { n, _ := readOOMKills(countFile); return n }
			}
		}
	}
	r.m = &capMonitor{cap: r.cap, mode: r.mode}
	r.m.probe = capProbe{procs: scanProcRSS}
	// The pid is known only after Start: the probes read it on first use.
	r.m.probe.killTree = func() { _ = proc.KillTree(r.m.pgrp) }
	r.m.probe.killPIDs = func(pids []int) {
		for _, p := range pids {
			_ = syscall.Kill(p, syscall.SIGKILL)
		}
	}
	r.events = &scopeEvents{find: scopeEventsPath, count: readOOMKills}
	if r.mode == modeCgroup {
		r.m.probe.oomKills = r.events.oomKills
	}
}

// Started begins watching the child.
func (r *CapRun) Started(pid int) {
	r.events.pid = pid
	r.m.pgrp = pid
	r.stop, r.done = make(chan struct{}), make(chan struct{})
	go r.m.run(r.stop, r.done)
}

// Ended stops watching, folds in the kernel's own count of kills, and removes
// what Before made. It is safe for a child that never started.
func (r *CapRun) Ended() {
	defer r.cleanup()
	if r.stop == nil {
		return
	}
	close(r.stop)
	<-r.done
	if r.final != nil {
		r.m.settle(r.final())
	}
}

// Result is what became of the run. The child is not asked: the monitor saw it.
func (r *CapRun) Result(_ *run.Child) CapResult {
	if r.m == nil {
		return CapResult{Cap: r.cap, Mode: r.mode}
	}
	return r.m.result()
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

// scopeEvents reads the kill counter of the scope pid runs in. The scope is
// found on first use — systemd-run moves itself into it a moment after it
// starts — and remembered, so each look after that is one file read.
type scopeEvents struct {
	pid   int
	path  string
	find  func(pid int) string
	count func(path string) (int, bool)
}

// oomKills is the count, ok=false while the scope cannot be found or read.
func (s *scopeEvents) oomKills() (int, bool) {
	if s.path == "" {
		s.path = s.find(s.pid)
		if s.path == "" {
			return 0, false
		}
	}
	return s.count(s.path)
}

// scopeEventsPath is the memory.events file of the transient scope pid runs
// in, "" until systemd-run has moved itself into it.
func scopeEventsPath(pid int) string { return scopeEventsPathFrom(pid, os.ReadFile) }

// scopeEventsPathFrom is scopeEventsPath reading /proc through readFile.
func scopeEventsPathFrom(pid int, readFile func(string) ([]byte, error)) string {
	data, err := readFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
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
