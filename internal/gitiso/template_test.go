package gitiso

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func templateGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A template is built once and handed out by copy: each copy must be a repo of
// its own, on main, with the committed files and an identity, and a commit in
// one copy must not move another's HEAD.
func TestRepoTemplate_CopiesAreIndependentCommittedRepos(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl")
	if err := BuildRepo(tmpl, map[string]string{"go.mod": "module x\n", "sub/a.txt": "a\n"}); err != nil {
		t.Fatal(err)
	}
	one, two := filepath.Join(t.TempDir(), "one"), filepath.Join(t.TempDir(), "two")
	for _, dst := range []string{one, two} {
		if err := CopyRepo(dst, tmpl); err != nil {
			t.Fatal(err)
		}
	}

	if got := templateGit(t, one, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("branch = %q, want main", got)
	}
	if got := templateGit(t, one, "status", "--porcelain"); got != "" {
		t.Errorf("a fresh copy is dirty:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(one, "sub", "a.txt")); err != nil {
		t.Errorf("committed file missing from the copy: %v", err)
	}
	before := templateGit(t, two, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(one, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	templateGit(t, one, "add", ".")
	templateGit(t, one, "commit", "-qm", "second")
	if after := templateGit(t, two, "rev-parse", "HEAD"); after != before {
		t.Errorf("a commit in one copy moved another's HEAD: %s -> %s", before, after)
	}
}

// CopyRepo never overwrites: a destination that already holds a file of the
// template is a copy handed out twice.
func TestCopyRepo_RefusesADestinationThatAlreadyHoldsTheTemplate(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl")
	if err := BuildRepo(tmpl, map[string]string{"a.txt": "a\n"}); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "dst")
	if err := CopyRepo(dst, tmpl); err != nil {
		t.Fatal(err)
	}
	if err := CopyRepo(dst, tmpl); err == nil {
		t.Error("a second copy into the same directory succeeded")
	}
}

// A template built with no files has no commit at all: a repo a test fills and
// commits itself, with the identity and the main branch already in place.
func TestBuildRepo_WithNoFilesLeavesARepoWithNoCommit(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl")
	if err := BuildRepo(tmpl, nil); err != nil {
		t.Fatal(err)
	}
	if got := templateGit(t, tmpl, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Errorf("HEAD points at %q, want main", got)
	}
	if out, err := exec.Command("git", "-C", tmpl, "rev-parse", "--verify", "-q", "HEAD").CombinedOutput(); err == nil {
		t.Errorf("a repo built from no files has a commit: %s", out)
	}
	if got := templateGit(t, tmpl, "config", "user.name"); got != "t" {
		t.Errorf("user.name = %q, want t", got)
	}
}

// A template may be built the first time some test needs it, under that test's
// own environment: a global config with a commit hook, or a hook's repository
// variables, must not reach the build.
func TestBuildRepo_IgnoresTheGlobalConfigAndTheRepositoryVariablesOfTheCaller(t *testing.T) {
	hooks := t.TempDir()
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[core]\n\thooksPath = "+filepath.ToSlash(hooks)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "elsewhere.git"))

	tmpl := filepath.Join(t.TempDir(), "tmpl")
	if err := BuildRepo(tmpl, map[string]string{"a.txt": "a\n"}); err != nil {
		t.Fatalf("the caller's environment reached the build: %v", err)
	}
	os.Unsetenv("GIT_DIR") // t.Setenv restores the caller's value at cleanup
	if got := templateGit(t, tmpl, "log", "--format=%s"); got != "init" {
		t.Errorf("log = %q, want the one init commit", got)
	}
}

// A template's own config keeps git's post-commit auto maintenance off: its
// build runs with the GIT_* variables stripped, so the process-wide switch
// does not reach it, and a detached maintenance run would leave lock files
// appearing and vanishing under CopyRepo's walk (CI, 2026-10-07:
// "open .git/objects/maintenance.lock: no such file or directory").
func TestBuildRepo_TemplateKeepsAutoMaintenanceOff(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl")
	if err := BuildRepo(tmpl, map[string]string{"a.txt": "a\n"}); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"maintenance.auto": "false", "gc.auto": "0"} {
		if got := templateGit(t, tmpl, "config", "--local", "--get", key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}
