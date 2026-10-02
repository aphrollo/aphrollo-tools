package postedit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The stop checks answer the three hooks that end something: Stop (the turn),
// SubagentStop (a subagent's turn) and TaskCompleted (a task marked done). The
// payloads under testdata/stophooks are recorded from Claude Code's documented
// hook inputs; each test overlays only the fields it controls, so the rest of
// the payload stays what the harness really sends.

const stopSession = "stop-sess"

// stopPayload reads a recorded hook payload and overlays the fields a test
// controls with the documented key names.
func stopPayload(t *testing.T, fixture string, overlay map[string]any) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "stophooks", fixture))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for k, v := range overlay {
		m[k] = v
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// finishedJobWith records a run (or build) phase for project under stopSession
// that matches its source as it stands. A nil out is a job still running.
func finishedJobWith(t *testing.T, project, phase string, out *PhaseOutcome, log string) {
	t.Helper()
	target := filepath.Join(project, "src", "lib.rs")
	write(t, project, "src/lib.rs", "pub fn a() {}\n")
	saveDeferredJob(DeferredJob{
		Project: project, Session: stopSession, Phase: phase, Dir: project, PID: 4242,
		Started: time.Now().Add(-time.Minute), File: target,
		HeadSHA: headSHAFor(project), FileHash: sourceIdentity(project, target),
		Runner: []string{"cargo", "test", "-p", "crate_a"},
	})
	job, ok := loadDeferredJob(stopSession, project)
	if !ok {
		t.Fatal("the recorded job must load back")
	}
	if err := os.WriteFile(job.Log, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if out != nil {
		writePhaseResult(job.Result, *out)
	}
}

// redJobAt records the unseen red every blocking test starts from.
func redJobAt(t *testing.T, project string) {
	t.Helper()
	write(t, project, "src/lib.rs", "pub fn a() {}\n")
	RecordFinishedRedDeferredJobForTest(project, filepath.Join(project, "src", "lib.rs"), stopSession, "tests::a_breaks")
}

func stopFields(cwd string) map[string]any {
	return map[string]any{"session_id": stopSession, "cwd": cwd}
}

func TestDecideStop_BlocksTheTurnOnceOnAnUnseenRedAndNamesItsGateLine(t *testing.T) {
	for _, tc := range []struct {
		event   StopEvent
		fixture string
	}{
		{StopHookStop, "stop.json"},
		{StopHookSubagentStop, "subagentstop.json"},
	} {
		t.Run(string(tc.event), func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			crate := mkProject(t, "Cargo.toml")
			redJobAt(t, crate)

			got := DecideStop(tc.event, stopPayload(t, tc.fixture, stopFields(crate)))

			if !got.Block {
				t.Fatalf("verdict = %+v, want a block: a deferred run finished red after Claude's last hook", got)
			}
			for _, want := range []string{"gate: deferred", "tests::a_breaks", crate, "cargo test -p crate_a"} {
				if !strings.Contains(got.Reason, want) {
					t.Fatalf("reason = %q, want the red's own gate line carrying %q", got.Reason, want)
				}
			}
			if _, ok := loadDeferredJob(stopSession, crate); ok {
				t.Fatal("the red the block carried is seen now: its record must be cleared so no later hook tells it twice")
			}
			again := DecideStop(tc.event, stopPayload(t, tc.fixture, stopFields(crate)))
			if again.Block {
				t.Fatalf("second check = %+v, want an allow: the red was already delivered once", again)
			}
		})
	}
}

func TestDecideStop_AllowsWhenStopHookActiveSaysTheTurnIsAlreadyContinuing(t *testing.T) {
	for _, tc := range []struct {
		event   StopEvent
		fixture string
	}{
		{StopHookStop, "stop.json"},
		{StopHookSubagentStop, "subagentstop.json"},
	} {
		t.Run(string(tc.event), func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			crate := mkProject(t, "Cargo.toml")
			redJobAt(t, crate)
			fields := stopFields(crate)
			fields["stop_hook_active"] = true

			got := DecideStop(tc.event, stopPayload(t, tc.fixture, fields))

			if got.Block {
				t.Fatalf("verdict = %+v, want an allow: never block twice in a row", got)
			}
			if _, ok := loadDeferredJob(stopSession, crate); !ok {
				t.Fatal("an allowed stop must leave the red for the next hook to report, not consume it unseen")
			}
		})
	}
}

func TestDecideStop_AllowsARedClaudeAlreadySaw(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate := mkProject(t, "Cargo.toml")
	redJobAt(t, crate)
	if told := promptHarvest(stopSession); !strings.Contains(told, "tests::a_breaks") {
		t.Fatalf("harvest = %q, want the red delivered to Claude before it stops", told)
	}

	got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(crate)))

	if got.Block {
		t.Fatalf("verdict = %+v, want an allow: a red Claude has seen may end the turn", got)
	}
}

func TestDecideStop_AllowsWhenTheSessionTurnedTheGateOff(t *testing.T) {
	for _, event := range []StopEvent{StopHookStop, StopHookSubagentStop} {
		t.Run(string(event), func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			crate := mkProject(t, "Cargo.toml")
			redJobAt(t, crate)
			state, path := loadSession(stopSession)
			state.Overrides.Off = true
			if err := state.Save(path); err != nil {
				t.Fatal(err)
			}
			fixture := map[StopEvent]string{StopHookStop: "stop.json", StopHookSubagentStop: "subagentstop.json"}[event]

			got := DecideStop(event, stopPayload(t, fixture, stopFields(crate)))

			if got.Block {
				t.Fatalf("verdict = %+v, want an allow: /tdd off switches the stop check off", got)
			}
			if _, ok := loadDeferredJob(stopSession, crate); !ok {
				t.Fatal("a switched-off check must not consume the job either")
			}
		})
	}
}

// A finished run that is not a red leaves the turn alone and stays on disk for
// the next hook, which tells Claude what it said.
func TestDecideStop_AllowsEveryFinishedRunThatIsNotARedAndLeavesItForTheNextHook(t *testing.T) {
	for _, tc := range []struct {
		name  string
		phase string
		out   *PhaseOutcome
		log   string
	}{
		{"still running", "run", nil, ""},
		{"green run", "run", &PhaseOutcome{ExitCode: 0, Seconds: 3}, "test result: ok. 4 passed; 0 failed\n"},
		{"build finished, no test ran yet", "build", &PhaseOutcome{ExitCode: 0, Seconds: 9}, "Finished `test` profile\n"},
		{"the phase's own setup failed", "run", &PhaseOutcome{ExitCode: 1, SetupFailed: true}, "no build slot came free\n"},
		{"killed by the memory cap", "run", &PhaseOutcome{ExitCode: 1, Inconclusive: "memory cap"}, "killed\n"},
		{"nextest found no test to run", "run", &PhaseOutcome{ExitCode: 4, Seconds: 2}, "error: no tests to run\n"},
		{"every failed test timed out", "run", &PhaseOutcome{ExitCode: 1, Seconds: 600}, "panic: test timed out after 10m0s\n\nFAIL\texample.com/a\t600.003s\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			crate := mkProject(t, "Cargo.toml")
			finishedJobWith(t, crate, tc.phase, tc.out, tc.log)

			got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(crate)))

			if got.Block {
				t.Fatalf("verdict = %+v, want an allow: %s is not a red", got, tc.name)
			}
			if _, ok := loadDeferredJob(stopSession, crate); !ok {
				t.Fatalf("%s: the record must stay for the next hook to report", tc.name)
			}
		})
	}
}

func TestDecideStop_BlocksOnAFailedBuildBecauseACompileErrorIsARed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate := mkProject(t, "Cargo.toml")
	finishedJobWith(t, crate, "build", &PhaseOutcome{ExitCode: 101, Seconds: 4},
		"error[E0425]: cannot find function `backoff` in this scope\n")

	got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(crate)))

	if !got.Block || !strings.Contains(got.Reason, crate) {
		t.Fatalf("verdict = %+v, want a block naming %s: the build failed", got, crate)
	}
}

// A result the tree has moved past is no verdict on the current code, but the
// harvest already reports a stale red because it is usually a real break the
// session made, so the stop check hands it over once, labelled as stale.
func TestDecideStop_BlocksOnAStaleRedAndLabelsItAsAnEarlierTreeState(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate := mkProject(t, "Cargo.toml")
	redJobAt(t, crate)
	write(t, crate, "src/lib.rs", "pub fn a() { moved_on() }\n")

	got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(crate)))

	if !got.Block || !strings.Contains(got.Reason, "measured on an earlier tree state") {
		t.Fatalf("verdict = %+v, want a block carrying the stale label", got)
	}
}

func TestDecideStop_SubagentStopChecksOnlyTheSubagentsLane(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane := makeGoRepo(t)
	otherLane := makeGoRepo(t)
	crateInLane := filepath.Join(lane, "crates", "a")
	redJobAt(t, crateInLane)
	redJobAt(t, filepath.Join(otherLane, "crates", "x"))
	if err := os.MkdirAll(filepath.Join(lane, "crates", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	subagentCwd := filepath.Join(lane, "crates", "b")

	got := DecideStop(StopHookSubagentStop, stopPayload(t, "subagentstop.json", stopFields(subagentCwd)))

	if !got.Block || !strings.Contains(got.Reason, crateInLane) {
		t.Fatalf("verdict = %+v, want a block naming the red in the subagent's own lane %s", got, crateInLane)
	}
	if strings.Contains(got.Reason, otherLane) {
		t.Fatalf("reason = %q, must not carry another lane's red", got.Reason)
	}
	if _, ok := loadDeferredJob(stopSession, filepath.Join(otherLane, "crates", "x")); !ok {
		t.Fatal("another lane's red is not the subagent's to consume")
	}

	alone := DecideStop(StopHookSubagentStop, stopPayload(t, "subagentstop.json", stopFields(subagentCwd)))
	if alone.Block {
		t.Fatalf("second check = %+v, want an allow: the only red in this lane was delivered", alone)
	}
}

func TestDecideStop_StopChecksEveryTreeTheSessionStartedARunIn(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane := makeGoRepo(t)
	otherLane := makeGoRepo(t)
	redJobAt(t, filepath.Join(otherLane, "crates", "x"))

	got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(lane)))

	if !got.Block || !strings.Contains(got.Reason, filepath.Join(otherLane, "crates", "x")) {
		t.Fatalf("verdict = %+v, want a block: the turn's red lives in %s, whatever cwd the harness left", got, otherLane)
	}
}

func TestDecideStop_AnotherSessionsRedIsNotThisSessions(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate := mkProject(t, "Cargo.toml")
	redJobAt(t, crate)
	fields := stopFields(crate)
	fields["session_id"] = "some-other-session"

	got := DecideStop(StopHookStop, stopPayload(t, "stop.json", fields))

	if got.Block {
		t.Fatalf("verdict = %+v, want an allow: that run belongs to %s", got, stopSession)
	}
}

func TestDecideStop_FailsOpenOnAPayloadItCannotRead(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate := mkProject(t, "Cargo.toml")
	redJobAt(t, crate)

	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"empty", nil},
		{"not json", []byte("{not json")},
		{"no session id", stopPayload(t, "stop.json", map[string]any{"session_id": "", "cwd": crate})},
		{"a field of the wrong type", stopPayload(t, "stop.json", map[string]any{"session_id": stopSession, "cwd": 7})},
		{"the recorded payload, a session nothing ran in", stopPayload(t, "stop.json", nil)},
	} {
		for _, event := range []StopEvent{StopHookStop, StopHookSubagentStop, StopHookTaskCompleted} {
			if got := DecideStop(event, tc.raw); got.Block {
				t.Errorf("%s %s: verdict = %+v, want an allow", event, tc.name, got)
			}
		}
	}
}

// A payload that names no cwd cannot say which lane or tree it ends, so the
// two checks that are scoped to one allow rather than guess.
func TestDecideStop_SubagentStopAndTaskCompletedAllowWhenThePayloadNamesNoCwd(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate := mkProject(t, "Cargo.toml")
	redJobAt(t, crate)
	// Even a resolver that would find the red must not be asked about no cwd.
	prev := stopTreeFn
	stopTreeFn = func(string) string { return crate }
	t.Cleanup(func() { stopTreeFn = prev })

	for event, fixture := range map[StopEvent]string{StopHookSubagentStop: "subagentstop.json", StopHookTaskCompleted: "taskcompleted.json"} {
		got := DecideStop(event, stopPayload(t, fixture, map[string]any{"session_id": stopSession, "cwd": ""}))

		if got.Block {
			t.Errorf("%s: verdict = %+v, want an allow: no cwd, no lane to check", event, got)
		}
	}
	if _, ok := loadDeferredJob(stopSession, crate); !ok {
		t.Fatal("an allow must leave the red for the next hook")
	}
}

// The check runs on every turn end, so the path with nothing to report reads
// two small files and resolves no tree: asking git for one is the cost this
// guards against.
func TestDecideStop_NothingToReportResolvesNoTree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	prev := stopTreeFn
	stopTreeFn = func(cwd string) string {
		t.Errorf("resolved the tree of %q with nothing to report", cwd)
		return cwd
	}
	t.Cleanup(func() { stopTreeFn = prev })
	crate := mkProject(t, "Cargo.toml")
	finishedJobWith(t, crate, "run", &PhaseOutcome{ExitCode: 0}, "test result: ok. 1 passed\n")
	stampProject(t, crate, "green", nil)

	for event, fixture := range map[StopEvent]string{
		StopHookStop: "stop.json", StopHookSubagentStop: "subagentstop.json", StopHookTaskCompleted: "taskcompleted.json",
	} {
		if got := DecideStop(event, stopPayload(t, fixture, stopFields(crate))); got.Block {
			t.Errorf("%s: verdict = %+v, want an allow", event, got)
		}
	}
}

func TestRenderStopVerdict_StopAndSubagentStopBlockWithADecisionOnStdout(t *testing.T) {
	v := StopVerdict{Block: true, Reason: "gate: deferred cargo test → outcome=red"}
	for _, event := range []StopEvent{StopHookStop, StopHookSubagentStop} {
		stdout, stderr, code := RenderStopVerdict(event, v)

		var got map[string]any
		if err := json.Unmarshal(stdout, &got); err != nil {
			t.Fatalf("%s: stdout %q is not JSON: %v", event, stdout, err)
		}
		if got["decision"] != "block" || got["reason"] != v.Reason {
			t.Errorf("%s: output = %v, want decision block with the reason", event, got)
		}
		if _, ok := got["hookSpecificOutput"]; ok {
			t.Errorf("%s: output = %v, Stop events have no hookSpecificOutput: the harness drops the payload", event, got)
		}
		if code != 0 || len(stderr) != 0 {
			t.Errorf("%s: code=%d stderr=%q, want exit 0 and a silent stderr", event, code, stderr)
		}
	}
}

func TestRenderStopVerdict_TaskCompletedExitsTwoWithTheReasonOnStderr(t *testing.T) {
	stdout, stderr, code := RenderStopVerdict(StopHookTaskCompleted, StopVerdict{Block: true, Reason: "gate: task kept open"})

	if code != 2 || !strings.Contains(string(stderr), "gate: task kept open") || len(stdout) != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q, want exit 2 with the reason on stderr", code, stdout, stderr)
	}
}

func TestRenderStopVerdict_AnAllowRendersNothingForEveryEvent(t *testing.T) {
	for _, event := range []StopEvent{StopHookStop, StopHookSubagentStop, StopHookTaskCompleted} {
		stdout, stderr, code := RenderStopVerdict(event, StopVerdict{})
		if len(stdout) != 0 || len(stderr) != 0 || code != 0 {
			t.Errorf("%s: stdout=%q stderr=%q code=%d, want silence and exit 0", event, stdout, stderr, code)
		}
	}
}
