package precommit

import (
	"strings"
	"testing"
)

// A trunk sync into a lane judges the lane's own contribution: the staged
// set is the merged index against the incoming trunk tip (stagedDiffBase).
// The law stage read the pre-images of that same set against HEAD — the lane
// tip — so a test TRUNK had removed (already judged when it landed there)
// read as the lane removing it, in a file the lane had only added to, and a
// diff-scoped law refused the sync over work the lane never did. The staged
// set and its pre-images come from one base.
func TestRatchetStage_TrunkSyncReadsPreImagesAtTheTrunkTip(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	write(t, root, ".ratchet/laws/test_removed.toml", strings.Join([]string{
		`name        = "test_removed"`,
		`description = "A Go test present at the base and gone at the tip."`,
		`severity    = "deny"`,
		``,
		`[scope]`,
		`include = ["**/*_test.go"]`,
		``,
		`[matcher]`,
		`kind    = "symbol-removed"`,
		`pattern = "^func (Test\\w+)\\("`,
		``,
	}, "\n"))
	head := "package a\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) {}\n\n"
	middle := "func TestKeepOne(t *testing.T) {}\n\nfunc TestKeepTwo(t *testing.T) {}\n\nfunc TestKeepThree(t *testing.T) {}\n"
	write(t, root, "internal/a/a_test.go", head+middle)
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "law and tests")
	trunk := currentBranch(t, root)

	gitDo(t, root, "checkout", "-qb", "lane/work")
	write(t, root, "internal/a/a_test.go", head+middle+"\nfunc TestLane(t *testing.T) {}\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane adds a test")
	gitDo(t, root, "checkout", "-q", trunk)
	write(t, root, "internal/a/a_test.go", "package a\n\nimport \"testing\"\n\n"+middle)
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "trunk retires TestOld")
	gitDo(t, root, "checkout", "-q", "lane/work")
	gitDo(t, root, "merge", "--no-commit", "--no-ff", trunk)

	if res := ratchetStage("premerge", root); res.Blocked {
		t.Fatalf("a trunk sync must not answer for trunk's own removal:\n%s", res.Message)
	}
}
