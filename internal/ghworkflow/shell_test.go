package ghworkflow

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeBash(t *testing.T, path string, err error) {
	t.Helper()
	prev := locateBash
	locateBash = func() (string, error) { return path, err }
	t.Cleanup(func() { locateBash = prev })
}

func TestShellArgv_BashAndShRunWithTheFlagsGitHubUses(t *testing.T) {
	fakeBash(t, "/fake/bash", nil)
	for shell, want := range map[string]string{
		"":     "/fake/bash --noprofile --norc -eo pipefail C:/s/step.sh",
		"bash": "/fake/bash --noprofile --norc -eo pipefail C:/s/step.sh",
		"sh":   "/fake/bash -e C:/s/step.sh",
	} {
		argv, err := shellArgv(shell, `C:\s\step.sh`)
		if err != nil || strings.Join(argv, " ") != want {
			t.Errorf("shell %q: argv = %v, %v; want %s", shell, argv, err, want)
		}
	}
}

func TestShellArgv_ABashThatCannotBeFoundIsAnError(t *testing.T) {
	fakeBash(t, "", errors.New("no bash here"))
	for _, shell := range []string{"", "bash", "sh"} {
		if _, err := shellArgv(shell, "s.sh"); err == nil || !strings.Contains(err.Error(), "no bash here") {
			t.Errorf("shell %q: err = %v, want the locate error", shell, err)
		}
	}
}

func TestShellArgv_TemplatesAndUnsupportedShells(t *testing.T) {
	fakeBash(t, "/fake/bash", nil)
	argv, err := shellArgv("bash -x {0} --flag", "s.sh")
	if err != nil || strings.Join(argv, " ") != "bash -x s.sh --flag" {
		t.Errorf("template argv = %v, %v", argv, err)
	}
	for _, shell := range []string{"pwsh", "powershell", "cmd", "ruby {1}"} {
		if _, err := shellArgv(shell, "s.sh"); err == nil || !strings.Contains(err.Error(), shell) {
			t.Errorf("shell %q: err = %v, want a refusal naming it", shell, err)
		}
	}
}

func TestShellArgv_PythonNeedsAPythonOnPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if _, err := shellArgv("python", "s.py"); err == nil {
		t.Fatal("python with none on PATH must be an error")
	}
	name := "python3"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, shell := range []string{"python", "python3"} {
		argv, err := shellArgv(shell, "s.py")
		if err != nil || len(argv) != 2 || argv[1] != "s.py" || !strings.Contains(argv[0], "python3") {
			t.Errorf("shell %s: argv = %v, %v", shell, argv, err)
		}
	}
}

func TestLocateBash_FindsABashThatRunsScripts(t *testing.T) {
	bash, err := locateBash()
	if err != nil {
		t.Fatalf("this box has no usable bash: %v", err)
	}
	out, err := exec.Command(bash, "-c", "echo ok").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Errorf("%s -c 'echo ok' = %q, %v", bash, out, err)
	}
	if strings.Contains(strings.ToLower(bash), `\system32\`) {
		t.Errorf("located the WSL launcher: %s", bash)
	}
}

func TestHostOS_NamesTheRunnerOS(t *testing.T) {
	want := map[string]string{"windows": "Windows", "darwin": "macOS", "linux": "Linux"}[runtime.GOOS]
	if want == "" {
		want = "Linux"
	}
	if got := hostOS(); got != want {
		t.Errorf("hostOS() = %q, want %q", got, want)
	}
}

func TestParseCommandFile_ReadsPairsAndDelimitedBlocks(t *testing.T) {
	got := parseCommandFile("a=1\r\n\nb=two=2\nmulti<<EOF\nl1\nl2\nEOF\nlast=x\nnoequals\n=bad\n")
	want := []KV{{"a", "1"}, {"b", "two=2"}, {"multi", "l1\nl2"}, {"last", "x"}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %v, want %v", i, got[i], want[i])
		}
	}
	if got := parseCommandFile("k=v<<x\n"); len(got) != 1 || got[0] != (KV{"k", "v<<x"}) {
		t.Errorf("a << after the = is part of the value: %v", got)
	}
}
