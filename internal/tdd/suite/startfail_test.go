package suite

import (
	"errors"
	"io/fs"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// exitError is the *exec.ExitError of a real process that exited with code.
func exitError(t *testing.T, code string) error {
	t.Helper()
	name, args := "sh", []string{"-c", "exit " + code}
	if runtime.GOOS == "windows" {
		name, args = "cmd", []string{"/c", "exit " + code}
	}
	err := exec.Command(name, args...).Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("exit %s ended with %v, want an ExitError", code, err)
	}
	return err
}

// A command that never started proved nothing about the code, whichever way
// the platform says it could not start. Everything else, including a run that
// merely mentions a missing command, stays an ordinary failure.
func TestStartFailure_NamesTheToolOnlyWhenTheCommandCouldNotStart(t *testing.T) {
	t.Parallel()
	const path = "/usr/local/bin:/usr/bin"
	cases := []struct {
		name   string
		err    error
		cmd    string
		output string
		want   string
	}{
		{"not found on PATH", &exec.Error{Name: "go", Err: exec.ErrNotFound}, "go", "",
			"SKIPPED (go not on PATH: " + path + ")"},
		{"no such file at an explicit path", &fs.PathError{Op: "fork/exec", Path: "/x/go", Err: fs.ErrNotExist}, "/x/go", "",
			"SKIPPED (/x/go could not start: fork/exec /x/go: file does not exist)"},
		{"shell: command not found", exitError(t, "127"), "sh", "bash: line 1: vitest: command not found\n",
			"SKIPPED (vitest not on PATH: " + path + ")"},
		{"shell: not found", exitError(t, "127"), "npm", "sh: 1: jest: not found\n",
			"SKIPPED (jest not on PATH: " + path + ")"},
		{"exit 127 that says nothing about a missing command", exitError(t, "127"), "go", "FAIL\tpkg\t0.1s\n", ""},
		{"a failing run that prints the words", exitError(t, "1"), "go", "go: command not found\n", ""},
		{"success", nil, "go", "", ""},
		{"an unrelated error", errors.New("boom"), "go", "", ""},
	}
	for _, tc := range cases {
		if got := StartFailure(tc.err, tc.cmd, func() string { return tc.output }, path); got != tc.want {
			t.Errorf("%s: StartFailure = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A suite command the box cannot find is not a red run: the result is the
// same inconclusive one a timeout or a memory refusal leaves, so no gate
// reads it as the code failing.
func TestRunSuite_ACommandNotOnPathIsInconclusiveNotRed(t *testing.T) {
	t.Parallel()
	res := RunSuite(time.Minute)(Runner{Cmd: "aphrollo-no-such-tool-1104", Args: []string{"test"}}, t.TempDir())
	if res.Passed || !res.TimedOut {
		t.Fatalf("res = %+v, want an unpassed run flagged as one that never reached a verdict", res)
	}
	if !strings.HasPrefix(res.Inconclusive, "SKIPPED (aphrollo-no-such-tool-1104 not on PATH: ") {
		t.Errorf("Inconclusive = %q, want it to name the missing tool and the PATH it looked on", res.Inconclusive)
	}
}

func TestRunSuite_ABinaryThatIsNotThereIsInconclusiveNotRed(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "bin", "go")
	res := RunSuite(time.Minute)(Runner{Cmd: missing, Args: []string{"test"}}, t.TempDir())
	if res.Passed || !res.TimedOut || !strings.Contains(res.Inconclusive, missing) {
		t.Fatalf("res = %+v, want an inconclusive run naming %s", res, missing)
	}
}

// A run that started and failed is still red: only a start failure is excused.
func TestRunSuite_AFailingRunIsStillAFailure(t *testing.T) {
	t.Parallel()
	r := Runner{Cmd: "sh", Args: []string{"-c", "exit 1"}}
	if runtime.GOOS == "windows" {
		r = Runner{Cmd: "cmd", Args: []string{"/c", "exit 1"}}
	}
	res := RunSuite(time.Minute)(r, t.TempDir())
	if res.Passed || res.TimedOut || res.Inconclusive != "" {
		t.Fatalf("res = %+v, want a plain failure", res)
	}
}

func TestIsToolMissing_TellsASkippedToolFromAMemoryRefusal(t *testing.T) {
	t.Parallel()
	if !IsToolMissing("SKIPPED (go not on PATH: /usr/bin)") {
		t.Error("a missing-tool text must be recognised")
	}
	for _, other := range []string{"SKIPPED — memory headroom: 1.0 GB available", "OOM-KILLED at 4.0 GB", ""} {
		if IsToolMissing(other) {
			t.Errorf("%q is not a missing tool", other)
		}
	}
}
