package lawgate

import (
	"os"
	"path/filepath"
	"testing"
)

// twinsLaw is a co-change law over every .go file, judged on the staged set.
const twinsLaw = `
name = "twins"
description = "a twin pair must change together"
severity = "deny"

[scope]
changed = "staged"
include = ["**/*.go"]

[matcher]
kind = "co-change"
`

// twinRepo commits a.go, whose A is marked as the twin of b.go's B, with a
// body long enough that a moved copy stays well above git's rename
// similarity threshold even after a one-line edit.
func twinRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "twins.toml"), twinsLaw)
	mustWrite(t, filepath.Join(root, "a.go"), twinA("1"))
	mustWrite(t, filepath.Join(root, "b.go"), "package a\n\nfunc B() {}\n")
	gitAddAll(t, root)
	commitAll(t, root)
	return root
}

func twinA(v string) string {
	return "package a\n\n// twin: b.go#B\nfunc A() int {\n\tx := " + v + "\n\ty := x + 1\n\tz := y * 2\n\tw := z - 3\n\treturn w\n}\n\n" +
		"func other() int {\n\ta := 1\n\tb := a + 1\n\tc := b * 2\n\td := c - 3\n\treturn d\n}\n"
}

func moveA(t *testing.T, root, body string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, "a.go")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "sub", "a.go"), body)
	gitAddAll(t, root)
}

// A twin-marked file moved unchanged is not a change to the marked
// declaration: its content is the same, only its path is new. The commit
// gate reads the moved file's pre-image from where the file came FROM, so
// the move passes without an escape.
func TestRatchetStage_AMovedTwinIsNotAChangedTwin(t *testing.T) {
	root := twinRepo(t)
	moveA(t, root, twinA("1"))
	if res := ratchetStage("precommit", root); res.Blocked {
		t.Fatalf("a pure move of a.go was read as a change to its twin-marked A: %s", res.Message)
	}
}

// Following the rename must not hide a real change: a moved file whose
// marked declaration also changed still needs its twin.
func TestRatchetStage_AMovedAndEditedTwinStillNeedsItsTwin(t *testing.T) {
	root := twinRepo(t)
	moveA(t, root, twinA("2"))
	if res := ratchetStage("precommit", root); !res.Blocked {
		t.Fatal("a.go moved AND its marked A changed, but its twin b.go did not — the co-change law must block")
	}
}

// The merge gate stages the lane's whole diff against trunk, and a lane
// that moved a twin-marked file unchanged must pass it the same way the
// lane's own commit did.
func TestRatchetStage_AMergedLaneThatMovedATwinUnchangedPasses(t *testing.T) {
	root := twinRepo(t)
	gitDo(t, root, "checkout", "-q", "-b", "lane")
	moveA(t, root, twinA("1"))
	gitDo(t, root, "commit", "-q", "-m", "move a.go")
	gitDo(t, root, "checkout", "-q", "-")
	gitDo(t, root, "merge", "-q", "--no-ff", "--no-commit", "lane")
	if res := ratchetStage("premerge", root); res.Blocked {
		t.Fatalf("merging a lane that only moved a.go was read as a change to its twin-marked A: %s", res.Message)
	}
}
