package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeManagedFiles installs the skills and agents into a Claude config dir
// the way install does.
func writeManagedFiles(t *testing.T, dir string) {
	t.Helper()
	if _, err := WriteTDDSkill(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSDDSkill(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteAgents(dir); err != nil {
		t.Fatal(err)
	}
}

// The finding says which way each file is wrong: a missing file wants an
// install, an edited one is somebody's work that the next install overwrites.
func TestDoctor_ManagedFilesNameEditedAndMissingApart(t *testing.T) {
	in := healthyInstall(t)
	if err := os.Remove(filepath.Join(in.ConfigDir, "skills", "sdd", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.ConfigDir, "agents", "reviewer.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := check(t, Doctor(in), "managed skills and agents")
	if c.OK {
		t.Fatal("a missing and an edited managed file must fail the check")
	}
	for _, want := range []string{"sdd/SKILL.md (missing)", "agents/reviewer.md (edited)"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q does not name %q", c.Detail, want)
		}
	}
}

// A current copy in the repo's own .claude satisfies the check even when the
// user-level dir holds a stale one: the session has a current copy to load.
func TestDoctor_ACurrentProjectCopyOutweighsAStaleUserCopy(t *testing.T) {
	in := healthyInstall(t)
	gitInit(t, in.Repo)
	writeManagedFiles(t, filepath.Join(in.Repo, ".claude"))
	if err := os.WriteFile(filepath.Join(in.ConfigDir, "agents", "reviewer.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if c := check(t, Doctor(in), "managed skills and agents"); !c.OK {
		t.Fatalf("the repo's .claude holds every current file, got: %s", c.Detail)
	}
}

// With no repo named there is no project dir to read: doctor must not fall
// back to whatever checkout the process happens to stand in.
func TestDoctor_NoRepoReadsNoProjectDir(t *testing.T) {
	in := healthyInstall(t)
	in.ConfigDir = t.TempDir()
	in.Repo = ""
	cwd := t.TempDir()
	gitInit(t, cwd)
	writeManagedFiles(t, filepath.Join(cwd, ".claude"))
	t.Chdir(cwd)

	if c := check(t, Doctor(in), "managed skills and agents"); c.OK {
		t.Fatal("with no repo, the working directory's .claude must not satisfy the check")
	}
}
