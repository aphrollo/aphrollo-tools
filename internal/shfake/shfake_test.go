package shfake

import (
	"os"
	"os/exec"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/gitiso"
)

func TestMain(m *testing.M) {
	os.Exit(gitiso.Main(func() int { return m.Run() }))
}

// The installed command must see its arguments exactly as the caller passed
// them (braces and pipes included), keep stdout and stderr apart, print LF
// line endings, and hand back its own exit status.
func TestInstall_RunsTheScriptWithArgumentsStreamsAndExitStatusIntact(t *testing.T) {
	dir := t.TempDir()
	Install(t, dir, "fakecmd", "#!/bin/sh\nprintf '%s\n' \"$*\"\nprintf 'warn\n' 1>&2\nexit 7\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cmd := exec.Command("fakecmd", "repos/{owner}/{repo}", ".a[] | {b}")
	var stderr []byte
	stdout, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		stderr = ee.Stderr
	}
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 7 {
		t.Fatalf("err = %v, want exit status 7", err)
	}
	if want := "repos/{owner}/{repo} .a[] | {b}\n"; string(stdout) != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if want := "warn\n"; string(stderr) != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}
