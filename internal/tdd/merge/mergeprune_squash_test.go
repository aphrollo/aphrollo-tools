package merge

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// squashPruneRepo is a main repo whose lane landed the way the merge queue
// lands one: as a SQUASH commit, so the lane's own tip is not an ancestor of
// main and `--merged` never lists it. Trunk then moves on with an unrelated
// commit.
func squashPruneRepo(t *testing.T) (mainRepo, laneWT string) {
	t.Helper()
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	mainRepo = t.TempDir()
	gitInit(t, mainRepo)
	gitDo(t, mainRepo, "checkout", "-q", "-B", "main")
	commitInitial(t, mainRepo)

	laneWT = filepath.Join(t.TempDir(), "sq")
	gitDo(t, mainRepo, "worktree", "add", "-q", "-b", "lane/squashed", laneWT)
	write(t, laneWT, "feature.go", "package main\n\n// feature\n")
	gitDo(t, laneWT, "add", "-A")
	gitDo(t, laneWT, "commit", "-qm", "lane work")
	write(t, laneWT, "feature.go", "package main\n\n// feature, refined\n")
	gitDo(t, laneWT, "commit", "-qam", "lane work again")

	gitDo(t, mainRepo, "merge", "-q", "--squash", "lane/squashed")
	gitDo(t, mainRepo, "commit", "-qm", "feature (#7)")
	write(t, mainRepo, "other.go", "package main\n\n// unrelated\n")
	gitDo(t, mainRepo, "add", "-A")
	gitDo(t, mainRepo, "commit", "-qm", "unrelated trunk work")
	pruneAgeLaneGit(t, laneWT)
	return mainRepo, laneWT
}

func prunedLanes(t *testing.T, mainRepo string) (pruned []PrunedLane, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	pruned = PruneMergedLanesAfterMerge(mainRepo, "", &out, &errb)
	return pruned, out.String(), errb.String()
}

func TestPruneMergedLanes_PrunesASquashMergedLaneWhoseTreeTrunkAlreadyHolds(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	pruneLanePush(t, mainRepo, "lane/squashed")

	pruned, out, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 1 || pruned[0].Branch != "lane/squashed" {
		t.Fatalf("pruned = %+v, want lane/squashed (stdout %q, stderr %q)", pruned, out, errs)
	}
	if _, err := os.Stat(laneWT); !os.IsNotExist(err) {
		t.Errorf("the squash-merged lane's worktree survived: %v", err)
	}
	if gitOutT(t, mainRepo, "branch", "--list", "lane/squashed") != "" {
		t.Error("the squash-merged lane's branch survived")
	}
}

func TestPruneMergedLanes_KeepsASquashMergedLaneThatCarriesWorkTrunkLacks(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	pruneLanePush(t, mainRepo, "lane/squashed")
	write(t, laneWT, "later.go", "package main\n\n// written after the squash\n")
	gitDo(t, laneWT, "add", "-A")
	gitDo(t, laneWT, "commit", "-qm", "work after the merge")

	pruned, _, _ := prunedLanes(t, mainRepo)

	if len(pruned) != 0 {
		t.Fatalf("pruned %+v: a lane with a commit trunk lacks must stay", pruned)
	}
	if _, err := os.Stat(laneWT); err != nil {
		t.Errorf("worktree removed: %v", err)
	}
}

// squashConflictRepo is a squash-merged lane whose file trunk then rewrote, so
// the tree test cannot judge it: the recorded merge is the only evidence.
func squashConflictRepo(t *testing.T) (mainRepo, laneWT string) {
	t.Helper()
	mainRepo, laneWT = squashPruneRepo(t)
	write(t, mainRepo, "feature.go", "package main\n\n// rewritten on trunk\n")
	gitDo(t, mainRepo, "commit", "-qam", "trunk rewrites the feature")
	return mainRepo, laneWT
}

func recordLaneMerge(laneWT, head string) {
	core.AppendEvent(core.Event{Kind: "merge", Root: laneWT, Verdict: "ok",
		Detail: map[string]string{"pr": "7", "method": "merge queue", "head": head}})
}

// pruneLanePush gives the repo an origin and pushes the lane to it, as the lane
// of a PR is before it merges.
func pruneLanePush(t *testing.T, mainRepo, branch string) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "origin.git")
	gitDo(t, t.TempDir(), "init", "-q", "--bare", remote)
	gitDo(t, mainRepo, "remote", "add", "origin", remote)
	gitDo(t, mainRepo, "push", "-q", "origin", branch)
}

func TestPruneMergedLanes_PrunesALaneTheMergeVerbRecordedAsMergedAfterItsLastCommit(t *testing.T) {
	mainRepo, laneWT := squashConflictRepo(t)
	// Never pushed from here: the recorded head is itself the proof GitHub held it.
	recordLaneMerge(laneWT, gitValue(t, laneWT, "rev-parse", "HEAD"))

	pruned, out, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 1 || pruned[0].Branch != "lane/squashed" {
		t.Fatalf("pruned = %+v, want lane/squashed (stdout %q, stderr %q)", pruned, out, errs)
	}
}

func TestPruneMergedLanes_KeepsAConflictingLaneWithNoRecordedMerge(t *testing.T) {
	mainRepo, laneWT := squashConflictRepo(t)

	pruned, _, _ := prunedLanes(t, mainRepo)

	if len(pruned) != 0 {
		t.Fatalf("pruned %+v with neither ancestry, tree nor a recorded merge to show it landed", pruned)
	}
	if _, err := os.Stat(laneWT); err != nil {
		t.Errorf("worktree removed: %v", err)
	}
}

func TestPruneMergedLanes_KeepsALaneCommittedToAfterItsRecordedMerge(t *testing.T) {
	mainRepo, laneWT := squashConflictRepo(t)
	recordLaneMerge(laneWT, gitValue(t, laneWT, "rev-parse", "HEAD"))
	write(t, laneWT, "after.go", "package main\n\n// made after the queued head\n")
	gitDo(t, laneWT, "add", "-A")
	gitDo(t, laneWT, "commit", "-qm", "work after the merged head")

	pruned, _, _ := prunedLanes(t, mainRepo)

	if len(pruned) != 0 {
		t.Fatalf("pruned %+v: the lane holds a commit after the head the log records", pruned)
	}
}

func TestPruneMergedLanes_KeepsALaneASessionWorkedInRecently(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	pruneLanePush(t, mainRepo, "lane/squashed")
	pruneSessionIn(t, "sess-live", laneWT, time.Now().Add(-5*time.Minute))

	pruned, _, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 0 {
		t.Fatalf("pruned %+v out from under a session active 5 minutes ago", pruned)
	}
	if !strings.Contains(errs, "session") {
		t.Errorf("stderr %q does not say a session holds the lane", errs)
	}
	if _, err := os.Stat(laneWT); err != nil {
		t.Errorf("worktree removed: %v", err)
	}
}

func TestPruneMergedLanes_PrunesALaneWhoseOnlySessionWentQuietLongAgo(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	pruneLanePush(t, mainRepo, "lane/squashed")
	pruneSessionIn(t, "sess-old", laneWT, time.Now().Add(-3*time.Hour))

	pruned, _, _ := prunedLanes(t, mainRepo)

	if len(pruned) != 1 {
		t.Fatalf("pruned = %+v, want the quiet lane removed", pruned)
	}
}

func TestPruneMergedLanes_KeepsALockedLane(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	pruneLanePush(t, mainRepo, "lane/squashed")
	gitDo(t, mainRepo, "worktree", "lock", "--reason", "agent holds it", laneWT)

	pruned, _, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 0 || !strings.Contains(errs, "locked") {
		t.Fatalf("pruned %+v, stderr %q: a locked worktree must be kept and named", pruned, errs)
	}
}

func TestPruneMergedLanes_KeepsALaneClaimedOnTheDevTier(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	pruneLanePush(t, mainRepo, "lane/squashed")
	claims := t.TempDir()
	t.Setenv("APHROLLO_DEVCLAIM_DIR", claims)
	if err := os.Symlink(laneWT, filepath.Join(claims, "web")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}

	pruned, _, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 0 || !strings.Contains(errs, "claim") {
		t.Fatalf("pruned %+v, stderr %q: a lane the dev units serve must be kept and named", pruned, errs)
	}
	if _, err := os.Stat(laneWT); err != nil {
		t.Errorf("worktree removed: %v", err)
	}
}

func TestPruneMergedLanes_RemovesTheLanesInstalledDependenciesWithIt(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	write(t, laneWT, ".gitignore", "node_modules/\n.venv/\n")
	gitDo(t, laneWT, "add", ".gitignore")
	gitDo(t, laneWT, "commit", "-qm", "ignore installs")
	pruneLanePush(t, mainRepo, "lane/squashed")
	gitDo(t, mainRepo, "merge", "-q", "--squash", "lane/squashed")
	gitDo(t, mainRepo, "commit", "-qm", "ignore installs (#8)")
	pruneAgeLaneGit(t, laneWT)
	write(t, laneWT, "node_modules/pkg/index.js", "x")
	write(t, laneWT, ".venv/lib/site.py", "x")

	pruned, _, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 1 {
		t.Fatalf("pruned = %+v, stderr %q", pruned, errs)
	}
	if _, err := os.Stat(laneWT); !os.IsNotExist(err) {
		t.Errorf("the lane directory, with its installs, survived: %v", err)
	}
}

// pruneSessionIn writes the state file of a session that last stamped a result
// in root at the given time.
func pruneSessionIn(t *testing.T, id, root string, at time.Time) {
	t.Helper()
	dir := core.StateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"schema":%d,"by_project":{%q:{"ts":%q}}}`, core.StateSchema, filepath.ToSlash(root), at.UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPruneMergedLanes_KeepsALaneWhoseOnlyCommitsAreACommitAndItsRevertNeverPushed(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	mainRepo := t.TempDir()
	gitInit(t, mainRepo)
	gitDo(t, mainRepo, "checkout", "-q", "-B", "main")
	commitInitial(t, mainRepo)
	laneWT := filepath.Join(t.TempDir(), "rv")
	gitDo(t, mainRepo, "worktree", "add", "-q", "-b", "lane/reverted", laneWT)
	write(t, laneWT, "scratch.go", "package main\n\n// tried and dropped\n")
	gitDo(t, laneWT, "add", "-A")
	gitDo(t, laneWT, "commit", "-qm", "try something")
	gitDo(t, laneWT, "revert", "--no-edit", "HEAD")
	write(t, mainRepo, "other.go", "package main\n\n// trunk moves on\n")
	gitDo(t, mainRepo, "add", "-A")
	gitDo(t, mainRepo, "commit", "-qm", "unrelated trunk work")

	pruned, _, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 0 || !strings.Contains(errs, "unpushed") {
		t.Fatalf("pruned %+v, stderr %q: two commits no remote holds are the last copy and must be kept", pruned, errs)
	}
	if gitOutT(t, mainRepo, "branch", "--list", "lane/reverted") == "" {
		t.Error("the lane's branch was deleted")
	}
}

// pruneAgeLaneGit sets the times of the lane's git state back an hour, as a
// lane nobody has touched for that long looks. A lane made a moment ago is,
// rightly, held by the activity check.
func pruneAgeLaneGit(t *testing.T, wt string) {
	t.Helper()
	gitdir := gitValue(t, wt, "rev-parse", "--absolute-git-dir")
	old := time.Now().Add(-time.Hour)
	for _, rel := range []string{"index", "HEAD", filepath.Join("logs", "HEAD")} {
		if err := os.Chtimes(filepath.Join(gitdir, rel), old, old); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func TestPruneMergedLanes_KeepsALaneWhoseGitStateChangedLately(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	pruneLanePush(t, mainRepo, "lane/squashed")
	gitdir := gitValue(t, laneWT, "rev-parse", "--absolute-git-dir")
	recent := time.Now().Add(-3 * time.Minute)
	if err := os.Chtimes(filepath.Join(gitdir, "logs", "HEAD"), recent, recent); err != nil {
		t.Fatal(err)
	}

	pruned, _, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 0 || !strings.Contains(errs, "git state") {
		t.Fatalf("pruned %+v, stderr %q: a Bash-only agent that committed 3 minutes ago leaves no session stamp and must still hold its lane", pruned, errs)
	}
	if _, err := os.Stat(laneWT); err != nil {
		t.Errorf("worktree removed: %v", err)
	}
}

func TestPruneMergedLanes_KeepsALaneWithADeferredJobStartedLately(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	pruneLanePush(t, mainRepo, "lane/squashed")
	pruneDeferredJobIn(t, laneWT, time.Now().Add(-5*time.Minute))

	pruned, _, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 0 || !strings.Contains(errs, "deferred") {
		t.Fatalf("pruned %+v, stderr %q: a builder whose first run is still deferred holds its lane", pruned, errs)
	}
}

func TestPruneMergedLanes_KeepsALaneWithAnIgnoredEnvFileNewerThanItsLastCommit(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	write(t, laneWT, ".gitignore", ".env*\n")
	gitDo(t, laneWT, "add", ".gitignore")
	gitDo(t, laneWT, "commit", "-qm", "ignore env files")
	gitDo(t, mainRepo, "merge", "-q", "--squash", "lane/squashed")
	gitDo(t, mainRepo, "commit", "-qm", "ignore env (#9)")
	pruneLanePush(t, mainRepo, "lane/squashed")
	write(t, laneWT, ".env.local", "SECRET=1\n")
	pruneAgeLaneGit(t, laneWT)

	pruned, _, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 0 || !strings.Contains(errs, ".env.local") {
		t.Fatalf("pruned %+v, stderr %q: an ignored .env.local written after the last commit exists nowhere else", pruned, errs)
	}
}

func TestPruneMergedLanes_KeepsALaneWithANestedIgnoredFileNewerThanItsLastCommit(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	write(t, laneWT, ".gitignore", ".env*\n")
	gitDo(t, laneWT, "add", ".gitignore")
	gitDo(t, laneWT, "commit", "-qm", "ignore env files")
	gitDo(t, mainRepo, "merge", "-q", "--squash", "lane/squashed")
	gitDo(t, mainRepo, "commit", "-qm", "ignore env (#9)")
	pruneLanePush(t, mainRepo, "lane/squashed")
	write(t, laneWT, "apps/web/.env.local", "SECRET=1\n")
	pruneAgeLaneGit(t, laneWT)

	pruned, _, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 0 || !strings.Contains(errs, "apps/web/.env.local") {
		t.Fatalf("pruned %+v, stderr %q: a nested ignored file written after the last commit exists nowhere else", pruned, errs)
	}
}

func TestPruneMergedLanes_ANewFileInIgnoredDependencyAndBuildDirectoriesDoesNotKeepALane(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	write(t, laneWT, ".gitignore", "node_modules/\ntarget/\ndist/\n")
	gitDo(t, laneWT, "add", ".gitignore")
	gitDo(t, laneWT, "commit", "-qm", "ignore build output")
	gitDo(t, mainRepo, "merge", "-q", "--squash", "lane/squashed")
	gitDo(t, mainRepo, "commit", "-qm", "ignore build output (#9)")
	pruneLanePush(t, mainRepo, "lane/squashed")
	write(t, laneWT, "node_modules/pkg/index.js", "x\n")
	write(t, laneWT, "target/debug/app", "x\n")
	write(t, laneWT, "web/dist/main.js", "x\n")
	pruneAgeLaneGit(t, laneWT)

	pruned, _, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 1 {
		t.Fatalf("pruned = %+v, stderr %q: rebuildable output must not hold a lane", pruned, errs)
	}
}

func TestPruneMergedLanes_AnOldIgnoredEnvFileGoesWithTheLaneAndTheLineSaysSo(t *testing.T) {
	mainRepo, laneWT := squashPruneRepo(t)
	write(t, laneWT, ".gitignore", ".env*\n")
	gitDo(t, laneWT, "add", ".gitignore")
	gitDo(t, laneWT, "commit", "-qm", "ignore env files")
	gitDo(t, mainRepo, "merge", "-q", "--squash", "lane/squashed")
	gitDo(t, mainRepo, "commit", "-qm", "ignore env (#9)")
	pruneLanePush(t, mainRepo, "lane/squashed")
	write(t, laneWT, ".env.local", "SECRET=1\n")
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(laneWT, ".env.local"), old, old); err != nil {
		t.Fatal(err)
	}
	pruneAgeLaneGit(t, laneWT)

	pruned, out, errs := prunedLanes(t, mainRepo)

	if len(pruned) != 1 {
		t.Fatalf("pruned = %+v, stderr %q", pruned, errs)
	}
	if !strings.Contains(out, "gitignored files") {
		t.Errorf("stdout %q does not say the lane's gitignored files were removed with it", out)
	}
}

// pruneDeferredJobIn writes the record a hook leaves when it detaches a run for
// root: a job started at the given time, with no result yet.
func pruneDeferredJobIn(t *testing.T, root string, started time.Time) {
	t.Helper()
	dir := filepath.Join(core.StateDir(), "deferred")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"schema":%d,"project":%q,"phase":"run","started":%q,"session":"s"}`, core.StateSchema, filepath.ToSlash(root), started.UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dir, "job.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
