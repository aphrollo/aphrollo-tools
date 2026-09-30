package tdd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// After a merge lands, the per-function test maps the commit-time mutation run
// selects tests with are rebuilt in the background, in a repo that declares
// mutants-at-commit and nowhere else. The spawn is a seam: no test here starts
// the real detached process.

const commitMutationToml = "[aphrollo]\nmutants-at-commit = true\n"

// recordTestMapSpawns replaces the detached spawn for one test and answers the
// roots it was asked to build for.
func recordTestMapSpawns(t *testing.T) *[]string {
	t.Helper()
	var roots []string
	t.Cleanup(SetTestMapSpawnForTest(func(root string) { roots = append(roots, root) }))
	return &roots
}

// sameTestMapRoot reports whether two paths name one directory, symlinks
// resolved: the hook names the repo by its resolved root.
func sameTestMapRoot(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func TestPostMergeSweep_StartsTheTestMapBuildWhereCommitMutationIsDeclared(t *testing.T) {
	roots := recordTestMapSpawns(t)
	mainRepo, mergedWT, _ := postMergeRepo(t, "aphrollo.toml", commitMutationToml)

	var out, errb bytes.Buffer
	PostMergeSweep(mainRepo, &out, &errb)

	if len(*roots) != 1 || !sameTestMapRoot((*roots)[0], mainRepo) {
		t.Fatalf("builds started for %v, want exactly one, for %s", *roots, mainRepo)
	}
	if _, err := os.Stat(mergedWT); err != nil {
		t.Errorf("the sweep ran without prune-lanes-on-merge: the merged worktree is gone (%v)", err)
	}
	if out.String() != "" || errb.String() != "" {
		t.Errorf("stdout %q stderr %q, want the build start silent", out.String(), errb.String())
	}
}

func TestPostMergeSweep_StartsNoTestMapBuildWithoutTheKey(t *testing.T) {
	roots := recordTestMapSpawns(t)
	for name, body := range map[string]string{
		"no key":         "[aphrollo]\nundercover = true\n",
		"the key is off": "[aphrollo]\nmutants-at-commit = false\n",
		"a refused key":  "[aphrollo]\nmutants-at-commit = \"ci\"\n",
		"only the sweep": optInToml,
	} {
		mainRepo, _, _ := postMergeRepo(t, "aphrollo.toml", body)
		var out, errb bytes.Buffer
		PostMergeSweep(mainRepo, &out, &errb)
		if len(*roots) != 0 {
			t.Errorf("%s: a build was started for %v", name, *roots)
		}
	}
}

func TestPostMergeSweep_TheBuildAndTheSweepAreIndependentKeys(t *testing.T) {
	roots := recordTestMapSpawns(t)
	mainRepo, mergedWT, _ := postMergeRepo(t, "aphrollo.toml", optInToml+"mutants-at-commit = true\n")

	var out, errb bytes.Buffer
	pruned := PostMergeSweep(mainRepo, &out, &errb)

	if len(*roots) != 1 {
		t.Errorf("builds started = %d, want 1", len(*roots))
	}
	if _, err := os.Stat(mergedWT); !os.IsNotExist(err) || len(pruned) != 1 {
		t.Errorf("the lane was not swept (err %v, pruned %v) although prune-lanes-on-merge is declared", err, pruned)
	}
}

func TestPostMergeSweep_StartsNoBuildOutsideARepository(t *testing.T) {
	roots := recordTestMapSpawns(t)
	var out, errb bytes.Buffer
	PostMergeSweep(t.TempDir(), &out, &errb)
	if len(*roots) != 0 {
		t.Errorf("a build was started for %v outside a repository", *roots)
	}
}

// A merge concluded by hand after a conflict never fires post-merge, so the
// same build starts from post-commit for that one shape and for no other.
func TestPostCommitMergeSweep_StartsTheBuildForAConflictResolvedMergeOnly(t *testing.T) {
	roots := recordTestMapSpawns(t)
	mainRepo := t.TempDir()
	gitInit(t, mainRepo)
	gitDo(t, mainRepo, "checkout", "-q", "-B", "main")
	write(t, mainRepo, "aphrollo.toml", commitMutationToml)
	write(t, mainRepo, "shared.go", "package main\n\n// base\n")
	gitDo(t, mainRepo, "add", "-A")
	gitDo(t, mainRepo, "commit", "-qm", "init")

	laneWT := filepath.Join(t.TempDir(), "conflict")
	gitDo(t, mainRepo, "worktree", "add", "-q", "-b", "lane/conflict", laneWT)
	write(t, laneWT, "shared.go", "package main\n\n// lane\n")
	gitDo(t, laneWT, "add", "-A")
	gitDo(t, laneWT, "commit", "-qm", "lane change")
	write(t, mainRepo, "shared.go", "package main\n\n// trunk\n")
	gitDo(t, mainRepo, "add", "-A")
	gitDo(t, mainRepo, "commit", "-qm", "trunk change")

	var out, errb bytes.Buffer
	PostCommitMergeSweep(mainRepo, &out, &errb)
	if len(*roots) != 0 {
		t.Fatalf("a build was started for an ordinary commit: %v", *roots)
	}

	mergeCmd := exec.Command(gitBinary(), "merge", "--no-ff", "-m", "merge lane/conflict", "lane/conflict")
	mergeCmd.Dir = mainRepo
	_ = mergeCmd.Run() // the conflict is the fixture; resolved and committed below
	write(t, mainRepo, "shared.go", "package main\n\n// resolved\n")
	gitDo(t, mainRepo, "add", "-A")
	gitDo(t, mainRepo, "commit", "-qm", "merge lane/conflict resolved")

	PostCommitMergeSweep(mainRepo, &out, &errb)
	if len(*roots) != 1 || !sameTestMapRoot((*roots)[0], mainRepo) {
		t.Errorf("builds started for %v, want exactly one, for %s", *roots, mainRepo)
	}
}
