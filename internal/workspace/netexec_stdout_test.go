package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeGHPrinting builds a `gh` script in t.TempDir() that prints stdout on
// its own line and a stderr warning on another, exits 0, and prepends that
// dir to PATH for the test's duration. Cleaned up automatically:
// t.TempDir()/t.Setenv() both unwind at test end, no leftover process or file.
func fakeGHPrinting(t *testing.T, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	name := "gh"
	script := "#!/bin/sh\nprintf '%s\\n' '" + stdout + "'\nprintf '%s\\n' '" + stderr + "' 1>&2\n"
	if runtime.GOOS == "windows" {
		name = "gh.bat"
		script = "@echo off\r\necho " + stdout + "\r\necho " + stderr + " 1>&2\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestGhCombinedOutput_ReturnsStdoutOnlyEvenWithAStderrWarning is #883: gh's
// stdout and stderr used to be folded together, so a warning line (an update
// notice, a deprecation line, a proxy/auth note) landed inside the JSON
// prune.go/mergewait.go/pr.go parse as data.
func TestGhCombinedOutput_ReturnsStdoutOnlyEvenWithAStderrWarning(t *testing.T) {
	fakeGHPrinting(t, `{"number":7}`, "warning: a new release of gh is available")
	got, err := ghCombinedOutput(t.TempDir(), "pr", "view")
	if err != nil {
		t.Fatalf("ghCombinedOutput: %v", err)
	}
	want := `{"number":7}` + "\n"
	if string(got) != want {
		t.Fatalf("ghCombinedOutput = %q, want %q (stdout only)", got, want)
	}
	if strings.Contains(string(got), "release") {
		t.Fatalf("ghCombinedOutput leaked stderr into its data return: %q", got)
	}
}
