package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildInstallPlan_RequiresGitRepo(t *testing.T) {
	if _, err := BuildInstallPlan(t.TempDir()); err == nil {
		t.Fatal("expected an error for a non-git directory")
	}
}

func TestInstallPlan_ApplyWritesExecutableShims(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildInstallPlan(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	for name, sub := range map[string]string{"pre-commit": "precommit", "pre-push": "prepush"} {
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

	plan, err := BuildInstallPlan(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	// The hand-written pre-commit must be preserved; pre-push still installed.
	if got, _ := os.ReadFile(preCommit); string(got) != foreign {
		t.Fatalf("foreign pre-commit was clobbered:\n%s", got)
	}
	if !strings.Contains(plan.Render(false), "SKIP") {
		t.Fatal("render should report the skipped conflicting hook")
	}
	if _, err := os.Stat(filepath.Join(hooks, "pre-push")); err != nil {
		t.Fatalf("pre-push should still be installed: %v", err)
	}
}

// A re-install overwrites our OWN shim (it carries the marker) without warning.
func TestInstallPlan_ReinstallOverOwnHook(t *testing.T) {
	root := t.TempDir()
	hooks := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(shim("precommit")), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, _ := BuildInstallPlan(root)
	if plan.Hooks[0].Conflict {
		t.Fatal("our own managed hook must not count as a conflict")
	}
}
