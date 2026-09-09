package tdd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// postMergeRepo is pruneRepo's shape — a landed lane and a fresh one — with
// the repo's manifest committed BEFORE any lane exists, so every worktree of
// it carries the same declaration and every tree stays CLEAN. Writing the
// manifest into a lane afterwards would make that worktree dirty, and the
// sweep's uncommitted-work guard rather than the opt-in would be what spared
// it: the test would pass while proving nothing about the key.
func postMergeRepo(t *testing.T, manifest, body string) (mainRepo, mergedWT, freshWT string) {
	t.Helper()
	mainRepo = t.TempDir()
	gitInit(t, mainRepo)
	gitDo(t, mainRepo, "checkout", "-q", "-B", "main")
	write(t, mainRepo, "main.go", "package main\n")
	if manifest != "" {
		write(t, mainRepo, manifest, body)
	}
	gitDo(t, mainRepo, "add", "-A")
	gitDo(t, mainRepo, "commit", "-q", "-m", "init")

	gitDo(t, mainRepo, "branch", "lane/merged")
	mergedWT = filepath.Join(t.TempDir(), "merged")
	gitDo(t, mainRepo, "worktree", "add", "-q", mergedWT, "lane/merged")
	write(t, mergedWT, "landed.go", "package main\n\n// landed\n")
	gitDo(t, mergedWT, "add", "-A")
	gitDo(t, mergedWT, "commit", "-qm", "lane work")
	gitDo(t, mainRepo, "merge", "-q", "--no-ff", "-m", "merge lane/merged", "lane/merged")

	freshWT = filepath.Join(t.TempDir(), "fresh")
	gitDo(t, mainRepo, "worktree", "add", "-q", "-b", "lane/fresh", freshWT)

	return mainRepo, mergedWT, freshWT
}

const optInToml = "[aphrollo]\nprune-lanes-on-merge = true\n"

// A repo that declared the key gets the same guarded sweep `aphrollo
// workspace merge` runs, now from a plain `git merge` too — the gap that let
// a hand-written post-merge hook exist on the box at all (issue #582).
func TestPostMergeSweep_PrunesALandedLaneWhenTheRepoOptsIn(t *testing.T) {
	mainRepo, mergedWT, freshWT := postMergeRepo(t, "aphrollo.toml", optInToml)

	var out, errb bytes.Buffer
	pruned := PostMergeSweep(mainRepo, &out, &errb)

	if _, err := os.Stat(mergedWT); !os.IsNotExist(err) {
		t.Fatalf("lane/merged's worktree at %s must be swept, got err=%v", mergedWT, err)
	}
	if _, err := os.Stat(freshWT); err != nil {
		t.Fatalf("lane/fresh's worktree at %s must survive, got err=%v", freshWT, err)
	}
	if len(pruned) != 1 || pruned[0].Branch != "lane/merged" {
		t.Fatalf("pruned = %+v, want exactly lane/merged", pruned)
	}
	if !strings.Contains(out.String(), "lane/merged") {
		t.Fatalf("stdout = %q, want it to name the swept lane", out.String())
	}
}

// The Cargo spelling of the same declaration, read with the precedence
// `mutants-at-merge` uses: a Rust workspace declares it in its own manifest
// rather than carrying a second file for one key.
func TestPostMergeSweep_ReadsTheCargoWorkspaceSpelling(t *testing.T) {
	mainRepo, mergedWT, _ := postMergeRepo(t, "Cargo.toml",
		"[workspace]\nmembers = []\n\n[workspace.metadata.aphrollo]\nprune-lanes-on-merge = true\n")

	var out, errb bytes.Buffer
	pruned := PostMergeSweep(mainRepo, &out, &errb)

	if _, err := os.Stat(mergedWT); !os.IsNotExist(err) {
		t.Fatalf("lane/merged's worktree at %s must be swept, got err=%v", mergedWT, err)
	}
	if len(pruned) != 1 || pruned[0].Branch != "lane/merged" {
		t.Fatalf("pruned = %+v, want exactly lane/merged", pruned)
	}
}

// core.hooksPath is global: this hook fires in EVERY repo on the box, and on
// `git pull` as much as on `git merge`. A repo that never asked for a sweep
// must get NOTHING — no removal, and no output either, since a line about
// lanes in a repo that has none is how an operator comes to read the tool as
// the thing deleting their work.
func TestPostMergeSweep_IsInertWithoutTheKey(t *testing.T) {
	mainRepo, mergedWT, freshWT := postMergeRepo(t, "", "")

	var out, errb bytes.Buffer
	pruned := PostMergeSweep(mainRepo, &out, &errb)

	if _, err := os.Stat(mergedWT); err != nil {
		t.Fatalf("an un-opted-in repo's landed lane at %s must survive, got err=%v", mergedWT, err)
	}
	if _, err := os.Stat(freshWT); err != nil {
		t.Fatalf("lane/fresh at %s must survive, got err=%v", freshWT, err)
	}
	if len(pruned) != 0 {
		t.Fatalf("pruned = %+v, want nothing swept in a repo that declared nothing", pruned)
	}
	if out.String() != "" || errb.String() != "" {
		t.Fatalf("stdout = %q, stderr = %q, want both silent", out.String(), errb.String())
	}
}

// The key written false is a repo that considered the sweep and refused it —
// exactly as strong a "no" as never declaring it.
func TestPostMergeSweep_IsInertWhenTheKeyIsFalse(t *testing.T) {
	mainRepo, mergedWT, _ := postMergeRepo(t, "aphrollo.toml", "[aphrollo]\nprune-lanes-on-merge = false\n")

	var out, errb bytes.Buffer
	pruned := PostMergeSweep(mainRepo, &out, &errb)

	if _, err := os.Stat(mergedWT); err != nil {
		t.Fatalf("a repo declaring false must keep its landed lane at %s, got err=%v", mergedWT, err)
	}
	if len(pruned) != 0 {
		t.Fatalf("pruned = %+v, want nothing swept", pruned)
	}
	if out.String() != "" || errb.String() != "" {
		t.Fatalf("stdout = %q, stderr = %q, want both silent", out.String(), errb.String())
	}
}

// The hook fires INSIDE the worktree the merge ran in, which may itself hold
// a branch that is now merged — a lane that just merged main into itself, or
// `git pull` in the lane. Sweeping it would pull the ground out from under
// the git process still running the hook, so it is excluded by path whatever
// its branch's state.
func TestPostMergeSweep_NeverSweepsTheWorktreeTheHookFiredIn(t *testing.T) {
	_, mergedWT, freshWT := postMergeRepo(t, "aphrollo.toml", optInToml)

	var out, errb bytes.Buffer
	pruned := PostMergeSweep(mergedWT, &out, &errb)

	if _, err := os.Stat(mergedWT); err != nil {
		t.Fatalf("the worktree the hook fired in at %s must survive, got err=%v", mergedWT, err)
	}
	if _, err := os.Stat(freshWT); err != nil {
		t.Fatalf("lane/fresh at %s must survive, got err=%v", freshWT, err)
	}
	if len(pruned) != 0 {
		t.Fatalf("pruned = %+v, want nothing — the only landed lane is the one the hook fired in", pruned)
	}
}

// A directory under no repo at all is where this hook runs most often by
// accident (a `git pull` in some clone the box happens to hold): it must say
// nothing and do nothing rather than error.
func TestPostMergeSweep_SaysNothingOutsideARepo(t *testing.T) {
	var out, errb bytes.Buffer
	if pruned := PostMergeSweep(t.TempDir(), &out, &errb); len(pruned) != 0 {
		t.Fatalf("pruned = %+v, want nothing outside a repo", pruned)
	}
	if out.String() != "" || errb.String() != "" {
		t.Fatalf("stdout = %q, stderr = %q, want both silent", out.String(), errb.String())
	}
}

// A gate that exists only as a subcommand never runs: git looks for a hook by
// its exact name, so both installers must write `post-merge` pointing at the
// `postmerge` subcommand.
func TestPostMergeSweep_BothInstallersWriteThePostMergeShim(t *testing.T) {
	found := false
	for _, h := range gitGateHooks {
		if h.name == "post-merge" {
			found = true
			if h.sub != "postmerge" {
				t.Fatalf("the global gate's post-merge shim calls %q, want postmerge", h.sub)
			}
		}
	}
	if !found {
		t.Fatal("the global gate must install a post-merge hook")
	}
	perRepo := false
	for _, h := range perRepoHooks {
		if h.name == "post-merge" {
			perRepo = true
			if h.sub != "postmerge" {
				t.Fatalf("the per-repo post-merge shim calls %q, want postmerge", h.sub)
			}
		}
	}
	if !perRepo {
		t.Fatal("per-repo install must write the post-merge hook too")
	}
}
