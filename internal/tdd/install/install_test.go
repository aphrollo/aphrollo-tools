package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// testBin stands in for the resolved aphrollo binary path the hooks should exec.
const testBin = "/opt/aphrollo/bin/aphrollo"

func TestBuildInstallPlan_RequiresGitRepo(t *testing.T) {
	t.Parallel()
	if _, err := BuildInstallPlan(t.TempDir(), testBin); err == nil {
		t.Fatal("expected an error for a non-git directory")
	}
}

// The installed shim must exec the RESOLVED binary path, not a bare `aphrollo`
// that depends on the hook process's PATH (mirroring the global git gate).
func TestInstallPlan_ShimUsesResolvedBinPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildInstallPlan(root, testBin)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range plan.Hooks {
		// Quoted since the Windows-path fix: the exec line always wraps the
		// (slash-normalized) resolved path in double quotes.
		if !strings.Contains(h.Content, "exec \""+testBin+"\" gate ") {
			t.Fatalf("shim does not exec the resolved bin %q:\n%s", testBin, h.Content)
		}
	}
}

// The pre-merge-commit shim invokes the renamed "premerge" subcommand, not
// the pre-rename "premergecommit" spelling — git's hook FILE keeps git's own
// name, only the aphrollo verb it calls changes.
func TestInstall_WiresPreMergeCommitToGatePremerge(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildInstallPlan(root, testBin)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, h := range plan.Hooks {
		if filepath.Base(h.Path) != "pre-merge-commit" {
			continue
		}
		found = true
		if !strings.Contains(h.Content, "gate premerge") {
			t.Fatalf("pre-merge-commit shim does not invoke gate premerge:\n%s", h.Content)
		}
		if strings.Contains(h.Content, "premergecommit") {
			t.Fatalf("pre-merge-commit shim still invokes the pre-rename spelling:\n%s", h.Content)
		}
	}
	if !found {
		t.Fatal("BuildInstallPlan did not produce a pre-merge-commit hook")
	}
}

func TestInstallPlan_ApplyWritesExecutableShims(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildInstallPlan(root, testBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	for name, sub := range map[string]string{"pre-commit": "precommit", "pre-merge-commit": "premerge", "pre-push": "prepush"} {
		p := filepath.Join(root, ".git", "hooks", name)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
		// NTFS carries no exec bit (os.Stat reports 0666); git runs the shim
		// through sh on Windows regardless, so the bit only matters elsewhere.
		if runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
			t.Fatalf("%s is not executable (%v)", name, fi.Mode())
		}
		data, _ := os.ReadFile(p)
		if !strings.Contains(string(data), "aphrollo\" gate "+sub) {
			t.Fatalf("%s does not invoke the subcommand:\n%s", name, data)
		}
	}
}

// ratchet: test_removed TestInstallPlan_PrunesStrandedManagedPrePush: pre-push is a managed hook again (the undercover ref wall, #879), so an older managed shim is rewritten rather than pruned; TestInstallPlan_RewritesAnOlderManagedPrePushShim pins that.

// An older managed pre-push shim is rewritten to the current one.
func TestInstallPlan_RewritesAnOlderManagedPrePushShim(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hooks := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	prePush := filepath.Join(hooks, "pre-push")
	older := "#!/bin/sh\n" + installMarker + "\nexec /old/aphrollo tdd prepush \"$@\"\n"
	if err := os.WriteFile(prePush, []byte(older), 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildInstallPlan(root, testBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(prePush)
	if err != nil {
		t.Fatalf("the managed pre-push shim is gone: %v", err)
	}
	if string(got) != shim(testBin, "prepush") {
		t.Fatalf("pre-push = %q, want the current shim", got)
	}
}

// A foreign pre-push hook (no marker) is never pruned.
func TestInstallPlan_LeavesForeignPrePush(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hooks := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "#!/bin/sh\necho my own pre-push\n"
	prePush := filepath.Join(hooks, "pre-push")
	if err := os.WriteFile(prePush, []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildInstallPlan(root, testBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(prePush); string(got) != foreign {
		t.Fatalf("foreign pre-push was clobbered:\n%s", got)
	}
}

func TestInstallPlan_DoesNotClobberForeignHook(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hooks := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "#!/bin/sh\necho my own hook\n"
	preCommit := filepath.Join(hooks, "pre-commit")
	if err := os.WriteFile(preCommit, []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildInstallPlan(root, testBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	// The hand-written pre-commit must be preserved.
	if got, _ := os.ReadFile(preCommit); string(got) != foreign {
		t.Fatalf("foreign pre-commit was clobbered:\n%s", got)
	}
	if !strings.Contains(plan.Render(false), "SKIP") {
		t.Fatal("render should report the skipped conflicting hook")
	}
}

// A re-install overwrites our OWN shim (it carries the marker) without warning.
func TestInstallPlan_ReinstallOverOwnHook(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hooks := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(shim(testBin, "precommit")), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, _ := BuildInstallPlan(root, testBin)
	if plan.Hooks[0].Conflict {
		t.Fatal("our own managed hook must not count as a conflict")
	}
}

// #588: a linked worktree's `.git` is a FILE (`gitdir: <common>/.git/worktrees/
// <name>`), not a directory, so a plan that stats it and demands a directory
// refuses every lane `git worktree add` creates — the one place the primary
// checkout tells you to regenerate the managed CLAUDE.md block from. The hooks
// themselves are shared: they live in the COMMON git dir, which is where the
// plan must aim them, not in a `.git/hooks` under the lane that does not exist.
func TestBuildInstallPlan_AcceptsALinkedWorktree(t *testing.T) {
	main := makeGoRepo(t)
	lane := filepath.Join(t.TempDir(), "lane")
	gitDo(t, main, "worktree", "add", "-q", "-b", "lane/install-probe", lane)

	plan, err := BuildInstallPlan(lane, testBin)

	if err != nil {
		t.Fatalf("a linked worktree was refused: %v", err)
	}
	if len(plan.Hooks) == 0 {
		t.Fatal("the plan for a linked worktree carries no hooks at all")
	}
	wantDir := filepath.ToSlash(filepath.Join(main, ".git", "hooks"))
	for _, h := range plan.Hooks {
		if got := filepath.ToSlash(filepath.Dir(h.Path)); !strings.EqualFold(got, wantDir) {
			t.Fatalf("hook %q sits in %q, want the common git dir %q", filepath.Base(h.Path), got, wantDir)
		}
	}
}

// git writes warnings to STDERR — an unreadable config, a CRLF conversion — and
// keeps answering on STDOUT. A caller that reads the two folded together
// (CombinedOutput) turns the warning into part of the value, and here the value
// is a DIRECTORY PATH that `install --apply` then MkdirAlls: a warning line
// would be created on disk as a directory, and every hook written under it.
func TestBuildInstallPlan_IgnoresGitWarningsOnStderr(t *testing.T) {
	main := makeGoRepo(t)
	lane := filepath.Join(t.TempDir(), "lane")
	gitDo(t, main, "worktree", "add", "-q", "-b", "lane/warning-probe", lane)

	// Only now stand this binary in as git — the fixture above needs the real
	// one. It prints a warning on stderr and fakeGitCommonDir on stdout.
	t.Setenv(realGitEnv, os.Args[0])

	plan, err := BuildInstallPlan(lane, testBin)

	if err != nil {
		t.Fatalf("a git that warns must still resolve: %v", err)
	}
	if len(plan.Hooks) == 0 {
		t.Fatal("the plan carries no hooks at all")
	}
	want := filepath.Join(filepath.FromSlash(fakeGitCommonDir), "hooks")
	for _, h := range plan.Hooks {
		if got := filepath.Dir(h.Path); got != want {
			t.Fatalf("hook %q sits in %q, want %q — git's stderr became part of the path install would create",
				filepath.Base(h.Path), got, want)
		}
	}
}

// fakeGitCommonDir is what this binary prints on STDOUT when it is standing in
// as `git` (see tddtest.Main). A fixed sentinel, so the test asserting on the
// hooks path built from it needs nothing from the real git.
const fakeGitCommonDir = tddtest.FakeGitCommonDir
