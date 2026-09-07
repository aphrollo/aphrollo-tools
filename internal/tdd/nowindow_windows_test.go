//go:build windows

package tdd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A DETACHED_PROCESS child has NO console, so the first console program it
// starts — cargo, then every rustc — allocates a NEW one, and Windows shows
// it. The edit hook spawns a wrapper on every edit, in every session, so the
// user's desktop filled with hundreds of windows. CREATE_NO_WINDOW gives the
// child a console that is never shown and that its children INHERIT, which is
// the difference between "no window" and "a window per compiler process".
func TestDetachedAttrs_UseCreateNoWindowNotDetachedProcess(t *testing.T) {
	t.Parallel()
	const (
		createNewProcessGroup = 0x00000200
		detachedProcess       = 0x00000008
		createNoWindow        = 0x08000000
	)
	flags := detachedAttrs().CreationFlags
	if flags&createNoWindow == 0 {
		t.Errorf("CreationFlags = %#x, want CREATE_NO_WINDOW set", flags)
	}
	if flags&detachedProcess != 0 {
		t.Errorf("CreationFlags = %#x, want DETACHED_PROCESS clear — it is what makes the child's children open windows", flags)
	}
	if flags&createNewProcessGroup == 0 {
		t.Errorf("CreationFlags = %#x, want CREATE_NEW_PROCESS_GROUP kept — a Ctrl-C at the session must not reach the build", flags)
	}
}

// The end-to-end statement: a child spawned the way the hooks spawn one has no
// visible console window, asked of Windows itself rather than of our flags.
func TestSpawnedPhaseHasNoConsoleWindow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	probe := filepath.Join(dir, "probe.go")
	out := filepath.Join(dir, "answer.txt")
	if err := os.WriteFile(probe, []byte(consoleProbeSource), 0o644); err != nil {
		t.Fatal(err)
	}

	exe := filepath.Join(dir, "probe.exe")
	build := exec.Command("go", "build", "-o", exe, probe)
	build.Dir = dir
	if b, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build the console probe here: %v\n%s", err, b)
	}

	cmd := exec.Command(exe, out)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = detachedAttrs()
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer cmd.Process.Release()

	deadline := time.Now().Add(20 * time.Second)
	var answer []byte
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil && len(b) > 0 {
			answer = b
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(answer) == 0 {
		t.Fatal("the probe never reported — it could not write its answer")
	}
	if got := strings.TrimSpace(string(answer)); got != "hidden" {
		t.Fatalf("the spawned process reports a %s console window", got)
	}
}

// consoleProbeSource is a two-level probe, because the defect is one level
// down: the spawned wrapper is quiet, and it is the CARGO it starts that gets
// a console. So the probe re-execs itself as an ordinary child — exactly how
// the wrapper starts cargo — and that child reports whether the console it
// ended up attached to is visible. Under DETACHED_PROCESS the child has no
// console to inherit and Windows gives it a fresh, VISIBLE one; under
// CREATE_NO_WINDOW it inherits its parent's hidden one.
const consoleProbeSource = `package main

import (
	"os"
	"os/exec"
	"syscall"
)

func main() {
	if len(os.Args) > 2 && os.Args[2] == "--child" {
		kernel32 := syscall.NewLazyDLL("kernel32.dll")
		user32 := syscall.NewLazyDLL("user32.dll")
		hwnd, _, _ := kernel32.NewProc("GetConsoleWindow").Call()
		answer := "hidden"
		if hwnd != 0 {
			visible, _, _ := user32.NewProc("IsWindowVisible").Call(hwnd)
			if visible != 0 {
				answer = "visible"
			}
		}
		os.WriteFile(os.Args[1], []byte(answer), 0o644)
		return
	}
	child := exec.Command(os.Args[0], os.Args[1], "--child")
	child.Run()
}
`

// Both detached spawn sites must go through the same silent stdio: a stream
// left inheriting the session's console hands the child a console to write to
// and a window to show, whatever the creation flags say.
func TestSilentStdioPointsEveryStreamAtTheNullDevice(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("cmd", "/c", "echo hi")
	closeStdio := silentStdio(cmd)
	defer closeStdio()

	if cmd.Stdin == nil || cmd.Stdout == nil || cmd.Stderr == nil {
		t.Fatalf("streams = %v/%v/%v, want the null device on all three", cmd.Stdin, cmd.Stdout, cmd.Stderr)
	}
	if cmd.Stdout != cmd.Stderr {
		t.Error("the two output streams should share the one null handle")
	}
	if f, ok := cmd.Stdout.(*os.File); !ok || !strings.EqualFold(f.Name(), os.DevNull) {
		t.Errorf("stdout = %v, want %s", cmd.Stdout, os.DevNull)
	}
}

// ratchet: test_removed TestBelowNormalAttrs_UseCreateNoWindowNotDetachedProcess: belowNormalAttrs is deleted with the detached mutation spawn it configured; the run is in the foreground and inherits the session it was typed in
