package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/shfake"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
)

// countGitSpawns puts a git on PATH that records each call's argv and then
// runs the real one, and answers a reader of what was recorded since the last
// read. Whatever a hook spawns, by whichever package, shows up here.
func countGitSpawns(t *testing.T) (calls func() []string) {
	t.Helper()
	real := gitx.GitBinary()
	dir, logPath := t.TempDir(), filepath.Join(t.TempDir(), "git-argv.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$GITCOUNT_LOG\"\nexec \"$GITCOUNT_REAL\" \"$@\"\n"
	shfake.Install(t, dir, "git", script)
	t.Setenv("GITCOUNT_LOG", logPath)
	t.Setenv("GITCOUNT_REAL", real)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		t.Helper()
		raw, err := os.ReadFile(logPath)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(logPath); err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
}

// editMode is how the hook runs its suite. The real hook defers the build
// (EnableDeferredPhases): it reads the tree once, starts the build and
// returns. A caller that runs the suite inline waits for it, and reads the tree
// a second time to learn whether it moved while the suite ran.
type editMode struct {
	name     string
	deferred bool
	want     string
	spawns   int
}

var editModes = []editMode{
	{name: "deferred", deferred: true, want: "BUILDING", spawns: 1},
	{name: "inline", want: "→ green", spawns: 2},
}

func (m editMode) arm(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	noInlineLint(t)
	if m.deferred {
		t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "0")
		fakePhases(t) // the build never finishes inside the budget: it defers
	}
}

// One Edit in a lane worktree costs one git spawn, the status of the tree:
// HEAD, branch, git directory, index and merge state are read from the files
// git keeps, and the file's committed copy from the object store. The new file
// has no committed copy; doc.go does.
func TestPostEdit_OneEditInALaneWorktreeSpawnsGitOnce(t *testing.T) {
	files := []struct{ name, content string }{
		{"widget.go", "package m\n\nfunc Widget() int { return 2 }\n"},
		{"doc.go", "package m\n\nfunc Doc() int { return 3 }\n"},
	}
	for _, mode := range editModes {
		for _, f := range files {
			t.Run(mode.name+"/"+f.name, func(t *testing.T) {
				mode.arm(t)
				_, lane := goPrimaryWithLane(t)
				write(t, lane, f.name, f.content)
				calls := countGitSpawns(t)

				got := PostEdit(postPayload("Edit", filepath.Join(lane, f.name)), fakeRun(true, "ok\nPASS"))

				if !strings.Contains(got, mode.want) {
					t.Fatalf("setup: the edit did not reach %q:\n%s", mode.want, got)
				}
				if spawns := calls(); len(spawns) > mode.spawns {
					t.Fatalf("one edit spawned git %d times, want at most %d:\n%s", len(spawns), mode.spawns, strings.Join(spawns, "\n"))
				}
			})
		}
	}
}

// A Bash command that rewrites several files is one batch: the hook asks git
// for the dirty set once, however many files moved, and reads each file's
// committed copy from the object store.
func TestPostBash_AMultiFileWriteBatchSpawnsGitOnce(t *testing.T) {
	for _, mode := range editModes {
		t.Run(mode.name, func(t *testing.T) {
			mode.arm(t)
			_, lane := goPrimaryWithLane(t)
			cmd := "cd " + filepath.ToSlash(lane) + " && gen"
			PreBash(bashPayload(t, "s-batch", lane, cmd))
			for _, name := range []string{"a.go", "b.go", "c.go"} {
				write(t, lane, name, "package m\n\nfunc F"+strings.ToUpper(name[:1])+"() int { return 1 }\n")
			}
			write(t, lane, "doc.go", "package m\n\nfunc Doc() int { return 3 }\n")
			calls := countGitSpawns(t)

			got := PostBash(bashPayload(t, "s-batch", lane, cmd), fakeRun(true, "ok\nPASS"))

			if !strings.Contains(got, mode.want) {
				t.Fatalf("setup: the batch did not reach %q:\n%s", mode.want, got)
			}
			if spawns := calls(); len(spawns) > mode.spawns {
				t.Fatalf("a four-file batch spawned git %d times, want at most %d:\n%s", len(spawns), mode.spawns, strings.Join(spawns, "\n"))
			}
		})
	}
}

// A repository with a law that judges a change (a removed test) has the edit
// hook read the files a commit would stage, and their committed copies: the
// same one status and the object store answer, no second spawn.
func TestPostEdit_AnEditJudgedByARemovedTestLawSpawnsGitOnce(t *testing.T) {
	mode := editModes[0]
	mode.arm(t)
	root := mkProject(t)
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "removed_tests.toml"), "name = \"removed_tests\"\ndescription = \"A test gone from the tree needs a tombstone\"\nseverity = \"deny\"\n\n[scope]\ninclude = [\"**/*_test.go\"]\n\n[matcher]\nkind = \"symbol-removed\"\npattern = \"^func (Test[A-Za-z0-9_]*)\\(\"\n")
	mustWrite(t, filepath.Join(root, "a_test.go"), "package m\n\nfunc TestOne(t *testing.T) {}\n\nfunc TestTwo(t *testing.T) {}\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "init")
	mustWrite(t, filepath.Join(root, "a_test.go"), "package m\n\nfunc TestOne(t *testing.T) {}\n")
	calls := countGitSpawns(t)

	got := PostEdit(postPayload("Edit", filepath.Join(root, "a_test.go")), fakeRun(true, "ok\nPASS"))

	if !strings.Contains(got, "removed_tests: a_test.go") {
		t.Fatalf("setup: the law did not name the removed test:\n%s", got)
	}
	if spawns := calls(); len(spawns) > mode.spawns {
		t.Fatalf("an edit judged by a removed-test law spawned git %d times, want at most %d:\n%s", len(spawns), mode.spawns, strings.Join(spawns, "\n"))
	}
}
