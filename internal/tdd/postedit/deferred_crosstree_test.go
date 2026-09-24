package postedit

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// A deferred job is harvested by the edit hook only for the tree the edit
// touched. A session that edits crate A (its build goes deferred), then
// crate B, then ends the turn never heard A's verdict: B's hook looked for a
// job under B alone, and nothing else fires before the next prompt. If A went
// red, the session never learned it.

// finishedRedJob records a run phase for crate root under session that has
// already finished red, with a failing test named in its log, matching the
// crate's source as it stands.
func finishedRedJob(t *testing.T, session, root, failing string) {
	t.Helper()
	target := root + "/src/lib.rs"
	write(t, root, "src/lib.rs", "pub fn a() {}\n")
	saveDeferredJob(DeferredJob{
		Project: root, Session: session, Phase: "run", Dir: root, PID: 4242,
		Started: time.Now().Add(-time.Minute), File: target,
		HeadSHA: headSHAFor(root), FileHash: sourceIdentity(root, target),
		Runner: []string{"cargo", "test", "-p", "crate_a"},
	})
	job, _ := loadDeferredJob(session, root)
	log := "running 1 test\ntest " + failing + " ... FAILED\n\nfailures:\n    " + failing +
		"\n\ntest result: FAILED. 0 passed; 1 failed; 0 ignored\n"
	if err := os.WriteFile(job.Log, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	writePhaseResult(job.Result, PhaseOutcome{ExitCode: 101, Seconds: 30})
}

func TestPostEdit_ReportsAnotherTreesFinishedJobOnceNamingItsTreeAndCommand(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crateA := mkProject(t, "Cargo.toml")
	crateB := mkProject(t, "Cargo.toml")
	finishedRedJob(t, "sess-post", crateA, "tests::a_breaks")
	done := &PhaseOutcome{ExitCode: 0, Seconds: 1}
	fakePhases(t, done, done, done, done)

	got := PostEdit(postPayload("Edit", crateB+"/src/widget.rs"), fakeRun(true, "ok"))

	if !strings.Contains(got, "tests::a_breaks") {
		t.Fatalf("advisory = %q, want crate A's red verdict naming tests::a_breaks", got)
	}
	if !strings.Contains(got, crateA) || !strings.Contains(got, "cargo test -p crate_a") {
		t.Fatalf("advisory = %q, want A's verdict attributed to %s and its own command", got, crateA)
	}
	if _, ok := loadDeferredJob("sess-post", crateA); ok {
		t.Fatal("a reported job must be cleared so no later hook reports it again")
	}
	again := PostEdit(postPayload("Edit", crateB+"/src/widget.rs"), fakeRun(true, "ok"))
	if strings.Contains(again, crateA) {
		t.Fatalf("second advisory = %q, crate A's verdict was already reported once", again)
	}
}

// The Bash hook is a sibling path to the edit hook: a turn whose last tool
// call is a shell command that changed nothing must still carry the verdict
// an earlier edit left running.
func TestPostBash_ReportsAnEarlierEditsFinishedJobWhenTheCommandChangedNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crateA := mkProject(t, "Cargo.toml")
	finishedRedJob(t, "sess-post", crateA, "tests::a_breaks")

	raw, err := json.Marshal(map[string]any{
		"session_id": "sess-post", "tool_name": "Bash", "cwd": t.TempDir(),
		"tool_input": map[string]any{"command": "ls"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := PostBash(raw, fakeRun(true, "ok"))

	if !strings.HasPrefix(got, "gate: ") || !strings.Contains(got, "tests::a_breaks") || !strings.Contains(got, crateA) {
		t.Fatalf("advisory = %q, want it to open on crate A's red verdict naming tests::a_breaks and %s", got, crateA)
	}
	if _, ok := loadDeferredJob("sess-post", crateA); ok {
		t.Fatal("a reported job must be cleared so no later hook reports it again")
	}
}

// A finished BUILD is not a verdict: its log holds a compile, not a test
// run. Reported as one, a warm build read as green for tests that never ran.
// The sweep continues it into its run phase, as the edit hook does for its
// own tree, and the line that says so names the command now running.
func TestPostEdit_AnotherTreesFinishedBuildStartsItsRunNamingTheCommand(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "0")
	crateA := mkProject(t, "Cargo.toml")
	crateB := mkProject(t, "Cargo.toml")
	target := crateA + "/src/lib.rs"
	write(t, crateA, "src/lib.rs", "pub fn a() {}\n")
	saveDeferredJob(DeferredJob{
		Project: crateA, Session: "sess-post", Phase: "build", Dir: crateA, PID: 4242,
		Started: time.Now().Add(-time.Minute), File: target,
		HeadSHA: headSHAFor(crateA), FileHash: sourceIdentity(crateA, target),
		Runner: []string{"cargo", "test", "-p", "crate_a", "--no-run"},
	})
	job, _ := loadDeferredJob("sess-post", crateA)
	writePhaseResult(job.Result, PhaseOutcome{ExitCode: 0, Seconds: 30})
	spawned := fakePhases(t) // nothing finishes: B's build and A's run stay running

	got := PostEdit(postPayload("Edit", crateB+"/src/widget.rs"), fakeRun(true, "ok"))

	var ranA []string
	for _, j := range *spawned {
		if j.Project == crateA {
			ranA = j.Runner
		}
	}
	if strings.Join(ranA, " ") != "cargo test -p crate_a" {
		t.Fatalf("spawned %+v, want crate A's run phase `cargo test -p crate_a`", *spawned)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("advisory = %q, want B's own line then one line for A", got)
	}
	if strings.Contains(lines[1], "green") || !strings.Contains(lines[1], crateA) ||
		!strings.HasSuffix(lines[1], " [cargo test -p crate_a]") {
		t.Fatalf("A's line = %q, want A's run reported as running, naming %s and ending in its command", lines[1], crateA)
	}
}

// withCommand leaves a multi-line verdict's body alone: the command lands on
// the headline, which is the line a reader matches a verdict by.
func TestWithCommand_NamesTheCommandOnTheHeadlineOfAMultiLineVerdict(t *testing.T) {
	j := DeferredJob{Runner: []string{"cargo", "test", "-p", "a"}}

	got := withCommand("gate: → infra-failed in /w/a\nbody", j)

	if got != "gate: → infra-failed in /w/a [cargo test -p a]\nbody" {
		t.Fatalf("got %q", got)
	}
	named := "gate: cargo test -p a in /w/a → green"
	if again := withCommand(named, j); again != named {
		t.Fatalf("a line already naming the command came back as %q", again)
	}
}

// ratchet: test_removed TestPromptHarvest_ClearsTheRecordOfAResultItDroppedAsStale: a stale result is no longer dropped; the test below pins that it is reported, labelled, and its record cleared.

// A result that no longer describes its tree is not a verdict on the
// current code, but a red there usually is a real break the session caused:
// it is reported once, labelled as measured on an earlier tree state, and
// its record goes with it.
func TestPostEdit_ReportsAnotherTreesStaleRedLabelledAsAnEarlierTreeState(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crateA := mkProject(t, "Cargo.toml")
	crateB := mkProject(t, "Cargo.toml")
	finishedRedJob(t, "sess-post", crateA, "tests::a_breaks")
	write(t, crateA, "src/lib.rs", "pub fn a() { moved_on() }\n")
	done := &PhaseOutcome{ExitCode: 0, Seconds: 1}
	fakePhases(t, done, done)

	got := PostEdit(postPayload("Edit", crateB+"/src/widget.rs"), fakeRun(true, "ok"))

	var lineA string
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, crateA) {
			lineA = l
		}
	}
	for _, want := range []string{"gate: deferred", "cargo test -p crate_a", "red", "tests::a_breaks",
		"measured on an earlier tree state", "the current code was NOT tested"} {
		if !strings.Contains(lineA, want) {
			t.Fatalf("A's line = %q (advisory %q), want it to carry %q", lineA, got, want)
		}
	}
	if _, ok := loadDeferredJob("sess-post", crateA); ok {
		t.Fatal("a reported job must be cleared so no later hook reports it again")
	}
}
