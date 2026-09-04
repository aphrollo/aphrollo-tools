package workspace

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestShipPlan_RendersAllStages(t *testing.T) {
	repo := repoWithRemote(t)
	writeFile(t, repo, "f.txt", "x\n")
	s, err := ShipPlan(targetFor(repo, "main"), ShipRequest{Message: "land it", StageAll: true})
	if err != nil {
		t.Fatalf("ShipPlan: %v", err)
	}
	dry := s.Render(false)
	for _, want := range []string{"commit -> push -> pr", "land it", "push", "pr", "--dry"} {
		if !strings.Contains(dry, want) {
			t.Errorf("ship dry-run missing %q:\n%s", want, dry)
		}
	}
}

func TestShipPlan_PropagatesStageError(t *testing.T) {
	// An empty message fails at the commit stage, before any side effect.
	if _, err := ShipPlan(targetFor("/x", "main"), ShipRequest{Message: ""}); err == nil {
		t.Fatal("expected ShipPlan to reject an empty commit message")
	}
}

// TestShipApply_ReportsAheadCountFromTheCommitItJustCreated is issue #162:
// ShipPlan resolves the push stage's ahead-count BEFORE Ship.Apply's commit
// stage creates the commit being shipped, so the printed receipt is stale.
// Here the branch starts already pushed and 0 commits ahead of its upstream;
// after ship commits one new change, the push line must say "(1 commit(s))",
// not the Plan-time snapshot of 0 (rendered as no count at all).
func TestShipApply_ReportsAheadCountFromTheCommitItJustCreated(t *testing.T) {
	repo := repoWithRemote(t)
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("checkout", "-q", "-b", "feat/y")
	writeFile(t, repo, "f.txt", "x\n")
	run("add", ".")
	run("commit", "-qm", "initial")
	run("push", "-q", "-u", "origin", "feat/y") // already pushed, 0 ahead

	stubGH(t,
		// An already-open PR: both push's reuse and the pr stage's reuse hit
		// this, so neither tries to create one.
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 9, URL: "https://github.com/o/r/pull/9", State: "OPEN"}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) {
			t.Fatal("an existing open PR must be reused, never re-created")
			return nil, nil
		},
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })

	writeFile(t, repo, "g.txt", "y\n") // the change ship itself will commit

	s, err := ShipPlan(targetFor(repo, "feat/y"), ShipRequest{Message: "followup", StageAll: true, NoVerify: true})
	if err != nil {
		t.Fatalf("ShipPlan: %v", err)
	}
	var out, errb bytes.Buffer
	if err := s.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "pushed feat/y -> origin (1 commit(s))") {
		t.Errorf("push receipt should report 1 commit ahead (the one ship just created), got:\n%s", out.String())
	}
}
