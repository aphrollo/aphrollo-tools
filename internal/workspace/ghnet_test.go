package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This package shells out to gh for the operations with the largest blast
// radius in the whole tool: `gh pr create`, `gh pr merge`, `gh pr edit`,
// `gh api`. It had no stub and a bare TestMain, so nothing but each test's
// own arrangement stood between a mutated guard and a real pull request being
// created, edited or MERGED on the real repository.
//
// That is not hypothetical here. The same exposure in internal/tdd filed
// three real issues against this repo from nobody — #155, #196 and #197,
// carrying that package's own fixture strings, two of them four seconds apart
// while mutation jobs were starting.
//
// So gh is made unreachable for the package, and the stub REFUSES rather than
// answering: a test that needs gh must arrange its own, and one that reaches
// it by accident fails loudly instead of quietly succeeding against GitHub.
func TestGh_ResolvesToARefusingStubForEveryTestInThePackage(t *testing.T) {
	found, err := exec.LookPath("gh")
	if err != nil {
		return // unresolvable is unreachable, which is the point
	}

	stub := ghRefusalDir()
	if stub == "" {
		t.Fatal("no refusing gh stub was installed for this package")
	}
	if !strings.HasPrefix(filepath.Clean(found), filepath.Clean(stub)) {
		t.Errorf("gh resolves to %q, outside the package stub at %q — a mutated guard reaches the real GitHub and can create, edit or merge a real pull request", found, stub)
	}
}

// installRefusingGh puts a gh on PATH that exits non-zero whatever it is
// asked, and returns its directory. Two tiny scripts rather than a compiled
// binary: the point is refusal, and this costs no build.
func installRefusingGh() string {
	dir, err := os.MkdirTemp("", "aphrollo-gh-refuse")
	if err != nil {
		return ""
	}
	const msg = "gh is not available to this package's tests: stub it in the test that needs it"
	if runtime.GOOS == "windows" {
		if err := os.WriteFile(filepath.Join(dir, "gh.bat"), []byte("@echo "+msg+" 1>&2\r\n@exit /b 1\r\n"), 0o755); err != nil {
			return ""
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\necho '"+msg+"' >&2\nexit 1\n"), 0o755); err != nil {
		return ""
	}
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		return ""
	}
	return dir
}

var ghRefusalPath string

func ghRefusalDir() string { return ghRefusalPath }
