package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testBin stands in for the resolved aphrollo binary path the hooks should exec.
const testBin = "/opt/aphrollo/bin/aphrollo"

func TestBuildInstallPlan_RequiresGitRepo(t *testing.T) {
	if _, err := BuildInstallPlan(t.TempDir(), testBin); err == nil {
		t.Fatal("expected an error for a non-git directory")
	}
}

// The installed shim must exec the RESOLVED binary path, not a bare `aphrollo`
// that depends on the hook process's PATH (mirroring the global git gate).
func TestInstallPlan_ShimUsesResolvedBinPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildInstallPlan(root, testBin)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range plan.Hooks {
		if !strings.Contains(h.Content, "exec "+testBin+" tdd ") {
			t.Fatalf("shim does not exec the resolved bin %q:\n%s", testBin, h.Content)
		}
	}
}

func TestInstallPlan_ApplyWritesExecutableShims(t *testing.T) {
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

	for name, sub := range map[string]string{"pre-commit": "precommit"} {
		p := filepath.Join(root, ".git", "hooks", name)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
		if fi.Mode().Perm()&0o111 == 0 {
			t.Fatalf("%s is not executable (%v)", name, fi.Mode())
		}
		data, _ := os.ReadFile(p)
		if !strings.Contains(string(data), "aphrollo tdd "+sub) {
			t.Fatalf("%s does not invoke the subcommand:\n%s", name, data)
		}
	}

	// pre-push is mechanical-only; install never writes it.
	if _, err := os.Stat(filepath.Join(root, ".git", "hooks", "pre-push")); !os.IsNotExist(err) {
		t.Fatalf("pre-push should not be installed: err=%v", err)
	}
}

// A stranded managed pre-push shim (from an earlier install) is pruned, while a
// foreign pre-push hook is left untouched.
func TestInstallPlan_PrunesStrandedManagedPrePush(t *testing.T) {
	root := t.TempDir()
	hooks := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	prePush := filepath.Join(hooks, "pre-push")
	if err := os.WriteFile(prePush, []byte(shim(testBin, "prepush")), 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildInstallPlan(root, testBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(prePush); !os.IsNotExist(err) {
		t.Fatalf("stranded managed pre-push should be pruned: err=%v", err)
	}
	if !strings.Contains(plan.Render(false), "prune") {
		t.Fatalf("render should report the pruned shim:\n%s", plan.Render(false))
	}
}

// A foreign pre-push hook (no marker) is never pruned.
func TestInstallPlan_LeavesForeignPrePush(t *testing.T) {
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
