package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/shfake"
)

// gitxSpawnLog puts a git on PATH that records each call's argv and runs the
// real one, and answers what was recorded since the last read.
func gitxSpawnLog(t *testing.T) (calls func() []string) {
	t.Helper()
	dir, logPath := t.TempDir(), filepath.Join(t.TempDir(), "argv.log")
	shfake.Install(t, dir, "git", "#!/bin/sh\nprintf '%s\n' \"$*\" >> \"$GITXCOUNT_LOG\"\nexec \"$GITXCOUNT_REAL\" \"$@\"\n")
	t.Setenv("GITXCOUNT_LOG", logPath)
	t.Setenv("GITXCOUNT_REAL", gitBinary())
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		raw, err := os.ReadFile(logPath)
		if err != nil {
			return nil
		}
		os.Remove(logPath)
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
}

// The commit gate asks for the staged set from half a dozen stages and for the
// trunk from several: each answer comes from the index and the refs, and the
// one diff that lists the staged files is run once for an unchanged index.
func TestCommitGate_AsksGitOncePerIndexForTheStagedSetAndNeverForTheTrunkOrAMergeTip(t *testing.T) {
	main := makeGoRepo(t)
	lane := filepath.Join(t.TempDir(), "lane")
	gitDo(t, main, "worktree", "add", "-q", "-b", "lane/x", lane)
	write(t, lane, "new.go", "package m\n")
	gitDo(t, lane, "add", "new.go")
	calls := gitxSpawnLog(t)

	var staged []string
	for range 6 {
		staged = stagedFiles(lane)
		_ = TrunkBranch(lane)
		_, _ = trunkSyncTip(lane)
	}

	if len(staged) != 1 || staged[0] != "new.go" {
		t.Fatalf("staged = %q, want [new.go]", staged)
	}
	if spawns := calls(); len(spawns) > 2 {
		t.Fatalf("six rounds of the staged set, the trunk and the merge tip spawned git %d times, want 2 (the staged diff, and the one config read for a trunk the repository does not name):\n%s", len(spawns), strings.Join(spawns, "\n"))
	}
}

func TestStagedFiles_SeesAChangeToTheIndexAtOnce(t *testing.T) {
	root := makeGoRepo(t)
	write(t, root, "a.go", "package m\n")
	gitDo(t, root, "add", "a.go")
	if got := stagedFiles(root); len(got) != 1 {
		t.Fatalf("staged = %q, want a.go", got)
	}

	write(t, root, "b.go", "package m\n")
	gitDo(t, root, "add", "b.go")
	if got := stagedFiles(root); len(got) != 2 {
		t.Errorf("after staging b.go, staged = %q, want a.go and b.go", got)
	}

	write(t, root, "b.go", "package m // same size")
	write(t, root, "b.go", "package m // other  size")
	gitDo(t, root, "reset", "-q")
	if got := stagedFiles(root); len(got) != 0 {
		t.Errorf("after reset, staged = %q, want none", got)
	}
}
