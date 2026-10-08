package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// A subagent's hooks carry its session's id, so the session's job records hold
// the reds of every builder it started. Each job names the agent whose edit
// started it; a stop judges only the reds of the agent that is stopping.

// agentRedJobAt records an unseen red at project started by agent ("" is the
// session's own agent).
func agentRedJobAt(t *testing.T, project, agent string) {
	t.Helper()
	redJobAt(t, project)
	j, ok := loadDeferredJob(stopSession, project)
	if !ok {
		t.Fatal("the recorded job must load back")
	}
	j.Agent = agent
	saveDeferredJob(j)
}

func TestDecideStop_StopLeavesASubagentsRedToTheSubagent(t *testing.T) {
	stopEnforceEnv(t)
	lane := makeGoRepo(t)
	builderLane := makeGoRepo(t)
	crate := filepath.Join(builderLane, "crates", "x")
	agentRedJobAt(t, crate, "a-builder")

	got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(lane)))

	if got.Block || got.Red {
		t.Fatalf("verdict = %+v, want an allow: the red is the builder's, mid-RED in its own lane", got)
	}
	if _, ok := loadDeferredJob(stopSession, crate); !ok {
		t.Fatal("the session's stop must not consume a subagent's red")
	}
}

func TestDecideStop_SubagentStopLeavesAnotherAgentsRedInItsLane(t *testing.T) {
	stopEnforceEnv(t)
	lane := makeGoRepo(t)
	crate := filepath.Join(lane, "crates", "a")
	agentRedJobAt(t, crate, "another-agent")

	got := DecideStop(StopHookSubagentStop, stopPayload(t, "subagentstop.json", stopFields(lane)))

	if got.Block || got.Red {
		t.Fatalf("verdict = %+v, want an allow: the red is another agent's", got)
	}
}

func TestDecideStop_SubagentStopBlocksOnItsOwnRed(t *testing.T) {
	stopEnforceEnv(t)
	lane := makeGoRepo(t)
	crate := filepath.Join(lane, "crates", "a")
	fields := stopFields(lane)
	fields["agent_id"] = "this-agent"
	agentRedJobAt(t, crate, "this-agent")

	got := DecideStop(StopHookSubagentStop, stopPayload(t, "subagentstop.json", fields))

	if !got.Block || !strings.Contains(got.Reason, "tests::a_breaks") {
		t.Fatalf("verdict = %+v, want a block naming the agent's own red", got)
	}
}

func TestFirstEditPhase_NamesTheAgentTheHookServes(t *testing.T) {
	core.SetHookActor("s", "a-builder")
	t.Cleanup(func() { core.SetHookActor("", "") })

	j := firstEditPhase(Runner{Cmd: "go", Args: []string{"test", "./..."}}, "/r", "/r/a.go", "h", "f", "s", "e")

	if j.Agent != "a-builder" {
		t.Fatalf("job agent = %q, want a-builder", j.Agent)
	}
}

func TestStopMode_WarnNamesTheRedAndHowToReadIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate := mkProject(t, "Cargo.toml")
	stopmodeDeclare(t, crate, "tdd = \"warn\"\n")
	redJobAt(t, crate)

	got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(crate)))

	for _, want := range []string{crate, "cd " + crate + " && aphrollo gate output"} {
		if !strings.Contains(got.Guidance, want) {
			t.Errorf("guidance = %q, want it to name %q", got.Guidance, want)
		}
	}
}

func TestStopMode_WarnNamesARedOnceAndThenStaysQuiet(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate := mkProject(t, "Cargo.toml")
	stopmodeDeclare(t, crate, "tdd = \"warn\"\n")
	redJobAt(t, crate)

	first := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(crate)))
	second := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(crate)))

	if !strings.Contains(first.Guidance, crate) {
		t.Fatalf("first guidance = %q, want it to name %s", first.Guidance, crate)
	}
	if second.Guidance != "" {
		t.Fatalf("second guidance = %q, want silence: the same red was already named", second.Guidance)
	}
	if !second.Red {
		t.Fatal("the shadow fact still holds: the agent has not been told of the red")
	}
}

func TestStopMode_WarnNamesOnlyTheRedItHasNotNamedYet(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	stopmodeDeclare(t, root, "tdd = \"warn\"\n")
	first := filepath.Join(root, "crates", "a")
	later := filepath.Join(root, "crates", "b")
	redJobAt(t, first)
	DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(root)))
	redJobAt(t, later)

	got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(root)))

	if !strings.Contains(got.Guidance, later) || strings.Contains(got.Guidance, first+":") {
		t.Fatalf("guidance = %q, want only the new red in %s", got.Guidance, later)
	}
}

func TestDecideStop_ARedInACheckoutGoneFromDiskIsNotCounted(t *testing.T) {
	stopEnforceEnv(t)
	lane := makeGoRepo(t)
	gone := filepath.Join(t.TempDir(), "lane-removed")
	redJobAt(t, gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(lane)))

	if got.Block || got.Red {
		t.Fatalf("verdict = %+v, want an allow: no hook can ever reach a checkout that is gone", got)
	}
}
