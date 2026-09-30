package mutation

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The canary's whole job is to notice what a test process that reached the
// real repository would change: its config, the checked-out branch and commit,
// the branches, packed-refs, and the operator's global git config (#1043).

// canaryRepo is a committed repository, and the global git config the canary
// reads is a file of the test's own.
func canaryRepo(t *testing.T) (repo, globalConfig string) {
	t.Helper()
	repo = makeGoRepo(t)
	gitDo(t, repo, "branch", "-M", "main")
	globalConfig = filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	return repo, globalConfig
}

func TestSnapshotGitWorld_AnUntouchedWorldHasNoChanges(t *testing.T) {
	repo, _ := canaryRepo(t)
	before := snapshotGitWorld(repo)

	// Reading the repository, as a run does, must not look like a change.
	gitOutT(t, repo, "status", "--porcelain")
	gitOutT(t, repo, "log", "--oneline")

	if changes := before.changesTo(snapshotGitWorld(repo)); len(changes) != 0 {
		t.Errorf("an untouched world changed: %v", changes)
	}
}

func TestSnapshotGitWorld_NamesEachThingALeakChanges(t *testing.T) {
	cases := []struct {
		name  string
		label string
		leak  func(t *testing.T, repo, sibling, global string)
	}{
		{"a config key", "the repository's config", func(t *testing.T, repo, _, _ string) {
			gitDo(t, repo, "config", "leak.key", "1")
		}},
		{"a new branch", "the branches", func(t *testing.T, repo, _, _ string) {
			gitDo(t, repo, "branch", "feat/x")
		}},
		{"a deleted branch", "the branches", func(t *testing.T, repo, _, _ string) {
			gitDo(t, repo, "branch", "-D", "spare")
		}},
		{"a switched worktree HEAD", "the worktree HEADs", func(t *testing.T, _, sibling, _ string) {
			gitDo(t, sibling, "switch", "-q", "-c", "other")
		}},
		{"a new worktree", "the worktree registrations", func(t *testing.T, repo, _, _ string) {
			gitDo(t, repo, "worktree", "add", "-q", "--detach", filepath.Join(t.TempDir(), "extra"))
		}},
		{"a moved main", "the tip of main", func(t *testing.T, repo, _, _ string) {
			gitDo(t, repo, "commit", "-q", "--allow-empty", "-m", "fixture")
		}},
		{"a moved checkout", "the checked-out commit", func(t *testing.T, _, _, _ string) {}},
		{"the global config", "the global git config", func(t *testing.T, _, _, global string) {
			mustWrite(t, global, "[user]\n\tname = Jane Doe\n")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "a moved checkout" {
				// The checkout's own commit moves when its own branch does.
				repo, _, _ := canaryLanes(t)
				lane := filepath.Join(t.TempDir(), "mine")
				gitDo(t, repo, "worktree", "add", "-q", "-b", "mine", lane)
				before := snapshotGitWorld(lane)
				gitDo(t, lane, "commit", "-q", "--allow-empty", "-m", "mine")
				requireChange(t, before.changesTo(snapshotGitWorld(lane)), c.label)
				return
			}
			repo, sibling, global := canaryLanes(t)
			before := snapshotGitWorld(repo)

			c.leak(t, repo, sibling, global)

			requireChange(t, before.changesTo(snapshotGitWorld(repo)), c.label)
		})
	}
}

// canaryLanes is a repository with a sibling lane checked out beside it, and the
// global config the canary reads a file of the test's own.
func canaryLanes(t *testing.T) (repo, sibling, globalConfig string) {
	t.Helper()
	repo, globalConfig = canaryRepo(t)
	sibling = filepath.Join(t.TempDir(), "sibling")
	gitDo(t, repo, "worktree", "add", "-q", "-b", "sibling", sibling)
	gitDo(t, repo, "branch", "spare")
	return repo, sibling, globalConfig
}

func requireChange(t *testing.T, changes []string, label string) {
	t.Helper()
	for _, change := range changes {
		if strings.HasPrefix(change, label) {
			return
		}
	}
	t.Errorf("no change named %q among %v", label, changes)
}

// Ordinary work on a busy box is not a leak: a sibling lane committing to its own
// branch, a push writing an upstream, the operator fetching.
func TestSnapshotGitWorld_OrdinaryWorkByASiblingLaneIsNotAChange(t *testing.T) {
	repo, sibling, _ := canaryLanes(t)
	before := snapshotGitWorld(repo)

	write(t, sibling, "work.txt", "x\n")
	gitDo(t, sibling, "add", "work.txt")
	gitDo(t, sibling, "commit", "-qm", "sibling work")
	gitDo(t, repo, "config", "branch.sibling.remote", "origin")
	gitDo(t, repo, "config", "branch.sibling.merge", "refs/heads/sibling")

	if changes := before.changesTo(snapshotGitWorld(repo)); len(changes) != 0 {
		t.Errorf("a sibling's commit and an upstream stanza looked like a leak: %v", changes)
	}
}

// laneDirOf is where the lanes of repo's checkout live: beside it, in
// .worktrees/<repo>, which is also where the gate's own scratch areas are.
func laneDirOf(repo string) string {
	return filepath.Join(filepath.Dir(repo), ".worktrees", filepath.Base(repo))
}

// The gate registers worktrees of its own during a run (a trunk preview, a PR
// merge checkout, a fail-first checkout) and lanes are made and removed, so a
// registration that is a lane or a gate path is ordinary work, never a leak.
func TestSnapshotGitWorld_LanesAndGateWorktreesComingAndGoingAreNotAChange(t *testing.T) {
	cases := []struct {
		name string
		path func(t *testing.T, repo string) string
	}{
		{"a new lane", func(t *testing.T, repo string) string { return filepath.Join(laneDirOf(repo), "new-lane") }},
		{"a gate PR merge checkout", func(t *testing.T, repo string) string {
			return filepath.Join(laneDirOf(repo), "gate-prmerge-123")
		}},
		{"a gate trunk preview", func(t *testing.T, _ string) string {
			return filepath.Join(t.TempDir(), "gate-trunkpreview-456")
		}},
		{"a fail-first checkout", func(t *testing.T, _ string) string {
			return filepath.Join(t.TempDir(), "gate-state", "failfirst-wt", "0123abcd")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, _ := canaryRepo(t)
			path := c.path(t, repo)
			before := snapshotGitWorld(repo)

			gitDo(t, repo, "worktree", "add", "-q", "--detach", path)
			if changes := before.changesTo(snapshotGitWorld(repo)); len(changes) != 0 {
				t.Errorf("adding %s looked like a leak: %v", path, changes)
			}
			added := snapshotGitWorld(repo)
			gitDo(t, repo, "worktree", "remove", "--force", path)
			if changes := added.changesTo(snapshotGitWorld(repo)); len(changes) != 0 {
				t.Errorf("removing %s looked like a leak: %v", path, changes)
			}
		})
	}
}

// A lane that was there when the run started and is gone when it ends is
// somebody pruning it, and a lane's own HEAD moving is not a change of a
// worktree the run could have reached: only a switched branch is.
func TestSnapshotGitWorld_AnExistingLanePrunedMidRunIsNotAChange(t *testing.T) {
	repo, _ := canaryRepo(t)
	lane := filepath.Join(laneDirOf(repo), "old-lane")
	gitDo(t, repo, "worktree", "add", "-q", "-b", "old-lane", lane)
	before := snapshotGitWorld(repo)

	gitDo(t, repo, "worktree", "remove", "--force", lane)

	if changes := before.changesTo(snapshotGitWorld(repo)); len(changes) != 0 {
		t.Errorf("pruning a lane looked like a leak: %v", changes)
	}
}

// The gate re-points its own checkouts (a fail-first worktree is reused from
// one run to the next), so a gate checkout switching what it has checked out is
// not a worktree the run changed.
func TestSnapshotGitWorld_AGateCheckoutRePointedMidRunIsNotAChange(t *testing.T) {
	repo, _, _ := canaryLanes(t)
	gate := filepath.Join(laneDirOf(repo), "gate-prmerge-9")
	gitDo(t, repo, "worktree", "add", "-q", "--detach", gate)
	before := snapshotGitWorld(repo)

	gitDo(t, gate, "switch", "-q", "spare")

	if changes := before.changesTo(snapshotGitWorld(repo)); len(changes) != 0 {
		t.Errorf("a gate checkout switching branch looked like a leak: %v", changes)
	}
}

// Lanes coming and going do not hide a real change: the sibling that was there
// all along switching branches is named, whatever else was added beside it.
func TestSnapshotGitWorld_AnExistingWorktreesHeadChangeShowsAmidLaneChurn(t *testing.T) {
	repo, sibling, _ := canaryLanes(t)
	before := snapshotGitWorld(repo)

	gitDo(t, repo, "worktree", "add", "-q", "-b", "churn", filepath.Join(laneDirOf(repo), "churn"))
	gitDo(t, sibling, "switch", "-q", "-c", "other")

	changes := before.changesTo(snapshotGitWorld(repo))

	requireChange(t, changes, "the worktree HEADs")
	for _, change := range changes {
		if strings.Contains(change, filepath.Join(laneDirOf(repo), "churn")) {
			t.Errorf("the new lane's own HEAD is reported: %s", change)
		}
	}
}

// What a test process of the run leaves behind lies in the run's own temp
// areas, or anywhere that is neither a lane nor a gate path: both are leaks.
func TestSnapshotGitWorld_AWorktreeInTheRunsTempAreaOrOutsideAnyLaneIsAChange(t *testing.T) {
	cases := []struct {
		name string
		path func(t *testing.T, repo, runTmp string) string
	}{
		{"the run's temp dir", func(_ *testing.T, _, runTmp string) string { return filepath.Join(runTmp, "leak") }},
		{"the shared go scratch dir", func(_ *testing.T, repo, _ string) string {
			return filepath.Join(laneDirOf(repo), "gotmp", "aphrollo-x", "leak")
		}},
		{"the mutation area", func(_ *testing.T, repo, _ string) string {
			return filepath.Join(laneDirOf(repo), ".mutants", "lane", "leak")
		}},
		{"a path that is no lane's", func(t *testing.T, _, _ string) string { return filepath.Join(t.TempDir(), "elsewhere", "leak") }},
		{"a directory inside a lane's", func(t *testing.T, repo, _ string) string {
			return filepath.Join(laneDirOf(repo), "a-lane", "nested")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, _ := canaryRepo(t)
			runTmp := filepath.Join(t.TempDir(), "runtmp")
			before := snapshotGitWorld(repo)

			gitDo(t, repo, "worktree", "add", "-q", "--detach", c.path(t, repo, runTmp))

			requireChange(t, before.changesTo(snapshotGitWorld(repo)), "the worktree registrations")
		})
	}
}

// The exact edges of "a lane of this repository" and of a gate path.
func TestIsWatchedWorktree_TheEdgesOfALaneDirAndOfAGatePath(t *testing.T) {
	laneDir := "/w/.worktrees/repo"
	cases := []struct {
		path string
		want bool
	}{
		{"/w/.worktrees/repo/lane", false},
		{"/w/.worktrees/repo/lane/nested", true},
		{"/w/.worktrees/repo/gotmp/x", true},
		{"/w/.worktrees/repo/.mutants/lane/x", true},
		{"/w/.worktrees/repo", true},
		{"/w/.worktrees/other/lane", true},
		{"/w/repo", true},
		{"/tmp/run/leak", true},
		{"/w/.worktrees/repo/gate-prmerge-1", false},
		{"/tmp/gate-trunkpreview-1", false},
		{"/tmp/gate-failfirst-1", false},
		{"/s/gate-state/failfirst-wt/ab12", false},
		{"/tmp/gate-prmerge", true},
		{"/tmp/gate-trunkpreview", true},
		{"/tmp/gate-failfirst", true},
	}
	for _, c := range cases {
		if got := isWatchedWorktree(c.path, laneDir); got != c.want {
			t.Errorf("isWatchedWorktree(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestWithoutBranchStanzas_DropsOnlyTheBranchSections(t *testing.T) {
	config := "[core]\n\tbare = false\n[branch \"a\"]\n\tremote = origin\n[remote \"origin\"]\n\turl = u\n[branch \"b\"]\n\tmerge = m\n[user]\n\tname = n\n"

	got := withoutBranchStanzas(config)

	want := "[core]\n\tbare = false\n[remote \"origin\"]\n\turl = u\n[user]\n\tname = n\n"
	if got != want {
		t.Errorf("withoutBranchStanzas =\n%q\nwant\n%q", got, want)
	}
	if got := withoutBranchStanzas(""); got != "" {
		t.Errorf("an empty config became %q", got)
	}
	if got := withoutBranchStanzas("[branches]\n\tx = 1\n"); got != "[branches]\n\tx = 1\n" {
		t.Errorf("a section merely starting like branch was dropped: %q", got)
	}
}

func TestWorktreeFacts_ListsPathsAndWhatEachHasCheckedOut(t *testing.T) {
	porcelain := "worktree /b\nHEAD 111\nbranch refs/heads/two\n\nworktree /a\nHEAD 222\ndetached\n\nworktree /c\nHEAD 333\nbranch refs/heads/one\n"

	paths, heads := worktreeFacts(porcelain)

	if want := "/a\n/b\n/c"; paths != want {
		t.Errorf("paths = %q, want %q", paths, want)
	}
	if want := "/a -> detached\n/b -> refs/heads/two\n/c -> refs/heads/one"; heads != want {
		t.Errorf("heads = %q, want %q", heads, want)
	}
}

// What changed is said, not just that something did: the lines a config gained
// and the branches that appeared or went.
func TestChangesTo_SaysWhatChanged(t *testing.T) {
	repo, _ := canaryRepo(t)
	before := snapshotGitWorld(repo)

	gitDo(t, repo, "config", "leak.key", "1")
	gitDo(t, repo, "branch", "feat/x")
	changes := strings.Join(before.changesTo(snapshotGitWorld(repo)), "\n")

	for _, want := range []string{"+\tkey = 1", "+refs/heads/feat/x"} {
		if !strings.Contains(changes, want) {
			t.Errorf("the changes do not say %q:\n%s", want, changes)
		}
	}
}

func TestLineDiff_ListsAddedAndRemovedLinesUpToACap(t *testing.T) {
	if got := lineDiff("a\nb\nc\n", "a\nb\nc\n"); got != "" {
		t.Errorf("equal texts differ: %q", got)
	}
	if got, want := lineDiff("a\nb\n", "b\nc\n"), "-a\n+c"; got != want {
		t.Errorf("lineDiff = %q, want %q", got, want)
	}
	var many strings.Builder
	for i := range diffLineCap + 3 {
		many.WriteString("line" + string(rune('a'+i)) + "\n")
	}
	got := strings.Split(lineDiff("", many.String()), "\n")
	if len(got) != diffLineCap+1 || got[diffLineCap] != "…3 more" {
		t.Errorf("lineDiff of %d added lines = %d lines %q, want %d and a closing note counting the 3 left out", diffLineCap+3, len(got), got, diffLineCap+1)
	}
	exactly := strings.Split(lineDiff("", strings.Repeat("x\n", diffLineCap)), "\n")
	if len(exactly) != diffLineCap {
		t.Errorf("lineDiff of exactly the cap = %d lines, want %d with no note", len(exactly), diffLineCap)
	}
}

// A global config that is not there yet and then is — a fixture's first
// `git config --global` — is a change; so is one that is deleted.
func TestSnapshotGitWorld_AGlobalConfigAppearingOrGoingIsAChange(t *testing.T) {
	repo, global := canaryRepo(t)
	before := snapshotGitWorld(repo)
	mustWrite(t, global, "[user]\n\tname = x\n")
	appeared := snapshotGitWorld(repo)

	if changes := before.changesTo(appeared); len(changes) != 1 || !strings.HasPrefix(changes[0], "the global git config") ||
		!strings.Contains(changes[0], "(it was not there)") {
		t.Errorf("appearing: %v", changes)
	}
	if err := os.Remove(global); err != nil {
		t.Fatal(err)
	}
	if changes := appeared.changesTo(snapshotGitWorld(repo)); len(changes) != 1 || !strings.Contains(changes[0], "(it is gone)") {
		t.Errorf("going: %v", changes)
	}
}

// Outside a repository only the global config is watched.
func TestSnapshotGitWorld_OutsideARepositoryWatchesOnlyTheGlobalConfig(t *testing.T) {
	_, global := canaryRepo(t)
	dir := t.TempDir()
	before := snapshotGitWorld(dir)

	mustWrite(t, global, "[user]\n\tname = x\n")

	changes := before.changesTo(snapshotGitWorld(dir))
	if len(changes) != 1 || !strings.HasPrefix(changes[0], "the global git config") {
		t.Errorf("changes = %v, want only the global config", changes)
	}
}

// recorded collects what the canary reports to the escape recorder.
type recorded struct{ root, runner, evidence []string }

func recordGitWorldChanges(t *testing.T) *recorded {
	t.Helper()
	got := &recorded{}
	t.Cleanup(SetGitWorldRecorder(func(root, runner, evidence string, _ io.Writer) {
		got.root = append(got.root, root)
		got.runner = append(got.runner, runner)
		got.evidence = append(got.evidence, evidence)
	}))
	return got
}

func TestGitWorldWatch_AQuietRunSaysNothing(t *testing.T) {
	repo, _ := canaryRepo(t)
	rec := recordGitWorldChanges(t)
	var log bytes.Buffer

	changes := watchGitWorld(repo, "proof").verify(&log)

	if len(changes) != 0 || log.Len() != 0 || len(rec.runner) != 0 {
		t.Errorf("a quiet run reported changes %v, log %q, %d escapes", changes, log.String(), len(rec.runner))
	}
}

func TestGitWorldWatch_ALeakIsRecordedOnceAndPrintsNothing(t *testing.T) {
	repo, _ := canaryRepo(t)
	rec := recordGitWorldChanges(t)
	var log bytes.Buffer
	watch := watchGitWorld(repo, "proof")

	gitDo(t, repo, "config", "leak.key", "1")
	changes := watch.verify(&log)

	if len(changes) != 1 || !strings.HasPrefix(changes[0], "the repository's config") {
		t.Fatalf("changes = %v, want the repository's config", changes)
	}
	if log.Len() != 0 {
		t.Errorf("verify printed %q; the caller prints the one refusal line", log.String())
	}
	if len(rec.runner) != 1 || rec.runner[0] != "proof" || rec.root[0] != repo || !strings.Contains(rec.evidence[0], "+\tkey = 1") {
		t.Errorf("recorded runner %v root %v evidence %v, want one escape naming the proof, the repo and the change", rec.runner, rec.root, rec.evidence)
	}
}

// The refusal is one plain line: which runner, what changed and where.
func TestGitWorldRefusal_IsOneLineNamingTheRunnerWhatChangedAndWhere(t *testing.T) {
	got := gitWorldRefusal("measurement", "/lane", []string{"the branches changed:\n+refs/heads/x", "the tip of main changed:\n-a\n+b"})

	want := "gate: refused — a test process of the measurement changed the branches, the tip of main in /lane or the global git config; its result is not trusted (details in the escape record)"
	if got != want {
		t.Errorf("gitWorldRefusal =\n%s\nwant\n%s", got, want)
	}
	if gitWorldRefusal("proof", "/l", []string{"the global git config /h/.gitconfig changed (it was not there)"}) !=
		"gate: refused — a test process of the proof changed the global git config /h/.gitconfig in /l or the global git config; its result is not trusted (details in the escape record)" {
		t.Error("a change with no lines after it is named by its label alone")
	}
}

// Without a recorder installed a leak is still refused and printed; nothing is
// recorded and nothing fails.
func TestNoteGitWorldChange_WithNoRecorderInstalledDoesNothing(t *testing.T) {
	t.Cleanup(SetGitWorldRecorder(nil))

	NoteGitWorldChange("/lane", "proof", "the branches changed", io.Discard)
}

func TestSetGitWorldRecorder_RestoresThePreviousOne(t *testing.T) {
	var first, second int
	restoreFirst := SetGitWorldRecorder(func(string, string, string, io.Writer) { first++ })
	restoreSecond := SetGitWorldRecorder(func(string, string, string, io.Writer) { second++ })

	NoteGitWorldChange("/lane", "proof", "x", io.Discard)
	restoreSecond()
	NoteGitWorldChange("/lane", "proof", "x", io.Discard)
	restoreFirst()
	NoteGitWorldChange("/lane", "proof", "x", io.Discard)

	if first != 1 || second != 1 {
		t.Errorf("first ran %d times and second %d, want 1 and 1", first, second)
	}
}

// A world whose set of watched things differs — a repository where there was
// none — is reported, not compared item by item.
func TestChangesTo_ADifferentSetOfWatchedThingsIsAChange(t *testing.T) {
	repo, _ := canaryRepo(t)
	inside, outside := snapshotGitWorld(repo), snapshotGitWorld(t.TempDir())

	got := inside.changesTo(outside)

	if len(got) != 1 || !strings.Contains(got[0], "a repository appeared or went away") {
		t.Errorf("changes = %v, want the one line naming the difference", got)
	}
	if again := inside.changesTo(inside); len(again) != 0 {
		t.Errorf("a world differs from itself: %v", again)
	}
}

// Where git reads the global config from when GIT_CONFIG_GLOBAL does not say.
func TestGlobalGitConfigs_FollowGitsOwnSearch(t *testing.T) {
	home, xdg := filepath.Join(t.TempDir(), "home"), filepath.Join(t.TempDir(), "xdg")
	cases := []struct {
		name                    string
		global, homeDir, xdgDir string
		want                    []string
	}{
		{"named", "/named/config", home, xdg, []string{"/named/config"}},
		{"xdg set", "", home, xdg, []string{filepath.Join(xdg, "git", "config"), filepath.Join(home, ".gitconfig")}},
		{"xdg unset", "", home, "", []string{filepath.Join(home, ".config", "git", "config"), filepath.Join(home, ".gitconfig")}},
		{"no home", "", "", xdg, []string{filepath.Join(xdg, "git", "config")}},
		{"nothing", "", "", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GIT_CONFIG_GLOBAL", c.global)
			t.Setenv("HOME", c.homeDir)
			t.Setenv("USERPROFILE", c.homeDir)
			t.Setenv("XDG_CONFIG_HOME", c.xdgDir)

			got := globalGitConfigs()

			if !slices.Equal(got, c.want) {
				t.Errorf("globalGitConfigs = %v, want %v", got, c.want)
			}
		})
	}
}

// ratchet: test_removed TestGitWorldWatch_ALeakIsPrintedAndRecordedOnce: renamed TestGitWorldWatch_ALeakIsRecordedOnceAndPrintsNothing, since the refusal is one line the caller prints
// ratchet: test_removed TestGitWorldRefusal_NamesTheRunnerTheRepoAndTheChanges: replaced by TestGitWorldRefusal_IsOneLineNamingTheRunnerWhatChangedAndWhere
