package failfirst

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	igit "github.com/aphrollo/aphrollo-tools/internal/git"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// pytestWorktrees builds the layout a pre-merge gate judges: a primary
// checkout holding a backend root, a lane worktree one commit ahead of it, and
// a detached merge worktree with the lane merged in (--no-commit), which is
// where the gate runs and where no gitignored .venv exists.
func pytestWorktrees(t *testing.T) (primary, lane, merge string) {
	t.Helper()
	base := t.TempDir()
	primary = filepath.Join(base, "repo")
	lane = filepath.Join(base, "lane")
	merge = filepath.Join(base, "gate-prmerge-1")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	tddtest.GitInit(t, primary)
	write(t, primary, "backend/requirements.txt", "pytest\n")
	gitDo(t, primary, "add", ".")
	gitDo(t, primary, "commit", "-qm", "base")
	gitDo(t, primary, "worktree", "add", "-q", "-b", "lane/x", lane)
	write(t, lane, "backend/app/calc.py", "def add(a, b):\n    return a + b\n")
	gitDo(t, lane, "add", ".")
	gitDo(t, lane, "commit", "-qm", "lane change")
	gitDo(t, primary, "worktree", "add", "-q", "--detach", merge, "HEAD")
	gitDo(t, merge, "merge", "-q", "--no-commit", "--no-ff", "lane/x")
	return primary, lane, merge
}

// pytestVenvIn gives root/backend/.venv an interpreter whose import probe
// answers with exit status code, and returns its path.
func pytestVenvIn(t *testing.T, root string, code string) string {
	t.Helper()
	py := filepath.Join(root, "backend", ".venv", "bin", "python")
	writeScript(t, py, "#!/bin/sh\nexit "+code+"\n")
	return py
}

// noPytestOnPath makes PATH hold only a python3 that cannot import pytest, so
// the box's own interpreter never decides a result.
func noPytestOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	writeScript(t, filepath.Join(bin, "python3"), "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestPytestExecRunner_AMergeWorktreeWithNoVenvUsesThePrimaryCheckoutsVenv:
// the merge worktree has no gitignored backend/.venv, so the gate takes the
// interpreter of the same root in the primary checkout, and still runs it in
// the merge worktree's own backend directory, never the primary's.
func TestPytestExecRunner_AMergeWorktreeWithNoVenvUsesThePrimaryCheckoutsVenv(t *testing.T) {
	noPytestOnPath(t)
	primary, _, merge := pytestWorktrees(t)
	venv := pytestVenvIn(t, primary, "0")
	backend := filepath.Join(merge, "backend")

	got, why := pytestExecRunner(backend, Runner{Cmd: "pytest", Args: []string{"-q"}, Dir: backend})
	if why != "" {
		t.Fatalf("a primary checkout with a venv holding pytest was not used: %s", why)
	}
	if got.Cmd != venv || !slices.Equal(got.Args, []string{"-m", "pytest", "-q"}) {
		t.Errorf("ran %s %v, want %s -m pytest -q", got.Cmd, got.Args, venv)
	}
	if got.Dir != backend {
		t.Errorf("ran in %s, want the merge worktree's %s", got.Dir, backend)
	}
}

// TestPytestExecRunner_AMergeWorktreeUsesTheLaneVenvWhenThePrimaryHasNone:
// the lane that triggered the merge keeps its venv as a symlink to a venv
// kept elsewhere; the primary has none. The merge worktree's MERGE_HEAD names
// the lane, so its venv is found through the link.
func TestPytestExecRunner_AMergeWorktreeUsesTheLaneVenvWhenThePrimaryHasNone(t *testing.T) {
	noPytestOnPath(t)
	_, lane, merge := pytestWorktrees(t)
	kept := t.TempDir()
	py := filepath.Join(kept, "bin", "python")
	writeScript(t, py, "#!/bin/sh\nexit 0\n")
	if err := os.Symlink(kept, filepath.Join(lane, "backend", ".venv")); err != nil {
		t.Skip("no symlinks here") // skip-ok: the lane's venv is a symlink on the box this models
	}
	backend := filepath.Join(merge, "backend")

	got, why := pytestExecRunner(backend, Runner{Cmd: "pytest", Dir: backend})
	if why != "" {
		t.Fatalf("the lane's venv was not found: %s", why)
	}
	if want := filepath.Join(lane, "backend", ".venv", "bin", "python"); got.Cmd != want {
		t.Errorf("ran %s, want %s", got.Cmd, want)
	}
}

// ratchet: test_removed TestPytestExecRunner_ThePrimaryVenvOutranksTheLaneVenv: the order is reversed, the merged lane before the primary (#1223 review)
// TestPytestExecRunner_TheLaneVenvOutranksThePrimaryVenv: both have a venv
// with pytest; the merged lane's is the one used, since it was built for the
// code being merged and the primary's may be stale.
func TestPytestExecRunner_TheLaneVenvOutranksThePrimaryVenv(t *testing.T) {
	noPytestOnPath(t)
	primary, lane, merge := pytestWorktrees(t)
	pytestVenvIn(t, primary, "0")
	want := pytestVenvIn(t, lane, "0")
	backend := filepath.Join(merge, "backend")

	got, why := pytestExecRunner(backend, Runner{Cmd: "pytest", Dir: backend})
	if why != "" || got.Cmd != want {
		t.Fatalf("got %s, %q; want the lane's %s", got.Cmd, why, want)
	}
}

// TestPytestExecRunner_ASiblingVenvWithoutPytestIsNotUsed: a venv elsewhere
// that cannot import pytest is passed over, not run.
func TestPytestExecRunner_ASiblingVenvWithoutPytestIsNotUsed(t *testing.T) {
	noPytestOnPath(t)
	primary, _, merge := pytestWorktrees(t)
	pytestVenvIn(t, primary, "1")
	backend := filepath.Join(merge, "backend")

	got, why := pytestExecRunner(backend, Runner{Cmd: "pytest", Dir: backend})
	if why == "" {
		t.Fatalf("an interpreter that cannot import pytest was chosen: %+v", got)
	}
}

// TestPytestExecRunner_RefusalNamesEveryVenvSearchedAndWhereToCreateOne: with
// no venv holding pytest anywhere, the reason lists each directory searched,
// the merge worktree's, the primary checkout's and the lane's, and says to
// create the venv in the primary checkout; the system python is not offered
// as a place to install requirements.
func TestPytestExecRunner_RefusalNamesEveryVenvSearchedAndWhereToCreateOne(t *testing.T) {
	noPytestOnPath(t)
	primary, lane, merge := pytestWorktrees(t)
	backend := filepath.Join(merge, "backend")

	_, why := pytestExecRunner(backend, Runner{Cmd: "pytest", Dir: backend})
	if why == "" {
		t.Fatal("no interpreter imports pytest, yet the gate found one")
	}
	for _, root := range []string{merge, primary, lane} {
		if dir := filepath.Join(root, "backend", ".venv"); !strings.Contains(why, dir) {
			t.Errorf("reason does not name the searched %s:\n%s", dir, why)
		}
	}
	if want := "create " + filepath.Join(primary, "backend", ".venv"); !strings.Contains(why, want) {
		t.Errorf("reason does not say where to create the venv (%q):\n%s", want, why)
	}
	if strings.Contains(why, "install the requirements of") {
		t.Errorf("reason still advises installing into the interpreter that failed:\n%s", why)
	}
}

// TestPytestProofRunner_NoInterpreterReasonNamesEveryVenvDirSearched: with no
// interpreter at all, the reason lists the venv directories of the root and of
// the other worktrees alike.
func TestPytestProofRunner_NoInterpreterReasonNamesEveryVenvDirSearched(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	_, why := pytestProofRunner(pytestSearch{root: root, elsewhere: []string{other}, remedy: other},
		Runner{Cmd: "pytest"}, noInterpreter, func(string) error { return nil })
	for _, dir := range []string{filepath.Join(root, ".venv"), filepath.Join(other, ".venv"), filepath.Join(other, "venv")} {
		if !strings.Contains(why, dir) {
			t.Errorf("reason does not name the searched %s:\n%s", dir, why)
		}
	}
	if want := "create " + filepath.Join(other, ".venv"); !strings.Contains(why, want) {
		t.Errorf("reason does not say %q:\n%s", want, why)
	}
}

// TestPytestExecRunner_AnUnrelatedWorktreesVenvIsNotUsed: a worktree that is
// neither the primary checkout nor the lane being merged may hold a venv with
// pytest; the gate does not borrow it, and does not list it as searched.
func TestPytestExecRunner_AnUnrelatedWorktreesVenvIsNotUsed(t *testing.T) {
	noPytestOnPath(t)
	primary, _, merge := pytestWorktrees(t)
	unrelated := filepath.Join(t.TempDir(), "unrelated")
	gitDo(t, primary, "worktree", "add", "-q", "--detach", unrelated, "HEAD")
	pytestVenvIn(t, unrelated, "0")
	backend := filepath.Join(merge, "backend")

	got, why := pytestExecRunner(backend, Runner{Cmd: "pytest", Dir: backend})
	if why == "" {
		t.Fatalf("borrowed the venv of an unrelated worktree: %+v", got)
	}
	if strings.Contains(why, unrelated) {
		t.Errorf("an unrelated worktree is listed as searched:\n%s", why)
	}
}

// TestPytestExecRunner_ARootInThePrimaryCheckoutSearchesItsOwnVenvOnce: the
// fail-first proof of a root in the primary checkout has no other tree to
// look in; its own venv directories are listed once, not twice.
func TestPytestExecRunner_ARootInThePrimaryCheckoutSearchesItsOwnVenvOnce(t *testing.T) {
	noPytestOnPath(t)
	primary, _, _ := pytestWorktrees(t)
	backend := filepath.Join(primary, "backend")

	_, why := pytestExecRunner(backend, Runner{Cmd: "pytest", Dir: backend})
	if n := strings.Count(why, filepath.Join(backend, ".venv")+","); n != 1 {
		t.Errorf("the root's own .venv is listed %d times in:\n%s", n, why)
	}
}

// TestParseWorktrees_ReadsPathAndHeadAndIgnoresAHeadBeforeAnyPath: each
// `worktree` row opens an entry that the following HEAD row fills; a HEAD row
// with no entry open (malformed output) is dropped, not a crash.
func TestParseWorktrees_ReadsPathAndHeadAndIgnoresAHeadBeforeAnyPath(t *testing.T) {
	got := parseWorktrees("HEAD stray\nworktree /r/main\nHEAD aaa\nbranch refs/heads/main\n\nworktree /r/lane\nHEAD bbb\ndetached\n")
	want := []worktreeEntry{{path: "/r/main", head: "aaa"}, {path: "/r/lane", head: "bbb"}}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A root reached through a symbolic link, or spelled in another case, is the
// same worktree git lists by its own spelling: the search compares and builds
// paths in one canonical spelling.
func TestOtherWorktreeRoots_ARootNamedThroughALinkFindsTheSameWorktreesGitLists(t *testing.T) {
	_, _, merge := pytestWorktrees(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Dir(merge), link); err != nil {
		t.Fatalf("cannot make a symbolic link: %v", err)
	}
	backend := filepath.Join(link, filepath.Base(merge), "backend")

	search := otherWorktreeRoots(backend)

	primary := igit.Canonical(filepath.Join(filepath.Dir(merge), "repo", "backend"))
	if search.remedy != primary {
		t.Errorf("remedy = %q, want the primary checkout's root %q", search.remedy, primary)
	}
	own := igit.Canonical(filepath.Join(merge, "backend"))
	if slices.Contains(search.elsewhere, own) || !slices.Contains(search.elsewhere, primary) {
		t.Errorf("elsewhere = %q, want the primary %q and never the root's own %q", search.elsewhere, primary, own)
	}
}

// The primary checkout is always searched, whatever the merge in progress, and
// a linked worktree is searched only when its HEAD is the merge being judged:
// a root in a lane with no merge in progress has the primary and nothing else,
// and a root in the merge worktree has the primary and the lane it merges.
func TestOtherWorktreeRoots_SearchThePrimaryAlwaysAndALinkedWorktreeOnlyWhenItIsTheMergeTip(t *testing.T) {
	primary, lane, merge := pytestWorktrees(t)
	unrelated := filepath.Join(t.TempDir(), "unrelated")
	gitDo(t, primary, "worktree", "add", "-q", "--detach", unrelated, "HEAD")
	root := func(tree string) string { return igit.Canonical(filepath.Join(tree, "backend")) }

	fromLane := otherWorktreeRoots(filepath.Join(lane, "backend"))
	if want := []string{root(primary)}; !slices.Equal(fromLane.elsewhere, want) {
		t.Errorf("from the lane (no merge in progress): elsewhere = %q, want only the primary %q", fromLane.elsewhere, want)
	}

	fromMerge := otherWorktreeRoots(filepath.Join(merge, "backend"))
	want := []string{root(lane), root(primary)}
	if !slices.Equal(fromMerge.elsewhere, want) {
		t.Errorf("from the merge worktree: elsewhere = %q, want the merged lane then the primary %q, not the unrelated or its own", fromMerge.elsewhere, want)
	}
}
