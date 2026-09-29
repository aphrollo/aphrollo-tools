package mutation

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gremlins copies the module it measures into a workdir of its own, `.git`
// included, and a mutant's test process runs git there. When the lane is a
// linked worktree its `.git` is a file naming the lane's own git dir, so a
// git command run in that copy acts on the lane's index and HEAD: the same
// hazard as #972, one copy removed. The measurement therefore runs gremlins
// in a copy that has a git dir of its own.
func TestMeasureGoLane_AMutantsGitResetInGremlinsCopyLeavesTheLanesGitDirAlone(t *testing.T) {
	primary, base := measurableTorqueLane(t)
	lane := filepath.Join(t.TempDir(), "lane")
	gitDo(t, primary, "worktree", "add", "-q", "-b", "lane", lane, "HEAD")
	headBefore := gitOutT(t, lane, "rev-parse", "HEAD")
	noSurvivors := `{"files":[{"file_name":"torque/torque.go","mutations":[
		{"type":"ARITHMETIC_BASE","status":"KILLED","line":5,"column":16}]}]}`

	stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		// What gremlins does: copy the tree it was pointed at, .git and all.
		workdir := filepath.Join(t.TempDir(), "workdir")
		if out, err := exec.Command("cp", "-a", c.Dir, workdir).CombinedOutput(); err != nil {
			t.Fatalf("cp -a: %v: %s", err, out)
		}
		// What the mutant does there.
		if out, err := exec.Command("git", "-C", workdir, "reset", "--hard", "HEAD~1").CombinedOutput(); err != nil {
			t.Fatalf("git reset in the copy: %v: %s", err, out)
		}
		mustWrite(t, gremlinsReportPath(lane), noSurvivors)
		return 0, nil
	})

	if _, err := MeasureLane(lane, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base}); err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}

	if got := gitOutT(t, lane, "rev-parse", "HEAD"); got != headBefore {
		t.Errorf("the lane's HEAD moved from %s to %s", strings.TrimSpace(headBefore), strings.TrimSpace(got))
	}
	if status := gitOutT(t, lane, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("the lane's index or tree changed: git status says\n%s", status)
	}
	if _, err := os.Stat(filepath.Join(lane, "torque", "torque.go")); err != nil {
		t.Errorf("the lane lost torque.go: %v", err)
	}
}
