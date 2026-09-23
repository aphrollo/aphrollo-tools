package precommit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Issue #726: merging trunk INTO a lane judged everything trunk had gained
// since the lane forked, because the merge gate's staged set is the merged
// index against HEAD, and on a lane HEAD is the lane tip. Trunk's changes were
// already gated when they landed on trunk; what the lane still owes is its own
// contribution. Merging a lane INTO trunk is the opposite case and must keep
// judging everything the merge brings in.

// syncRepo commits a Go module on trunk, forks lane/work from it, applies
// laneFiles as one lane commit and trunkFiles as one later trunk commit, and
// returns the repo checked out on the lane together with trunk's name.
func syncRepo(t *testing.T, laneFiles, trunkFiles map[string]string) (root, trunk string) {
	t.Helper()
	root = makeGoRepo(t)
	trunk = currentBranch(t, root)
	gitDo(t, root, "checkout", "-qb", "lane/work")
	for rel, body := range laneFiles {
		write(t, root, rel, body)
	}
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane change")
	gitDo(t, root, "checkout", "-q", trunk)
	for rel, body := range trunkFiles {
		write(t, root, rel, body)
	}
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "trunk change")
	gitDo(t, root, "checkout", "-q", "lane/work")
	return root, trunk
}

func TestMechanical_TrunkSyncIntoDocsOnlyLaneTakesTheDocsFastPath(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root, trunk := syncRepo(t,
		map[string]string{"docs/decisions.md": "# decisions\n"},
		map[string]string{"internal/b/b.go": "package b\n\nfunc B() int { return 1 }\n"})
	gitDo(t, root, "merge", "--no-commit", "--no-ff", trunk)

	if res := Mechanical(root, refuseToRun(t)); res.Blocked {
		t.Fatalf("a trunk sync into a docs-only lane must not be blocked: %s", res.Message)
	}
	log, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("no gate.log written: %v", err)
	}
	if !strings.Contains(string(log), "docs-only-fastpath") {
		t.Fatalf("a trunk sync into a docs-only lane must take the docs fast path, got:\n%s", log)
	}
}

func TestMechanical_TrunkSyncJudgesOnlyTheLanesOwnPackages(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk := syncRepo(t,
		map[string]string{"internal/a/a.go": "package a\n\nfunc A() int { return 1 }\n"},
		map[string]string{"internal/b/b.go": "package b\n\nfunc B() int { return 1 }\n"})
	gitDo(t, root, "merge", "--no-commit", "--no-ff", trunk)

	var seen []Runner
	if res := Mechanical(root, recordRunner(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []Runner{{Cmd: "go", Args: []string{"test", "-race", "-count=1", "-shuffle=on", "./internal/a"}, Dir: "", Deadline: time.Time{}}}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("a trunk sync must judge the lane's own package only:\n got %+v\nwant %+v", seen, want)
	}
}

// The comment-only fast path must read the lane's change against the same
// base the staged set does. Against HEAD, a lane's own code change is
// invisible during a trunk sync (HEAD already carries it), so a real token
// change read as comment-only and its crate skipped the suite.
func TestCommentOnlyRust_TrunkSyncReadsTheLanesCodeChange(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	trunk := currentBranch(t, root)
	gitDo(t, root, "checkout", "-qb", "lane/work")
	write(t, root, "src/lib.rs", "pub fn base() -> i32 { 1 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane code change")
	gitDo(t, root, "checkout", "-q", trunk)
	write(t, root, "docs/notes.md", "# notes\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "trunk docs change")
	gitDo(t, root, "checkout", "-q", "lane/work")
	gitDo(t, root, "merge", "--no-commit", "--no-ff", trunk)

	if commentOnlySource(root) {
		t.Fatal("a lane that changed `0` to `1` in src/lib.rs is not comment-only against trunk")
	}
}

// originTrunkRepo is syncRepo's shape where trunk resolves as origin/main: a
// local main, a remote-tracking origin/main at the fork point and
// refs/remotes/origin/HEAD naming it, with no network remote at all. The
// lane commit is on lane/work, the trunk commit on local main only.
func originTrunkRepo(t *testing.T) string {
	t.Helper()
	root := makeGoRepo(t)
	gitDo(t, root, "checkout", "-qB", "main")
	gitDo(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitDo(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	gitDo(t, root, "checkout", "-qb", "lane/work")
	write(t, root, "internal/a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane change")
	gitDo(t, root, "checkout", "-q", "main")
	write(t, root, "internal/b/b.go", "package b\n\nfunc B() int { return 1 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "trunk change")
	return root
}

func mechanicalRuns(t *testing.T, root string) []Runner {
	t.Helper()
	var seen []Runner
	if res := Mechanical(root, recordRunner(&seen, root)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	return seen
}

func goTestRun(pkgs ...string) Runner {
	return Runner{Cmd: "go", Args: append([]string{"test", "-race", "-count=1", "-shuffle=on"}, pkgs...), Dir: "", Deadline: time.Time{}}
}

// A lane landing on trunk is the merge that must judge everything it brings.
func TestMechanical_LaneIntoTrunkJudgesEverythingItBringsIn(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk := syncRepo(t,
		map[string]string{"internal/a/a.go": "package a\n\nfunc A() int { return 1 }\n"},
		map[string]string{"internal/b/b.go": "package b\n\nfunc B() int { return 1 }\n"})
	gitDo(t, root, "checkout", "-q", trunk)
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "lane/work")

	if got, want := mechanicalRuns(t, root), []Runner{goTestRun("./internal/a")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a lane merged into trunk must judge the lane's change:\n got %+v\nwant %+v", got, want)
	}
}

// Trunk taking its own remote (a pull on the primary) is not a lane sync:
// HEAD is trunk, so the incoming change is judged even though trunk holds it.
func TestMechanical_TrunkPullingItsRemoteJudgesTheIncomingChange(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := originTrunkRepo(t)
	gitDo(t, root, "update-ref", "refs/remotes/origin/main", "main")
	gitDo(t, root, "reset", "-q", "--hard", "main~1")
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "origin/main")

	if got, want := mechanicalRuns(t, root), []Runner{goTestRun("./internal/b")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("trunk merging its remote must judge the incoming change:\n got %+v\nwant %+v", got, want)
	}
}

// One lane merged into another brings work no gate has judged on trunk.
func TestMechanical_LaneIntoLaneJudgesTheIncomingLane(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk := syncRepo(t,
		map[string]string{"docs/decisions.md": "# decisions\n"},
		map[string]string{"README.md": "# readme\n"})
	gitDo(t, root, "checkout", "-q", trunk)
	gitDo(t, root, "checkout", "-qb", "lane/other")
	write(t, root, "internal/c/c.go", "package c\n\nfunc C() int { return 1 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "other lane change")
	gitDo(t, root, "checkout", "-q", "lane/work")
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "lane/other")

	if got, want := mechanicalRuns(t, root), []Runner{goTestRun("./internal/c")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a lane merged into another lane must judge the incoming lane:\n got %+v\nwant %+v", got, want)
	}
}

// A lane lands on the primary's local trunk before anything is pushed, so a
// sync names local main while trunk resolves as origin/main.
func TestMechanical_SyncFromLocalTrunkAheadOfOriginIsScopedToTheLane(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := originTrunkRepo(t)
	gitDo(t, root, "checkout", "-q", "lane/work")
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "main")

	if got, want := mechanicalRuns(t, root), []Runner{goTestRun("./internal/a")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a sync from local trunk must judge the lane's own package only:\n got %+v\nwant %+v", got, want)
	}
}

// A clean automerge fires pre-merge-commit before MERGE_HEAD exists; only
// GIT_REFLOG_ACTION names the incoming branch.
func TestMechanical_AutomergeTrunkSyncNamedByReflogIsScopedToTheLane(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, trunk := syncRepo(t,
		map[string]string{"internal/a/a.go": "package a\n\nfunc A() int { return 1 }\n"},
		map[string]string{"internal/b/b.go": "package b\n\nfunc B() int { return 1 }\n"})
	gitDo(t, root, "merge", "--no-commit", "--no-ff", trunk)
	if err := os.Remove(filepath.Join(root, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal(err)
	}
	t.Setenv(reflogActionEnv, "merge "+trunk)

	if got, want := mechanicalRuns(t, root), []Runner{goTestRun("./internal/a")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("an automerge trunk sync must judge the lane's own package only:\n got %+v\nwant %+v", got, want)
	}
}

// The lockfile scope reads the lane's bump against the same base as the
// staged set. Against HEAD the lane's own bump is invisible during a trunk
// sync, so no package read as moved and the check narrowed to nothing.
func TestLockfileScope_TrunkSyncReadsTheLanesBump(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.lock", lockBeforeLeftpadBump)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base lockfile")
	trunk := currentBranch(t, root)
	gitDo(t, root, "checkout", "-qb", "lane/work")
	write(t, root, "Cargo.lock", lockAfterLeftpadBump)
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane bumps leftpad")
	gitDo(t, root, "checkout", "-q", trunk)
	write(t, root, "docs/notes.md", "# notes\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "trunk docs change")
	gitDo(t, root, "checkout", "-q", "lane/work")
	gitDo(t, root, "merge", "--no-commit", "--no-ff", trunk)

	stubWorkspaceGraph(t, map[string][]string{"alpha": nil, "beta": nil})

	got := lockfileScope("g", root, root, []string{"Cargo.lock"})
	want := []string{"alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lockfileScope = %v, want %v (the lane moved leftpad, which alpha depends on)", got, want)
	}
}
