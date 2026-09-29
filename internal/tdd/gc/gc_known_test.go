package gc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	got := knownReposFrom(path, now, knownReposMax)

	if len(got) != 1 {
		t.Fatalf("known repos = %v, want one entry: repoA and its lane are one repo, repoB is 20 days stale, the rest are unusable", got)
	}
	if got[0] != laneA && got[0] != repoA {
		t.Fatalf("known repos = %v, want repoA's or its lane's root", got)
	}
}

func TestKnownReposFrom_AMissingLogIsNoRepos(t *testing.T) {
	if got := knownReposFrom(filepath.Join(t.TempDir(), "none.log"), time.Now(), knownReposMax); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

// A repo named by the log's very first line is still a known repo.
func TestKnownReposFrom_TheFirstLineOfTheLogCounts(t *testing.T) {
	now := time.Now()
	repo := makeCargoRepo(t)
	path := writeKnownReposLog(t, knownReposLogLine(now.Add(-time.Hour), repo))
	if got := knownReposFrom(path, now, knownReposMax); len(got) != 1 || got[0] != repo {
		t.Fatalf("known repos = %v, want [%s] from a one-line log", got, repo)
	}
}

// A line with the three fields every entry starts with is enough to name a
// root: the command and verdict after it are not the sweep's business.
func TestKnownReposFrom_ALineOfExactlyThreeFieldsCounts(t *testing.T) {
	now := time.Now()
	repo := makeCargoRepo(t)
	line := fmt.Sprintf("%s precommit %s", now.Add(-time.Hour).UTC().Format(time.RFC3339), repo)
	if got := knownReposFrom(writeKnownReposLog(t, line), now, knownReposMax); len(got) != 1 {
		t.Fatalf("known repos = %v, want the repo of a three-field line", got)
	}
	twoFields := fmt.Sprintf("%s %s", now.Add(-time.Hour).UTC().Format(time.RFC3339), repo)
	if got := knownReposFrom(writeKnownReposLog(t, twoFields), now, knownReposMax); len(got) != 0 {
		t.Fatalf("known repos = %v, want nothing from a line with no stage field", got)
	}
}

// The window is inclusive at its edge: a repo last worked in exactly the
// window ago is still known, one a nanosecond older is not.
func TestKnownReposFrom_TheWindowEdgeIsInclusive(t *testing.T) {
	repo := makeCargoRepo(t)
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	path := writeKnownReposLog(t, knownReposLogLine(at, repo))
	if got := knownReposFrom(path, at.Add(knownReposWindow), knownReposMax); len(got) != 1 {
		t.Fatalf("at exactly the window: %v, want the repo", got)
	}
	if got := knownReposFrom(path, at.Add(knownReposWindow+time.Second), knownReposMax); len(got) != 0 {
		t.Fatalf("a second past the window: %v, want nothing", got)
	}
}

// The limit is how many repos, not one more.
func TestKnownReposFrom_StopsAtTheLimit(t *testing.T) {
	now := time.Now()
	var lines []string
	for range 3 {
		lines = append(lines, knownReposLogLine(now.Add(-time.Hour), makeCargoRepo(t)))
	}
	if got := knownReposFrom(writeKnownReposLog(t, lines...), now, 2); len(got) != 2 {
		t.Fatalf("known repos = %v, want exactly 2 of 3 with a limit of 2", got)
	}
}

// Only the tail of a huge log is read: a repo named only in the part before
// it is not known, and one in the tail is.
func TestKnownReposFrom_OnlyTheTailOfALargeLogIsRead(t *testing.T) {
	now := time.Now()
	old := makeCargoRepo(t)
	recent := makeCargoRepo(t)
	path := filepath.Join(t.TempDir(), "gate.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	filler := strings.Repeat("x", 1000) + "\n"
	fmt.Fprintln(f, knownReposLogLine(now.Add(-time.Hour), old))
	for i := 0; i < (knownReposTail/len(filler))+2000; i++ {
		f.WriteString(filler)
	}
	fmt.Fprintln(f, knownReposLogLine(now.Add(-time.Hour), recent))
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got := knownReposFrom(path, now, knownReposMax)
	if len(got) != 1 || got[0] != recent {
		t.Fatalf("known repos = %v, want only %s: %s is named before the tail", got, recent, old)
	}
}
