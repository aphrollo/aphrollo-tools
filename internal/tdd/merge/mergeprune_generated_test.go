package merge

import (
	"strings"
	"testing"
)

// fanpyp kept its merged L6 lane because gitignored
// frontend/e2e/.output/.last-run.json, written by the last playwright run, was
// newer than the lane's last commit. Test reports are generated output: they
// are made again by the next run and never hold a lane.
func TestPruneMergedLanes_GeneratedTestOutputDoesNotKeepALane(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	write(t, laneWT, ".gitignore", ".output/\ntest-results/\nplaywright-report/\n.last-run.json\n")
	gitDo(t, laneWT, "add", ".gitignore")
	gitDo(t, laneWT, "commit", "-qm", "ignore test output")
	gitDo(t, mainRepo, "merge", "-q", "--squash", "lane/squashed")
	gitDo(t, mainRepo, "commit", "-qm", "ignore test output (#9)")
	pruneLanePush(t, mainRepo, "lane/squashed")
	write(t, laneWT, "frontend/e2e/.output/.last-run.json", "{\"status\":\"passed\"}\n")
	write(t, laneWT, "frontend/test-results/a/trace.zip", "x\n")
	write(t, laneWT, "frontend/playwright-report/index.html", "x\n")
	write(t, laneWT, "frontend/.last-run.json", "{}\n")
	pruneAgeLaneGit(t, laneWT)

	pruned, _, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 1 {
		t.Fatalf("pruned = %+v, stderr %q: generated test output must not hold a lane", pruned, errs)
	}
}

// A secret beside generated output still keeps the lane: the list names
// generated output only.
func TestPruneMergedLanes_ASecretBesideGeneratedOutputStillKeepsTheLane(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	write(t, laneWT, ".gitignore", ".output/\n.env*\n")
	gitDo(t, laneWT, "add", ".gitignore")
	gitDo(t, laneWT, "commit", "-qm", "ignore output and env")
	gitDo(t, mainRepo, "merge", "-q", "--squash", "lane/squashed")
	gitDo(t, mainRepo, "commit", "-qm", "ignore output and env (#9)")
	pruneLanePush(t, mainRepo, "lane/squashed")
	write(t, laneWT, "frontend/e2e/.output/.last-run.json", "{}\n")
	write(t, laneWT, "frontend/.env.local", "SECRET=1\n")
	pruneAgeLaneGit(t, laneWT)

	pruned, _, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 0 || !strings.Contains(errs, "frontend/.env.local") {
		t.Fatalf("pruned %+v, stderr %q: the secret must keep the lane and be named", pruned, errs)
	}
}
