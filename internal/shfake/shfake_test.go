package shfake

import (
	"os"
	"os/exec"
	"path/filepath"
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

// A go.work above the temp dir (or a GOWORK in the environment) that does not
// list the trampoline's module must not stop it building: go refuses a module
// its workspace does not name, and the failure is cached for the whole binary.
func TestBuildTrampoline_IgnoresAWorkspaceAboveTheTempDir(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain on PATH") // skip-ok: the build needs one
	}
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "other", "go.mod"), []byte("module example.com/other\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "go.work"), []byte("go 1.21\n\nuse ./other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "") // the run's own isolation says off; go then looks for the file
	if _, err := buildTrampoline(filepath.Join(parent, "tramp")); err != nil {
		t.Fatalf("with a go.work above the temp dir: %v", err)
	}

	foreign := filepath.Join(parent, "foreign", "go.work")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("go 1.21\n\nuse ../other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", foreign)
	if _, err := buildTrampoline(filepath.Join(parent, "tramp2")); err != nil {
		t.Fatalf("with GOWORK naming a workspace that omits the module: %v", err)
	}
}
