package gc

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeKnownReposLog(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate.log")
	data := ""
	for _, l := range lines {
		data += l + "\n"
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func knownReposLogLine(at time.Time, root string) string {
	return fmt.Sprintf("%s precommit %s go vet ./... green 1.0s", at.UTC().Format(time.RFC3339), root)
}

func TestKnownReposFrom_ReadsRecentRootsThatStillExistOncePerRepo(t *testing.T) {
	now := time.Now()
	repoA := makeCargoRepo(t)
	repoB := makeCargoRepo(t)
	laneA := filepath.Join(filepath.Dir(repoA), "lane-a")
	gitDo(t, repoA, "worktree", "add", "-q", "-b", "lane/a", laneA)
	path := writeKnownReposLog(t,
		knownReposLogLine(now.Add(-20*24*time.Hour), repoB),    // too old to count
		knownReposLogLine(now.Add(-2*time.Hour), repoA),        // the primary checkout
		knownReposLogLine(now.Add(-time.Hour), laneA),          // its lane: the same repo
		knownReposLogLine(now.Add(-time.Hour), "/no/such/dir"), // gone
		knownReposLogLine(now.Add(-time.Hour), "relative/dir"), // not a path the sweep can name
		"garbage line",
	)

	got := knownReposFrom(path, now)

	if len(got) != 1 {
		t.Fatalf("known repos = %v, want one entry: repoA and its lane are one repo, repoB is 20 days stale, the rest are unusable", got)
	}
	if got[0] != laneA && got[0] != repoA {
		t.Fatalf("known repos = %v, want repoA's or its lane's root", got)
	}
}

func TestKnownReposFrom_AMissingLogIsNoRepos(t *testing.T) {
	if got := knownReposFrom(filepath.Join(t.TempDir(), "none.log"), time.Now()); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}
