package gc

import (
	"path/filepath"
	"testing"
	"time"
)

func knownReposEntry(at time.Time, root string) gateEntry {
	return gateEntry{At: at.UTC(), Stage: "precommit", Root: root, Cmd: "go vet ./...", Verdict: "green", Secs: 1}
}

func TestKnownReposFrom_ReadsRecentRootsThatStillExistOncePerRepo(t *testing.T) {
	now := time.Now()
	repoA := makeCargoRepo(t)
	repoB := makeCargoRepo(t)
	laneA := filepath.Join(filepath.Dir(repoA), "lane-a")
	gitDo(t, repoA, "worktree", "add", "-q", "-b", "lane/a", laneA)
	entries := []gateEntry{
		knownReposEntry(now.Add(-20*24*time.Hour), repoB),    // too old to count
		knownReposEntry(now.Add(-2*time.Hour), repoA),        // the primary checkout
		knownReposEntry(now.Add(-time.Hour), laneA),          // its lane: the same repo
		knownReposEntry(now.Add(-time.Hour), "/no/such/dir"), // gone
		knownReposEntry(now.Add(-time.Hour), "relative/dir"), // not a path the sweep can name
	}

	got := knownReposFrom(entries, now, knownReposMax)

	if len(got) != 1 {
		t.Fatalf("known repos = %v, want one entry: repoA and its lane are one repo, repoB is 20 days stale, the rest are unusable", got)
	}
	if got[0] != laneA && got[0] != repoA {
		t.Fatalf("known repos = %v, want repoA's or its lane's root", got)
	}
}

// ratchet: test_removed TestKnownReposFrom_AMissingLogIsNoRepos: the roots are read from the event logs now, and no entries is the empty case every other test here relies on
// ratchet: test_removed TestKnownReposFrom_ALineOfExactlyThreeFieldsCounts: a text line's field count has no meaning for an entry that is already parsed
// ratchet: test_removed TestKnownReposFrom_OnlyTheTailOfALargeLogIsRead: the sweep no longer reads the tail of a text file; the event log is read by month

// A repo named by the first entry is still a known repo.
func TestKnownReposFrom_TheFirstLineOfTheLogCounts(t *testing.T) {
	now := time.Now()
	repo := makeCargoRepo(t)
	if got := knownReposFrom([]gateEntry{knownReposEntry(now.Add(-time.Hour), repo)}, now, knownReposMax); len(got) != 1 || got[0] != repo {
		t.Fatalf("known repos = %v, want [%s] from a one-entry log", got, repo)
	}
}

// The window is inclusive at its edge: a repo last worked in exactly the
// window ago is still known, one a nanosecond older is not.
func TestKnownReposFrom_TheWindowEdgeIsInclusive(t *testing.T) {
	repo := makeCargoRepo(t)
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	entries := []gateEntry{knownReposEntry(at, repo)}
	if got := knownReposFrom(entries, at.Add(knownReposWindow), knownReposMax); len(got) != 1 {
		t.Fatalf("at exactly the window: %v, want the repo", got)
	}
	if got := knownReposFrom(entries, at.Add(knownReposWindow+time.Second), knownReposMax); len(got) != 0 {
		t.Fatalf("a second past the window: %v, want nothing", got)
	}
}

// The limit is how many repos, not one more.
func TestKnownReposFrom_StopsAtTheLimit(t *testing.T) {
	now := time.Now()
	var entries []gateEntry
	for range 3 {
		entries = append(entries, knownReposEntry(now.Add(-time.Hour), makeCargoRepo(t)))
	}
	if got := knownReposFrom(entries, now, 2); len(got) != 2 {
		t.Fatalf("known repos = %v, want exactly 2 of 3 with a limit of 2", got)
	}
}
