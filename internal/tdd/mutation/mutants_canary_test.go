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
		leak  func(t *testing.T, repo, global string)
	}{
		{"a config key", "the repository's config", func(t *testing.T, repo, _ string) {
			gitDo(t, repo, "config", "leak.key", "1")
		}},
		{"a new branch", "the branches", func(t *testing.T, repo, _ string) {
			gitDo(t, repo, "branch", "feat/x")
		}},
		{"a moved HEAD", "this checkout's HEAD", func(t *testing.T, repo, _ string) {
			gitDo(t, repo, "switch", "-q", "-c", "other")
		}},
		{"a fixture commit", "the checked-out commit", func(t *testing.T, repo, _ string) {
			write(t, repo, "fixture.txt", "x\n")
			gitDo(t, repo, "add", "fixture.txt")
			gitDo(t, repo, "commit", "-qm", "fixture")
		}},
		{"packed refs", "packed-refs", func(t *testing.T, repo, _ string) {
			gitDo(t, repo, "pack-refs", "--all")
		}},
		{"the global config", "the global git config", func(t *testing.T, _, global string) {
			mustWrite(t, global, "[user]\n\tname = Jane Doe\n")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, global := canaryRepo(t)
			before := snapshotGitWorld(repo)

			c.leak(t, repo, global)

			changes := before.changesTo(snapshotGitWorld(repo))
			found := false
			for _, change := range changes {
				found = found || strings.HasPrefix(change, c.label)
			}
			if !found {
				t.Errorf("no change named %q among %v", c.label, changes)
			}
		})
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

func TestGitWorldWatch_ALeakIsPrintedAndRecordedOnce(t *testing.T) {
	repo, _ := canaryRepo(t)
	rec := recordGitWorldChanges(t)
	var log bytes.Buffer
	watch := watchGitWorld(repo, "proof")

	gitDo(t, repo, "config", "leak.key", "1")
	changes := watch.verify(&log)

	if len(changes) != 1 || !strings.HasPrefix(changes[0], "the repository's config") {
		t.Fatalf("changes = %v, want the repository's config", changes)
	}
	for _, want := range []string{"proof", repo, "the repository's config", "+\tkey = 1"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("the log lacks %q:\n%s", want, log.String())
		}
	}
	if len(rec.runner) != 1 || rec.runner[0] != "proof" || rec.root[0] != repo || !strings.Contains(rec.evidence[0], "+\tkey = 1") {
		t.Errorf("recorded runner %v root %v evidence %v, want one escape naming the proof, the repo and the change", rec.runner, rec.root, rec.evidence)
	}
}

// The refusal says which result it refuses and what to do.
func TestGitWorldRefusal_NamesTheRunnerTheRepoAndTheChanges(t *testing.T) {
	got := gitWorldRefusal("measurement", "/lane", []string{"the branches changed", "packed-refs changed"})

	for _, want := range []string{"measurement", "/lane", "the branches changed", "packed-refs changed", "refused"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal lacks %q: %s", want, got)
		}
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
