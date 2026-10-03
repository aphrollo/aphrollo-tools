// Package shfake installs a POSIX sh script as a command a test puts on PATH.
// On POSIX the script is the executable. Windows cannot run a shebang script,
// and exec.LookPath ignores a file with no PATHEXT extension, so there the
// command is a compiled trampoline that runs a sibling script under the box's
// sh. A .bat would be shorter, but cmd re-parses its arguments (mangling the
// braces and pipes of a gh --jq expression) and a batch `echo` ends its lines
// in CRLF where the real tool prints LF.
package shfake

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
	"github.com/aphrollo/aphrollo-tools/internal/run"
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
	out, err := run.LightOutput(run.Spec{Name: "git", Args: []string{"--exec-path"}})
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

// trampolineSource is the trampoline program, a file of its own so it is read
// as the program it is: it starts the box's sh as a plain child with the
// terminal handed through, and is built on its own, where it cannot reach
// internal/run.
//
//go:embed testdata/trampoline/main.go
var trampolineSource string

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
	// The module is a throwaway: a go.work above the temp dir, or one the
	// environment names, lists other modules and go refuses this one.
	spec := run.Spec{Name: "go", Args: []string{"build", "-o", exe, "."}, Dir: dir, Env: append(os.Environ(), "GOWORK=off")}
	if out, err := run.LightCombined(spec); err != nil {
		return "", fmt.Errorf("building the sh trampoline: %v\n%s", err, out)
	}
	return exe, nil
}
