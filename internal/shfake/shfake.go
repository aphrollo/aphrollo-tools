// Package shfake installs a POSIX sh script as a command a test puts on PATH.
// On POSIX the script is the executable. Windows cannot run a shebang script,
// and exec.LookPath ignores a file with no PATHEXT extension, so there the
// command is a compiled trampoline that runs a sibling script under the box's
// sh. A .bat would be shorter, but cmd re-parses its arguments (mangling the
// braces and pipes of a gh --jq expression) and a batch `echo` ends its lines
// in CRLF where the real tool prints LF.
package shfake

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// Install writes script as the command name in dir; the caller puts dir on
// PATH. It fails the test when the box cannot run it.
func Install(t testing.TB, dir, name, script string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		write(t, filepath.Join(dir, name), []byte(script))
		return
	}
	sh := windowsSh()
	if sh == "" {
		t.Fatal("shfake: no sh next to the git on PATH: cannot run a POSIX fake")
	}
	shim, err := trampoline()
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.ReadFile(shim)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, name+".shpath"), []byte(sh))
	write(t, filepath.Join(dir, name+".sh"), []byte(script))
	write(t, filepath.Join(dir, name+".exe"), exe)
}

func write(t testing.TB, path string, data []byte) {
	t.Helper()
	if err := proc.WriteExecutable(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

// windowsSh finds the sh.exe Git for Windows ships in usr\bin. git itself says
// where it lives (--exec-path is <root>\mingw64\libexec\git-core), which survives a
// git shim on PATH and avoids taking a WSL launcher for an sh.
func windowsSh() string {
	out, err := exec.Command("git", "--exec-path").Output() // stderr-ok: a failed lookup means no sh, which Install reports
	if err != nil {
		return ""
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(strings.TrimSpace(string(out))))))
	for _, rel := range []string{`usr\bin\sh.exe`, `bin\sh.exe`} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			return filepath.Join(root, rel)
		}
	}
	return ""
}

const trampolineSource = `package main

import (
	"os"
	"os/exec"
	"strings"
)

func main() {
	self, err := os.Executable()
	if err != nil {
		os.Exit(127)
	}
	base := strings.TrimSuffix(self, ".exe")
	sh, err := os.ReadFile(base + ".shpath")
	if err != nil {
		os.Exit(127)
	}
	cmd := exec.Command(string(sh), append([]string{base + ".sh"}, os.Args[1:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Without noglob the msys runtime expands the braces of gh's {owner}/{repo}
	// placeholders away before the script sees its arguments.
	cmd.Env = append(os.Environ(), "MSYS=noglob")
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		os.Exit(127)
	}
}
`

// trampoline builds the trampoline once per test binary, under the binary's
// temp root (the package TestMains isolate TMP and remove that root at exit).
var trampoline = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "shfake-")
	if err != nil {
		return "", err
	}
	return buildTrampoline(dir)
})

// buildTrampoline compiles the trampoline into dir and answers the path of the
// executable.
func buildTrampoline(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(trampolineSource), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module shtrampoline\n\ngo 1.26\n"), 0o644); err != nil {
		return "", err
	}
	exe := filepath.Join(dir, "shim.exe")
	cmd := exec.Command("go", "build", "-o", exe, ".")
	cmd.Dir = dir
	// The module is a throwaway: a go.work above the temp dir, or one the
	// environment names, lists other modules and go refuses this one.
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the sh trampoline: %v\n%s", err, out)
	}
	return exe, nil
}
