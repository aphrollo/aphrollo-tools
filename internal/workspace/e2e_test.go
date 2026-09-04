package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeSystemctl writes a fake systemctl to dir that records its invocation
// args to marker (overwriting each call), for the claim/unclaim Apply e2e
// tests that fake the privileged restart. POSIX: a shebang shell script.
// Windows can't run one directly — CreateProcess doesn't dispatch a shebang,
// and Go's os/exec refuses a file with no PATHEXT-recognized extension even
// given a full path (LookPath's Windows rule applies to the executable check,
// not just name resolution) — so on Windows the fake is a .bat with the
// equivalent one-liner instead.
func fakeSystemctl(t *testing.T, dir, marker string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		p := filepath.Join(dir, "systemctl.bat")
		if err := os.WriteFile(p, []byte("@echo off\r\necho %* > "+marker+"\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho \"$@\" > "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// scrubGitEnv unsets the repo-pointing GIT_* variables a git hook exports
// (GIT_DIR, GIT_INDEX_FILE, GIT_WORK_TREE, …). When the suite runs UNDER the
// aphrollo tdd pre-commit gate, those point at the REAL repo — without this,
// the fixtures' git ops and production Apply's `git worktree add` would target
// (and can corrupt) the real .git instead of the throwaway. Restored on cleanup.
func scrubGitEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE",
		"GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR", "GIT_PREFIX",
	} {
		if v, ok := os.LookupEnv(k); ok {
			os.Unsetenv(k)
			t.Cleanup(func() { os.Setenv(k, v) })
		}
	}
}

// initRepo builds a throwaway git repo with one commit and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	return initRepoAt(t, t.TempDir())
}

// initRepoAt builds a throwaway git repo with one commit at a caller-chosen
// path (created if absent), so spaces-layout fixtures (<root>/<owner>/<repo>)
// can be assembled. Same git isolation as initRepo.
func initRepoAt(t *testing.T, dir string) string {
	t.Helper()
	// Isolate git BEFORE the first commit. The operator box installs a global
	// core.hooksPath (the aphrollo tdd gate); an empty GIT_CONFIG_GLOBAL +
	// GIT_CONFIG_SYSTEM=/dev/null drops it so the fixture commit can't recurse
	// into the gate. scrubGitEnv drops the hook's GIT_DIR/GIT_INDEX_FILE so git
	// targets this throwaway, not the real repo.
	scrubGitEnv(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir
}

// TestPrepareApply_E2E drives BuildPlan + Apply against a real git repo and
// asserts the worktree is created and safe.directory is written — to an
// ISOLATED global config (GIT_CONFIG_GLOBAL) so the test never touches the real
// ~/.gitconfig. Re-running must be idempotent: the second plan skips both the
// safe.directory and the worktree-add steps.
func TestPrepareApply_E2E(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	gitcfg := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
	// safe.directory now lands in a dedicated runtime config (git-[include]d
	// from the ansible-managed ~/.gitconfig), NOT the global file ansible owns,
	// so the two writers never clobber each other. Point it at a not-yet-
	// existing dir so the plan's mkdir step is exercised too.
	runtimecfg := filepath.Join(t.TempDir(), "git", "aphrollo-runtime.cfg")
	t.Setenv("APHROLLO_GIT_RUNTIME_CONFIG", runtimecfg)

	req := Request{Repo: repo, Branch: "feat/work", NoInstall: true}

	plan, err := BuildPlan(req)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	// Fresh repo: nothing skipped yet.
	for _, s := range plan.Steps {
		if s.Skip != "" {
			t.Fatalf("fresh plan should have no skips, got skip on %q: %s", s.Title, s.Skip)
		}
	}
	// The plan ensures the runtime-config dir before marking safe.directory.
	var hasDirStep bool
	for _, s := range plan.Steps {
		if s.Title == "ensure git config dir" {
			hasDirStep = true
		}
	}
	if !hasDirStep {
		t.Fatal("plan should include the runtime-config dir-ensure step")
	}

	var out, errb bytes.Buffer
	if err := Apply(plan, &out, &errb); err != nil {
		t.Fatalf("Apply: %v\nstdout:%s\nstderr:%s", err, out.String(), errb.String())
	}

	// Worktree exists and is a real working tree.
	wt := plan.Worktree
	if fi, err := os.Stat(wt); err != nil || !fi.IsDir() {
		t.Fatalf("worktree not created at %s: %v", wt, err)
	}
	if err := exec.Command("git", "-C", wt, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		t.Fatalf("%s is not a git worktree: %v", wt, err)
	}

	// safe.directory landed in the dedicated runtime config, NOT the
	// ansible-owned global file — the whole point of the include split. git
	// config escapes a literal backslash in a value as "\\", so on Windows
	// (where our paths carry backslashes) the raw file bytes double them;
	// unescape before the substring check rather than matching escaped text.
	rcfg, _ := os.ReadFile(runtimecfg)
	unescaped := strings.ReplaceAll(string(rcfg), `\\`, `\`)
	if !strings.Contains(unescaped, plan.Repo) || !strings.Contains(unescaped, wt) {
		t.Fatalf("safe.directory not written to runtime config for repo+worktree:\n%s", rcfg)
	}
	if gcfg, _ := os.ReadFile(gitcfg); strings.Contains(string(gcfg), "safe.directory") {
		t.Fatalf("global config must not carry safe.directory (ansible owns it):\n%s", gcfg)
	}

	// Idempotency: rebuild — safe.directory + worktree-add now skipped.
	plan2, err := BuildPlan(req)
	if err != nil {
		t.Fatalf("BuildPlan rerun: %v", err)
	}
	var safeSkipped, addSkipped bool
	for _, s := range plan2.Steps {
		switch s.Title {
		case "mark git-safe":
			if s.Skip != "" {
				safeSkipped = true
			}
		case "create worktree":
			if s.Skip != "" {
				addSkipped = true
			}
		}
	}
	if !safeSkipped {
		t.Error("rerun should skip an already-marked safe.directory")
	}
	if !addSkipped {
		t.Error("rerun should skip the existing worktree")
	}

	// Apply is safe to re-run (skips, no error).
	var out2 bytes.Buffer
	if err := Apply(plan2, &out2, &errb); err != nil {
		t.Fatalf("re-Apply: %v", err)
	}
	if !strings.Contains(out2.String(), "[skip]") {
		t.Fatalf("re-Apply should report skips:\n%s", out2.String())
	}
}

// TestList_E2E checks List returns the repo's worktrees including a prepared one.
func TestList_E2E(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))

	plan, err := BuildPlan(Request{Repo: repo, Branch: "wt1", NoInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := Apply(plan, &out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	listing, err := List(repo)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(listing, "wt1") {
		t.Fatalf("List missing prepared worktree:\n%s", listing)
	}
}

func TestBuildPlan_NotARepo(t *testing.T) {
	_, err := BuildPlan(Request{Repo: t.TempDir(), Branch: "x"})
	if err == nil {
		t.Fatal("expected error for non-git dir")
	}
}
