package tdd

import (
	"testing"
)

// Issue #726 on the edit-hook side. A conflicted `git merge main` in a lane
// stages everything trunk gained since the fork, and the Bash harvest read
// those paths as this command's edits and ran suites over them in the lane.
// Trunk's work was judged when it landed; a trunk sync into a lane judges
// only what the lane contributes, the same scope the merge gate uses.

// laneWithTrunkAhead returns a primary on main and a lane on lane/x that both
// edited docs/decisions.md differently (so a sync conflicts and stops with
// MERGE_HEAD), with trunk also carrying a source change the lane never made.
func laneWithTrunkAhead(t *testing.T) (primary, lane string) {
	t.Helper()
	primary, lane = goPrimaryWithLane(t)
	write(t, lane, "docs/decisions.md", "# lane decision\n")
	gitDo(t, lane, "add", "-A")
	gitDo(t, lane, "commit", "-qm", "lane docs")
	write(t, primary, "docs/decisions.md", "# trunk decision\n")
	write(t, primary, "internal/b/b.go", "package b\n\nfunc B() int { return 1 }\n")
	gitDo(t, primary, "add", "-A")
	gitDo(t, primary, "commit", "-qm", "trunk change")
	return primary, lane
}

func TestPostBash_ConflictedTrunkSyncInALaneRunsNoSuiteOverTrunksPaths(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, lane := laneWithTrunkAhead(t)

	cmd := "git merge main"
	PreBash(bashPayload(t, "s726", lane, cmd))
	if _, err := git(lane, "merge", "main"); err == nil {
		t.Fatal("setup: expected the sync to conflict, but it succeeded cleanly")
	}

	var dirs []string
	text := PostBash(bashPayload(t, "s726", lane, cmd), recordSuiteDirs(&dirs))
	if len(dirs) != 0 {
		t.Fatalf("a trunk sync into a docs-only lane must run no suite over trunk's paths, ran in %v (%q)", dirs, text)
	}
}

// The lane's own contribution is still this command's to judge: a source file
// both sides changed comes out of the sync conflicted, differs from trunk's
// copy, and keeps its suite run in the lane.
func TestPostBash_ConflictedTrunkSyncStillRunsTheLanesOwnConflictedSource(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, lane := goPrimaryWithLane(t)
	write(t, lane, "internal/a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	gitDo(t, lane, "add", "-A")
	gitDo(t, lane, "commit", "-qm", "lane source")
	write(t, primary, "internal/a/a.go", "package a\n\nfunc A() int { return 2 }\n")
	gitDo(t, primary, "add", "-A")
	gitDo(t, primary, "commit", "-qm", "trunk source")

	cmd := "git merge main"
	PreBash(bashPayload(t, "s726own", lane, cmd))
	if _, err := git(lane, "merge", "main"); err == nil {
		t.Fatal("setup: expected the sync to conflict, but it succeeded cleanly")
	}

	var dirs []string
	text := PostBash(bashPayload(t, "s726own", lane, cmd), recordSuiteDirs(&dirs))
	if len(dirs) == 0 {
		t.Fatalf("the lane's own conflicted source must still be judged, got no run (%q)", text)
	}
}

// Concluding a merge moves every path the merge staged out of the dirty set.
// Those paths were not edited by the concluding command; the pre-commit gate
// just ran the pre-merge routine over them. Reading them as edits re-ran
// their suites after every conflicted merge, on the primary and in lanes.

func TestPostBash_ConcludingAConflictedMergeOnThePrimaryRunsNoSuite(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, lane := goPrimaryWithLane(t)
	write(t, lane, "doc.go", "package m\n\nfunc Lane() int { return 1 }\n")
	write(t, lane, "internal/a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	gitDo(t, lane, "add", "-A")
	gitDo(t, lane, "commit", "-qm", "lane change")
	write(t, primary, "doc.go", "package m\n\nfunc Primary() int { return 2 }\n")
	gitDo(t, primary, "add", "-A")
	gitDo(t, primary, "commit", "-qm", "primary change")
	if _, err := git(primary, "merge", "--no-ff", "lane/x"); err == nil {
		t.Fatal("setup: expected the merge to conflict, but it succeeded cleanly")
	}
	write(t, primary, "doc.go", "package m\n\nfunc Merged() int { return 3 }\n")
	gitDo(t, primary, "add", "-A")

	cmd := "git commit --no-edit"
	PreBash(bashPayload(t, "s726c", primary, cmd))
	gitDo(t, primary, "commit", "--no-edit")

	var dirs []string
	text := PostBash(bashPayload(t, "s726c", primary, cmd), recordSuiteDirs(&dirs))
	if len(dirs) != 0 {
		t.Fatalf("concluding a merge must not re-run the merged paths' suites, ran in %v (%q)", dirs, text)
	}
}

func TestPostBash_ConcludingAConflictedTrunkSyncInALaneRunsNoSuite(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, lane := laneWithTrunkAhead(t)
	if _, err := git(lane, "merge", "main"); err == nil {
		t.Fatal("setup: expected the sync to conflict, but it succeeded cleanly")
	}
	write(t, lane, "docs/decisions.md", "# both decisions\n")
	gitDo(t, lane, "add", "-A")

	cmd := "git commit --no-edit"
	PreBash(bashPayload(t, "s726l", lane, cmd))
	gitDo(t, lane, "commit", "--no-edit")

	var dirs []string
	text := PostBash(bashPayload(t, "s726l", lane, cmd), recordSuiteDirs(&dirs))
	if len(dirs) != 0 {
		t.Fatalf("concluding a trunk sync must not re-run trunk's paths, ran in %v (%q)", dirs, text)
	}
}

// A command that concludes a merge AND edits a file it leaves uncommitted
// still made that edit, and the harvest still judges that path.
func TestPostBash_ConcludingAMergeStillRunsAPathTheCommandEdited(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, lane := laneWithTrunkAhead(t)
	if _, err := git(lane, "merge", "main"); err == nil {
		t.Fatal("setup: expected the sync to conflict, but it succeeded cleanly")
	}
	write(t, lane, "docs/decisions.md", "# both decisions\n")
	gitDo(t, lane, "add", "-A")
	write(t, lane, "doc.go", "package m\n\nfunc Draft() int { return 1 }\n")

	cmd := "sed -i s/Draft/Final/ doc.go && git commit --no-edit"
	PreBash(bashPayload(t, "s726e", lane, cmd))
	write(t, lane, "doc.go", "package m\n\nfunc Final() int { return 12 }\n")
	gitDo(t, lane, "commit", "--no-edit")

	var dirs []string
	text := PostBash(bashPayload(t, "s726e", lane, cmd), recordSuiteDirs(&dirs))
	if len(dirs) == 0 {
		t.Fatalf("an edit the concluding command made and left dirty must still be judged, got no run (%q)", text)
	}
}
