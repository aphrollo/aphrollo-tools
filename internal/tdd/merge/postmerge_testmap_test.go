package merge

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// After a merge lands, the per-function test maps are rebuilt in the
// background in a repo that declares mutants-at-commit and nowhere else. The
// launch is a seam: no test here starts a detached process.

func declaredRepo(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	gitDo(t, root, "checkout", "-q", "-B", "main")
	write(t, root, "aphrollo.toml", body)
	write(t, root, "main.go", "package main\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-q", "-m", "init")
	return root
}

func recordSpawns(t *testing.T) *[]string {
	t.Helper()
	var roots []string
	t.Cleanup(SetTestMapSpawnForTest(func(root string) { roots = append(roots, root) }))
	return &roots
}

func sameRepoDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func TestTestMapBuildWanted_OnlyWhereTheKeyIsTrue(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		body string
		want bool
	}{
		"true":             {"[aphrollo]\nmutants-at-commit = true\n", true},
		"false":            {"[aphrollo]\nmutants-at-commit = false\n", false},
		"absent":           {"[aphrollo]\nundercover = true\n", false},
		"a refused value":  {"[aphrollo]\nmutants-at-commit = \"ci\"\n", false},
		"a retired key":    {"[aphrollo]\nmutants-at-commit = true\nmutation-receipt = true\n", false},
		"only the sweep":   {"[aphrollo]\nprune-lanes-on-merge = true\n", false},
		"both keys are on": {"[aphrollo]\nmutants-at-commit = true\nprune-lanes-on-merge = true\n", true},
	} {
		root := t.TempDir()
		write(t, root, "aphrollo.toml", tc.body)
		if got := testMapBuildWanted(root); got != tc.want {
			t.Errorf("%s: testMapBuildWanted = %v, want %v", name, got, tc.want)
		}
	}
	if testMapBuildWanted(t.TempDir()) {
		t.Error("a directory declaring nothing wants a build")
	}
}

func TestStartTestMapBuild_StartsWhereWantedAndOnlyThere(t *testing.T) {
	roots := recordSpawns(t)
	startTestMapBuild(declaredRepo(t, "[aphrollo]\nundercover = true\n"))
	if len(*roots) != 0 {
		t.Fatalf("a build was started for a repo that declares nothing: %v", *roots)
	}
	wanted := declaredRepo(t, "[aphrollo]\nmutants-at-commit = true\n")
	startTestMapBuild(wanted)
	if len(*roots) != 1 || (*roots)[0] != wanted {
		t.Errorf("builds = %v, want exactly one, for %s", *roots, wanted)
	}
}

func TestPostMergeSweep_StartsTheBuildInADeclaredRepoAndStaysSilent(t *testing.T) {
	roots := recordSpawns(t)
	repo := declaredRepo(t, "[aphrollo]\nmutants-at-commit = true\n")
	var out, errb bytes.Buffer
	PostMergeSweep(repo, &out, &errb)
	if len(*roots) != 1 || !sameRepoDir((*roots)[0], repo) {
		t.Errorf("builds = %v, want one for %s", *roots, repo)
	}
	if out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("stdout %q stderr %q, want silence", out.String(), errb.String())
	}
}

func TestPostMergeSweep_StartsNoBuildWithoutTheKeyOrOutsideARepo(t *testing.T) {
	roots := recordSpawns(t)
	var out, errb bytes.Buffer
	PostMergeSweep(declaredRepo(t, "[aphrollo]\nmutants-at-commit = false\n"), &out, &errb)
	PostMergeSweep(t.TempDir(), &out, &errb)
	if len(*roots) != 0 {
		t.Errorf("builds = %v, want none", *roots)
	}
}

// A merge concluded by hand after a conflict never fires post-merge, so the
// same build starts from post-commit for that one shape and for no other.
func TestPostCommitMergeSweep_TheBuildStartsForAConflictResolvedMergeAndNoOtherCommit(t *testing.T) {
	roots := recordSpawns(t)
	repo := declaredRepo(t, "[aphrollo]\nmutants-at-commit = true\n")
	write(t, repo, "shared.go", "package main\n\n// base\n")
	gitDo(t, repo, "add", "-A")
	gitDo(t, repo, "commit", "-qm", "shared")
	laneWT := filepath.Join(t.TempDir(), "conflict")
	gitDo(t, repo, "worktree", "add", "-q", "-b", "lane/conflict", laneWT)
	write(t, laneWT, "shared.go", "package main\n\n// lane\n")
	gitDo(t, laneWT, "add", "-A")
	gitDo(t, laneWT, "commit", "-qm", "lane change")
	write(t, repo, "shared.go", "package main\n\n// trunk\n")
	gitDo(t, repo, "add", "-A")
	gitDo(t, repo, "commit", "-qm", "trunk change")

	var out, errb bytes.Buffer
	PostCommitMergeSweep(repo, &out, &errb)
	if len(*roots) != 0 {
		t.Fatalf("a build was started for an ordinary commit: %v", *roots)
	}
	mergeCmd := exec.Command(gitBinary(), "merge", "--no-ff", "-m", "merge lane/conflict", "lane/conflict")
	mergeCmd.Dir = repo
	_ = mergeCmd.Run() // the conflict is the fixture; resolved and committed below
	write(t, repo, "shared.go", "package main\n\n// resolved\n")
	gitDo(t, repo, "add", "-A")
	gitDo(t, repo, "commit", "-qm", "merge lane/conflict resolved")

	PostCommitMergeSweep(repo, &out, &errb)
	if len(*roots) != 1 || !sameRepoDir((*roots)[0], repo) {
		t.Errorf("builds = %v, want exactly one, for %s", *roots, repo)
	}
}

func TestPostCommitMergeSweep_AnUndeclaredRepoIsLeftAloneWhateverTheCommit(t *testing.T) {
	roots := recordSpawns(t)
	repo := declaredRepo(t, "[aphrollo]\nundercover = true\n")
	var out, errb bytes.Buffer
	PostCommitMergeSweep(repo, &out, &errb)
	if len(*roots) != 0 || out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("builds %v stdout %q stderr %q, want nothing", *roots, out.String(), errb.String())
	}
}

// The real spawn declines from a Go test binary, which would answer the verb
// by running its whole suite, and never reaches the launch.
func TestSpawnTestMapBuild_DeclinesFromATestBinary(t *testing.T) {
	launched := 0
	prev := testMapLaunchFn
	testMapLaunchFn = func(*exec.Cmd) bool { launched++; return true }
	t.Cleanup(func() { testMapLaunchFn = prev })
	spawnTestMapBuild(t.TempDir())
	if launched != 0 {
		t.Errorf("the spawn launched %d command(s) from a test binary", launched)
	}
}

func TestTestMapCommand_RunsTheVerbInTheRepoWithACleanEnvironment(t *testing.T) {
	t.Parallel()
	cmd := testMapCommand("/usr/local/bin/aphrollo", "/repo")
	if want := []string{"/usr/local/bin/aphrollo", "gate", "mutants", "testmap"}; !slices.Equal(cmd.Args, want) {
		t.Errorf("argv = %v, want %v", cmd.Args, want)
	}
	if cmd.Dir != "/repo" {
		t.Errorf("dir = %q, want /repo", cmd.Dir)
	}
	for _, want := range []string{"CI=1", "NO_COLOR=1"} {
		if !slices.Contains(cmd.Env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
}

func TestLaunchDetached_ACommandThatCannotStartIsNotLaunched(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no-such-binary")
	if launchDetached(exec.Command(missing)) {
		t.Error("a command with no binary was reported launched")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("the launch created the binary")
	}
}
