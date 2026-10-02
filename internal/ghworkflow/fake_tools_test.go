package ghworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The tests never run the box's python or pip: they copy the test binary to a
// file named pip, python or python3 and put that on PATH. Run under such a name
// the binary does not run tests (TestMain hands it to fakeToolMain); it records
// the call to the file named by FAKE_TOOLS_LOG as "<its own path>\t<name> <args>",
// so a test can read which copy ran, and so where an install would have landed.
// `python -m venv <dir>` makes a venv of copies of itself.

const fakeLogEnv = "FAKE_TOOLS_LOG"

// fakeExt is the executable suffix a copy needs on this host.
func fakeExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// fakeToolMain runs the fake tool the binary's own name asks for. ok is false
// when the binary is an ordinary test run.
func fakeToolMain() (code int, ok bool) {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	args := os.Args[1:]
	switch name {
	case "pip", "pip3":
		return fakeRecord(name, args), true
	case "prioprobe":
		fmt.Println(probePriority())
		return 0, true
	case "python", "python3":
		if len(args) == 3 && args[0] == "-m" && args[1] == "venv" {
			return fakeVenv(args[2]), true
		}
		return fakeRecord(name, args), true
	}
	return 0, false
}

func fakeRecord(name string, args []string) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake", name+":", err)
		return 2
	}
	f, err := os.OpenFile(os.Getenv(fakeLogEnv), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake", name+":", err)
		return 2
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s %s\n", self, name, strings.Join(args, " "))
	return 0
}

// fakeVenv makes dir a venv: python and pip copies in its bin directory.
func fakeVenv(dir string) int {
	self, err := os.Executable()
	if err != nil {
		return 2
	}
	bin := filepath.Join(dir, "bin")
	if runtime.GOOS == "windows" {
		bin = filepath.Join(dir, "Scripts")
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "fake venv:", err)
		return 2
	}
	for _, name := range []string{"python", "pip"} {
		if err := copyFile(self, filepath.Join(bin, name+fakeExt())); err != nil {
			fmt.Fprintln(os.Stderr, "fake venv:", err)
			return 2
		}
	}
	return 0
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}

// fakeToolsOnPath puts fake python, python3 and pip into a new directory,
// puts it first on this process's PATH, points the fakes' log at a file in a
// fresh directory, and returns that directory and the log's path.
func fakeToolsOnPath(t *testing.T) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"python", "python3", "pip"} {
		if err := copyFile(self, filepath.Join(dir, name+fakeExt())); err != nil {
			t.Fatal(err)
		}
	}
	log = filepath.Join(t.TempDir(), "fake-tools.log")
	t.Setenv(fakeLogEnv, log)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, log
}

// useFakePython makes the run's venv come from the fake python3 in dir.
func useFakePython(t *testing.T, dir string) {
	t.Helper()
	prev := findPython
	findPython = func() (string, bool) { return filepath.Join(dir, "python3"+fakeExt()), true }
	t.Cleanup(func() { findPython = prev })
}

// prioProbeOnPath puts a copy of this binary named prioprobe first on PATH.
// Run under that name it prints "low" when its own process runs below normal
// priority and "normal" when it does not (probePriority, per platform).
func prioProbeOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFile(self, filepath.Join(dir, "prioprobe"+fakeExt())); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
