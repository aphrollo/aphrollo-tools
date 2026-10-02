package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TaskCompleted keeps a task open while the tests of the tree it ran in are
// red. "Red" is what the session last recorded for a project of that tree, so
// the check reads the gate's own state: a project whose last run failed, under
// the git state it failed in.

// stampProject records outcome for project under stopSession, fingerprinted to
// the git state the project is in now.
func stampProject(t *testing.T, project, outcome string, failing []string) {
	t.Helper()
	state, path := loadSession(stopSession)
	state.Stamp(project, projectState{Outcome: outcome, FailingTests: failing, Fingerprint: computeFingerprint(project)})
	if err := state.Save(path); err != nil {
		t.Fatal(err)
	}
}

func taskFields(cwd string) map[string]any {
	return map[string]any{"session_id": stopSession, "cwd": cwd}
}

func TestDecideStop_TaskCompletedKeepsTheTaskOpenNamingTheFailingTests(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	stampProject(t, repo, "red", []string{"TestRetry/backoff", "TestRetry/jitter"})

	got := DecideStop(StopHookTaskCompleted, stopPayload(t, "taskcompleted.json", taskFields(repo)))

	if !got.Block {
		t.Fatalf("verdict = %+v, want the task kept open: its tests are red", got)
	}
	for _, want := range []string{"TestRetry/backoff", "TestRetry/jitter", repo} {
		if !strings.Contains(got.Reason, want) {
			t.Fatalf("reason = %q, want it to name %q", got.Reason, want)
		}
	}
}

func TestDecideStop_TaskCompletedJudgesTheLastRecordedOutcomeOfTheTree(t *testing.T) {
	for _, tc := range []struct {
		outcome string
		failing []string
		block   bool
	}{
		{"red", []string{"TestA"}, true},
		{"red-missing-impl", nil, true},
		{"red-bogus", nil, true},
		{"no-delta", []string{"TestA"}, true},
		{"green", nil, false},
		{"green-with-warnings", nil, false},
		{"writing-test", nil, false},
	} {
		t.Run(tc.outcome, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			repo := makeGoRepo(t)
			stampProject(t, repo, tc.outcome, tc.failing)

			got := DecideStop(StopHookTaskCompleted, stopPayload(t, "taskcompleted.json", taskFields(repo)))

			if got.Block != tc.block {
				t.Fatalf("outcome %s: verdict = %+v, want block=%v", tc.outcome, got, tc.block)
			}
			if tc.block && !strings.Contains(got.Reason, tc.outcome) {
				t.Fatalf("outcome %s: reason = %q, want it to name the outcome", tc.outcome, got.Reason)
			}
			if tc.block && len(tc.failing) == 0 && strings.Contains(got.Reason, "failing tests") {
				t.Fatalf("outcome %s: reason = %q, names failing tests the run never parsed", tc.outcome, got.Reason)
			}
		})
	}
}

func TestDecideStop_TaskCompletedFindsARedProjectNestedInTheTasksTree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	crate := filepath.Join(repo, "crates", "a")
	if err := os.MkdirAll(crate, 0o755); err != nil {
		t.Fatal(err)
	}
	stampProject(t, crate, "red", []string{"tests::a_breaks"})
	cwd := filepath.Join(repo, "crates")

	got := DecideStop(StopHookTaskCompleted, stopPayload(t, "taskcompleted.json", taskFields(cwd)))

	if !got.Block || !strings.Contains(got.Reason, "tests::a_breaks") {
		t.Fatalf("verdict = %+v, want the task kept open for the red crate under the repo", got)
	}
}

func TestDecideStop_TaskCompletedIgnoresARedOfAnotherTree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	otherLane := makeGoRepo(t)
	stampProject(t, otherLane, "red", []string{"TestElsewhere"})

	got := DecideStop(StopHookTaskCompleted, stopPayload(t, "taskcompleted.json", taskFields(repo)))

	if got.Block {
		t.Fatalf("verdict = %+v, want an allow: that red is in %s, not this task's tree", got, otherLane)
	}
}

// A red stamped under an older git state is not what the tree is now: a commit
// is gated on a green suite, so the red is history.
func TestDecideStop_TaskCompletedIgnoresARedStampedUnderAnEarlierGitState(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	stampProject(t, repo, "red", []string{"TestA"})
	write(t, repo, "later.go", "package later\n")
	gitDo(t, repo, "add", "-A")
	gitDo(t, repo, "-c", "core.hooksPath=", "commit", "-q", "-m", "later", "--no-verify")

	got := DecideStop(StopHookTaskCompleted, stopPayload(t, "taskcompleted.json", taskFields(repo)))

	if got.Block {
		t.Fatalf("verdict = %+v, want an allow: the red describes a git state the tree has left", got)
	}
}

// A recorded outcome the gate could not pin to a git state — none was taken, or
// the project is no repository now — says nothing about the tree as it is.
func TestDecideStop_TaskCompletedIgnoresARedWithNoGitStateToCompareTo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	state, path := loadSession(stopSession)
	state.Stamp(repo, projectState{Outcome: "red", FailingTests: []string{"TestA"}})
	plain := t.TempDir()
	state.Stamp(plain, projectState{Outcome: "red", FailingTests: []string{"TestB"}, Fingerprint: &fingerprint{Branch: "main", HeadSHA: "0123abc"}})
	if err := state.Save(path); err != nil {
		t.Fatal(err)
	}

	for _, cwd := range []string{repo, plain} {
		got := DecideStop(StopHookTaskCompleted, stopPayload(t, "taskcompleted.json", taskFields(cwd)))

		if got.Block {
			t.Errorf("cwd %s: verdict = %+v, want an allow: the red has no git state to match", cwd, got)
		}
	}
}

// A green project beside a red one in the same tree is not what holds the task
// open, so the reason never names it.
func TestDecideStop_TaskCompletedDoesNotNameAGreenProjectBesideARedOne(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	red, green := filepath.Join(repo, "crates", "red"), filepath.Join(repo, "crates", "green")
	for _, dir := range []string{red, green} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stampProject(t, red, "red", []string{"Test_red"})
	stampProject(t, green, "green", nil)

	got := DecideStop(StopHookTaskCompleted, stopPayload(t, "taskcompleted.json", taskFields(repo)))

	if !got.Block || !strings.Contains(got.Reason, red) {
		t.Fatalf("verdict = %+v, want the task kept open for %s", got, red)
	}
	if strings.Contains(got.Reason, green) {
		t.Fatalf("reason = %q, names the green project %s", got.Reason, green)
	}
}

func TestDecideStop_TaskCompletedNamesEveryRedProjectInOrder(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	for _, name := range []string{"d", "b", "c", "a"} {
		crate := filepath.Join(repo, "crates", name)
		if err := os.MkdirAll(crate, 0o755); err != nil {
			t.Fatal(err)
		}
		stampProject(t, crate, "red", []string{"Test_" + name})
	}

	got := DecideStop(StopHookTaskCompleted, stopPayload(t, "taskcompleted.json", taskFields(repo)))

	last := -1
	for _, name := range []string{"a", "b", "c", "d"} {
		at := strings.Index(got.Reason, "Test_"+name)
		if at < 0 || at < last {
			t.Fatalf("reason = %q, want Test_a, Test_b, Test_c, Test_d each present and in that order", got.Reason)
		}
		last = at
	}
}

func TestDecideStop_TaskCompletedAllowsWhenTheSessionTurnedTheGateOff(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	stampProject(t, repo, "red", []string{"TestA"})
	state, path := loadSession(stopSession)
	state.Overrides.Off = true
	if err := state.Save(path); err != nil {
		t.Fatal(err)
	}

	got := DecideStop(StopHookTaskCompleted, stopPayload(t, "taskcompleted.json", taskFields(repo)))

	if got.Block {
		t.Fatalf("verdict = %+v, want an allow: /tdd off switches the check off", got)
	}
}

// A task closing is one more place Claude can hear a red it never saw; the
// check delivers it with the failing names, exactly as a stop would.
func TestDecideStop_TaskCompletedDeliversAnUnseenRedAndKeepsTheTaskOpen(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	crate := filepath.Join(repo, "crates", "a")
	redJobAt(t, crate)

	got := DecideStop(StopHookTaskCompleted, stopPayload(t, "taskcompleted.json", taskFields(repo)))

	if !got.Block || !strings.Contains(got.Reason, "tests::a_breaks") || !strings.Contains(got.Reason, crate) {
		t.Fatalf("verdict = %+v, want the task kept open with the unseen red's line", got)
	}
	if _, ok := loadDeferredJob(stopSession, crate); ok {
		t.Fatal("the red the task check carried is seen now: its record must be cleared")
	}
}

// Unlike a stop, a task is not released by a second ask: it stays open for as
// long as its tests are red.
func TestDecideStop_TaskCompletedKeepsBlockingWhileTheTestsStayRed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGoRepo(t)
	stampProject(t, repo, "red", []string{"TestA"})

	for range 3 {
		got := DecideStop(StopHookTaskCompleted, stopPayload(t, "taskcompleted.json", taskFields(repo)))
		if !got.Block {
			t.Fatalf("verdict = %+v, want the task kept open on every ask", got)
		}
	}
}
