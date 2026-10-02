//go:build windows

package ghworkflow

import "testing"

func TestIsWSLLauncher_RecognisesTheSystem32Bash(t *testing.T) {
	for path, want := range map[string]bool{
		`C:\Windows\System32\bash.exe`:          true,
		`c:\windows\SYSTEM32\bash.exe`:          true,
		`C:\Program Files\Git\bin\bash.exe`:     false,
		`C:\Program Files\Git\usr\bin\bash.exe`: false,
	} {
		if got := isWSLLauncher(path); got != want {
			t.Errorf("isWSLLauncher(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestGitForWindowsBash_FindsTheBashBesideGit(t *testing.T) {
	p, ok := gitForWindowsBash()
	if !ok {
		t.Fatal("git is on PATH, so Git for Windows' bash must be found beside it")
	}
	if !fileExists(p) || isWSLLauncher(p) {
		t.Errorf("gitForWindowsBash() = %q, want an existing Git bash", p)
	}
	t.Setenv("PATH", t.TempDir())
	if p, ok := gitForWindowsBash(); ok {
		t.Errorf("with no git on PATH nothing can be found, got %q", p)
	}
}
