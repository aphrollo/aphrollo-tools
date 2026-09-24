package postedit

import (
	"path/filepath"
	"strings"
	"testing"
)

// Issue #769: a lane session's post-edit line named the merge-only PRIMARY
// checkout as its run root, and the run behind it really did execute there,
// on code the lane never wrote. A command whose writes the scanner cannot
// see and whose cd lands in the primary (a lane reading main, or the harness
// leaving the shell there) keeps its snapshot on the primary, and any
// unstaged change that shared tree picked up meanwhile was harvested as this
// command's edit. A merge-only primary takes merges, not edits: a change
// there that no merge explains is not a lane's edit, and running a suite on
// it reports a verdict about another tree under this session's name.
func TestPostBash_RunsNoSuiteForUnstagedPrimaryDirtTheCommandWasNotSeenWriting(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, lane := goPrimaryWithLane(t)
	cmd := "cd " + filepath.ToSlash(primary) + " && python3 tools/report.py"

	PreBash(bashPayload(t, "s769", lane, cmd))
	// Someone else's edit to a tracked source file in the shared primary,
	// left unstaged, lands while this command runs.
	write(t, primary, "doc.go", "package m\n\nfunc Elsewhere() int { return 9 }\n")

	var dirs []string
	text := PostBash(bashPayload(t, "s769", lane, cmd), recordSuiteDirs(&dirs))

	if len(dirs) != 0 {
		t.Fatalf("the suite ran in %v, want nowhere: an unexplained change in a merge-only primary is not this lane's edit (%q)", dirs, text)
	}
	if !strings.Contains(text, "doc.go") || !strings.Contains(text, "NOT tested") {
		t.Fatalf("the line must name the path it did not test and say the code was NOT tested, got %q", text)
	}
}

// A session that waived the wall edits the primary on purpose, so a change
// its command leaves there is its own edit and is judged where it landed.
func TestPostBash_StillRunsThePrimarysSuiteForASessionThatWaivedTheWall(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, _ := goPrimaryWithLane(t)
	if err := setPrimaryEdits("s769w", true); err != nil {
		t.Fatal(err)
	}
	cmd := "python3 tools/gen.py"

	PreBash(bashPayload(t, "s769w", primary, cmd))
	write(t, primary, "doc.go", "package m\n\nfunc Waived() int { return 3 }\n")

	var dirs []string
	text := PostBash(bashPayload(t, "s769w", primary, cmd), recordSuiteDirs(&dirs))

	if len(dirs) != 1 || dirs[0] != primary {
		t.Fatalf("a waived session's edit in the primary must be judged there, ran in %v (%q)", dirs, text)
	}
}

// Resolving a conflicted merge in the primary is the one edit that tree
// takes: the conflicted path is unmerged, not staged, and the command that
// writes the resolution is the merge's own work, judged where it landed.
func TestPostBash_StillRunsThePrimarysSuiteForAConflictResolvedMidMerge(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, lane := goPrimaryWithLane(t)
	write(t, lane, "doc.go", "package m\n\nfunc Lane() int { return 1 }\n")
	gitDo(t, lane, "add", "doc.go")
	gitDo(t, lane, "commit", "-qm", "lane change")
	write(t, primary, "doc.go", "package m\n\nfunc Primary() int { return 2 }\n")
	gitDo(t, primary, "add", "doc.go")
	gitDo(t, primary, "commit", "-qm", "primary change")
	if _, err := git(primary, "merge", "--no-ff", "lane/x"); err == nil {
		t.Fatal("setup: expected the merge to conflict, but it succeeded cleanly")
	}
	cmd := "python3 tools/resolve.py"

	PreBash(bashPayload(t, "s769m", primary, cmd))
	write(t, primary, "doc.go", "package m\n\nfunc Merged() int { return 3 }\n")

	var dirs []string
	text := PostBash(bashPayload(t, "s769m", primary, cmd), recordSuiteDirs(&dirs))

	if len(dirs) != 1 || dirs[0] != primary {
		t.Fatalf("a conflict resolved mid-merge in the primary must be judged there, ran in %v (%q)", dirs, text)
	}
}
