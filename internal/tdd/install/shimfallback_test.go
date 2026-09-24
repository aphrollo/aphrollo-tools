package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The POSIX shims exec the aphrollo binary by ABSOLUTE path. When that path
// stops resolving — a rename, a half-finished install, a binary deleted while
// a copy of it was running — every `git` and every `cargo` in every Bash
// session dies with
//
//	./git: line 3: exec: C:/Users/olive/bin/aphrollo.exe: not found
//
// exit 127. That happened: two sessions lost git and cargo entirely for
// several minutes, with a message naming a path and no way to act on it.
//
// The gate is best-effort and the tools it wraps are not. A missing binary
// must degrade to running the real tool UNGATED, saying so once and naming
// the command that fixes it. `sh` runs the script on both platforms here —
// it is the interpreter the shim itself names on its shebang line, and the
// Windows boxes this gate runs on have it.
func TestBinShim_RunsTheRealToolWhenTheBinaryIsGone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// A "real git" that proves it ran, standing in for the tool on PATH.
	real := filepath.Join(dir, "realgit")
	mustWrite(t, real, "#!/bin/sh\necho REAL \"$@\"\n")
	if err := os.Chmod(real, 0o755); err != nil {
		t.Fatal(err)
	}

	shim := filepath.Join(dir, "git")
	mustWrite(t, shim, binShim(filepath.Join(dir, "no-such-aphrollo"), "git", real))
	if err := os.Chmod(shim, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("sh", shim, "status", "--short").CombinedOutput()
	if err != nil {
		t.Fatalf("the shim failed instead of falling through: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "REAL status --short") {
		t.Errorf("output %q does not show the real tool running with the caller's arguments verbatim", out)
	}
	if !strings.Contains(string(out), "aphrollo install") {
		t.Errorf("output %q names no way to fix the missing binary", out)
	}
}

// ...and with the binary present nothing changes: the shim still hands every
// argument to the gate, because that is the whole point of it being there.
func TestBinShim_StillExecsTheGateWhenTheBinaryIsThere(t *testing.T) {
	t.Parallel()
	script := binShim("/opt/aphrollo", "cargo", "/usr/bin/cargo")

	if !strings.Contains(script, `exec "/opt/aphrollo" `+CmdName+" cargo") {
		t.Errorf("shim does not exec the gate:\n%s", script)
	}
}

// A fallback nobody could resolve at install time must not turn into an exec
// of the empty string, which the shell reads as running the shim's own
// directory. With no fallback the shim says what is wrong and stops.
func TestBinShim_WithNoFallbackRefusesRatherThanExecNothing(t *testing.T) {
	t.Parallel()
	script := binShim("/opt/aphrollo", "git", "")

	if strings.Contains(script, `exec "" `) {
		t.Errorf("an empty fallback became an exec of nothing:\n%s", script)
	}
	if !strings.Contains(script, "aphrollo install") {
		t.Errorf("shim names no remedy:\n%s", script)
	}
}
