package workspace

import (
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
	for _, want := range []string{"commit -> push -> pr", "land it", "push", "pr", "--apply"} {
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
