package tdd

import (
	"path/filepath"
	"strings"
	"testing"
)

// The commit gate judges what is IN the commit. An untracked file is part of
// no commit — another session's scaffolding in a shared checkout, a scratch
// note, a generator's leftovers — and rejecting a merge over one is a
// rejection nobody can clear by staging anything.
func TestRatchetStageIgnoresAnUntrackedOffendingFile(t *testing.T) {
	root := lawTree(t, "deny")
	gitAddAll(t, root)
	commitAll(t, root)

	mustWrite(t, filepath.Join(root, "crates", "b", "src", "scratch.rs"),
		"let b = y.clamp(0.0, 1.0);\n")

	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("an untracked file is part of no commit: %s", res.Message)
	}
}

// Staging it is the moment it becomes part of a commit, and then it answers.
func TestRatchetStageJudgesTheSameFileOnceItIsStaged(t *testing.T) {
	root := lawTree(t, "deny")
	gitAddAll(t, root)
	commitAll(t, root)

	mustWrite(t, filepath.Join(root, "crates", "b", "src", "scratch.rs"),
		"let b = y.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)

	res := ratchetStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "scratch.rs") {
		t.Fatalf("a staged offence is part of the commit and must reject: %+v", res)
	}
}

// The restriction must not turn into "scan nothing": a tracked file that is
// already committed is still measured, so an offence added to one in an
// earlier commit still shows up against the ceiling.
func TestRatchetStageStillJudgesTrackedFilesInHead(t *testing.T) {
	root := lawTree(t, "deny")
	gitAddAll(t, root)
	commitAll(t, root)

	mustWrite(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")
	gitAddAll(t, root)
	commitAll(t, root)

	res := ratchetStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "lib.rs") {
		t.Fatalf("a tracked file over its ceiling must reject: %+v", res)
	}
}

// Pre-edit is the other side: the file being written may not exist in the
// index at all, and denying the write is the whole point of judging it there.
func TestRatchetAdvisoryStillDeniesAWriteToAnUntrackedFile(t *testing.T) {
	root := lawTree(t, "deny")
	gitAddAll(t, root)
	commitAll(t, root)

	path := filepath.Join(root, "crates", "a", "src", "brand_new.rs")
	raw := ratchetPayload(t, "Write", path, map[string]any{
		"content": "let b = y.clamp(0.0, 1.0);\n",
	})
	if d := RatchetAdvisory(raw); d.Action != Block {
		t.Fatalf("a write to a file git has never seen is still judged: %v %s", d.Action, d.Reason)
	}
}
