//go:build !windows

package lock

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDetectCapMode_NeedsAManagerAndTheMemoryController(t *testing.T) {
	files := map[string]string{
		"/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/cgroup.controllers": "cpu memory pids\n",
	}
	readFile := func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, os.ErrNotExist
	}
	env := func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return "/run/user/1000"
		}
		return ""
	}
	socket := func(p string) bool { return p == "/run/user/1000/systemd/private" }

	if mode, rt := detectCapMode(env, 1000, readFile, socket, true, "linux"); mode != modeCgroup || rt != "/run/user/1000" {
		t.Fatalf("with a manager and the controller: got (%s, %s), want cgroup", mode, rt)
	}
	if mode, _ := detectCapMode(env, 1000, readFile, func(string) bool { return false }, true, "linux"); mode != modeWatchdog {
		t.Fatalf("no user manager socket: got %s, want watchdog", mode)
	}
	files["/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/cgroup.controllers"] = "cpu pids\n"
	if mode, _ := detectCapMode(env, 1000, readFile, socket, true, "linux"); mode != modeWatchdog {
		t.Fatalf("memory controller not delegated: got %s, want watchdog (a scope would apply no limit)", mode)
	}
	files["/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/cgroup.controllers"] = "cpu memory pids\n"
	if mode, _ := detectCapMode(env, 1000, readFile, socket, false, "linux"); mode != modeWatchdog {
		t.Fatalf("no systemd-run: got %s, want watchdog", mode)
	}
	if mode, _ := detectCapMode(env, 1000, readFile, socket, true, "darwin"); mode != modeWatchdog {
		t.Fatalf("not linux: got %s, want watchdog", mode)
	}
}

func TestDetectCapMode_RuntimeDirDefaultsToRunUserUID(t *testing.T) {
	seen := ""
	exists := func(p string) bool { seen = p; return false }
	detectCapMode(func(string) string { return "" }, 4242, func(string) ([]byte, error) { return nil, os.ErrNotExist }, exists, true, "linux")
	if !strings.HasPrefix(seen, "/run/user/4242/") {
		t.Fatalf("probed %q, want the default /run/user/4242 runtime dir", seen)
	}
}

func TestScopeArgv_HoldsTheCommandUnderTheLimitWithNoSwap(t *testing.T) {
	got := scopeArgv("/usr/bin/systemd-run", MemCap{MB: 2048}, "/tmp/count", []string{"/usr/bin/go", "test", "./..."})
	want := []string{"/usr/bin/systemd-run", "--user", "--scope", "--quiet", "-p", "MemoryMax=2048M", "-p", "MemorySwapMax=0",
		"-p", "OOMPolicy=continue", "--", "sh", "-c", scopeWrapper, "sh", "/tmp/count", "/usr/bin/go", "test", "./..."}
	if !slices.Equal(got, want) {
		t.Fatalf("argv =\n%q\nwant\n%q", got, want)
	}
}

// The wrapper is what makes a fast kill visible: it must hand back the
// command's own exit status and write the count only when the cgroup has one.
func TestScopeWrapper_KeepsTheCommandsExitStatus(t *testing.T) {
	count := filepath.Join(t.TempDir(), "count")
	cmd := exec.Command("sh", "-c", scopeWrapper, "sh", count, "sh", "-c", "exit 7")
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 7 {
		t.Fatalf("err = %v, want the command's exit status 7 passed through", err)
	}
}

func TestEventsPathFromCgroup_OnlyARunScopeCounts(t *testing.T) {
	scope := "0::/user.slice/user-1000.slice/user@1000.service/app.slice/run-p123-i456.scope\n"
	if got := eventsPathFromCgroup(scope); got != "/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/app.slice/run-p123-i456.scope/memory.events" {
		t.Fatalf("got %q", got)
	}
	// Before systemd-run has moved itself, the process is still in the
	// caller's own scope, whose events belong to everything else in it.
	tmux := "0::/user.slice/user-1000.slice/user@1000.service/tmux-spawn-abc.scope\n"
	if got := eventsPathFromCgroup(tmux); got != "" {
		t.Fatalf("got %q for a scope that is not this run's, want none", got)
	}
}

func TestParseOOMKills_ReadsTheKernelCounter(t *testing.T) {
	n, ok := parseOOMKills("low 0\nhigh 0\nmax 12\noom 1\noom_kill 2\noom_group_kill 0\n")
	if !ok || n != 2 {
		t.Fatalf("got (%d, %v), want 2 (oom_kill, not oom or oom_group_kill)", n, ok)
	}
	if _, ok := parseOOMKills("max 1\n"); ok {
		t.Fatal("no oom_kill line is unreadable, never zero")
	}
}

func TestParseStatGroupRSS_CountsFromTheLastParenthesis(t *testing.T) {
	// pid (comm) state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt utime stime cutime cstime priority nice threads itreal start vsize rss
	line := "1234 (weird ) name) S 1 777 777 0 -1 4194560 100 0 0 0 5 6 0 0 20 0 1 0 999 123456789 4321 18446744073709551615"
	pg, rss, ok := parseStatGroupRSS(line)
	if !ok || pg != 777 || rss != 4321 {
		t.Fatalf("got (%d, %d, %v), want group 777 and 4321 resident pages", pg, rss, ok)
	}
	if _, _, ok := parseStatGroupRSS("garbage"); ok {
		t.Fatal("a line with no command name is unreadable")
	}
}

func TestWithEnv_SetsOnlyWhatIsMissing(t *testing.T) {
	if got := withEnv(nil, "XDG_RUNTIME_DIR", "/run/user/1"); got != nil {
		t.Fatalf("a nil env inherits the parent's own and must stay nil, got %v", got)
	}
	if got := withEnv([]string{"A=1"}, "XDG_RUNTIME_DIR", "/run/user/1"); len(got) != 2 || got[1] != "XDG_RUNTIME_DIR=/run/user/1" {
		t.Fatalf("got %v, want the runtime dir appended", got)
	}
	if got := withEnv([]string{"XDG_RUNTIME_DIR=/x"}, "XDG_RUNTIME_DIR", "/run/user/1"); got[0] != "XDG_RUNTIME_DIR=/x" || len(got) != 1 {
		t.Fatalf("got %v, want the caller's own value kept", got)
	}
}

// capHelperEnv marks the re-executed test binary as the allocating child.
const capHelperEnv = "LOCKTEST_CAP_HELPER_MB"

// TestCapHelper_Allocate is not a test: it is the child the real-launch tests
// start. It touches capHelperEnv megabytes, then waits (bounded) to be ended.
func TestCapHelper_Allocate(t *testing.T) {
	raw := os.Getenv(capHelperEnv)
	if raw == "" {
		t.Skip("child process of the real-launch tests")
	}
	mb, _ := strconv.Atoi(raw)
	b := make([]byte, mb<<20)
	for i := 0; i < len(b); i += 4096 {
		b[i] = 1
	}
	time.Sleep(20 * time.Second)
	_ = b[0]
}

func realLaunch(t *testing.T, mode string, capMB int64, allocMB int) (CapResult, error) {
	t.Helper()
	prev := capModeFn
	capModeFn = func() string { return mode }
	t.Cleanup(func() { capModeFn = prev })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCapHelper_Allocate$")
	cmd.Env = append(os.Environ(), capHelperEnv+"="+strconv.Itoa(allocMB))
	return RunCapped(cmd, MemCap{MB: capMB, Why: "test"})
}

func TestRunCapped_WatchdogEndsARunawayAtTheCap(t *testing.T) {
	start := time.Now()
	res, err := realLaunch(t, modeWatchdog, 150, 600)
	if !res.Killed || res.Mode != modeWatchdog {
		t.Fatalf("res=%+v err=%v, want the runaway ended by the watchdog", res, err)
	}
	if err == nil {
		t.Fatal("a killed child must not report success")
	}
	if took := time.Since(start); took > 15*time.Second {
		t.Fatalf("took %s to end a 600MB child under a 150MB cap", took)
	}
}

func TestRunCapped_WatchdogLeavesAWellBehavedRunAlone(t *testing.T) {
	// 20MB under a 400MB cap: the child then sleeps its 20s, so cut it off
	// with the context after the watchdog has had many looks.
	prev := capModeFn
	capModeFn = func() string { return modeWatchdog }
	t.Cleanup(func() { capModeFn = prev })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCapHelper_Allocate$")
	cmd.Env = append(os.Environ(), capHelperEnv+"=20")
	res, _ := RunCapped(cmd, MemCap{MB: 400})
	if res.Killed || res.Kills != 0 {
		t.Fatalf("res=%+v: a 20MB child under a 400MB cap must never be killed by it", res)
	}
}

func TestRunCapped_KernelScopeEndsARunawayAtTheCap(t *testing.T) {
	if capModeFn() != modeCgroup {
		t.Skip("no user systemd manager with a delegated memory controller on this box")
	}
	res, err := realLaunch(t, modeCgroup, 150, 600)
	if !res.Killed || res.Mode != modeCgroup {
		t.Fatalf("res=%+v err=%v, want the kernel's scope to end the runaway and the monitor to report it", res, err)
	}
}
