package postedit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tdd setting grades the Stop and SubagentStop checks: enforce blocks the
// turn on an unseen red (today's behaviour, and the live default until the
// phase A A/B flips it), warn only says so, off says nothing. /tdd off still
// wins over all three.

// stopmodeDeclare makes project a repo that declares text in its trellis.toml.
func stopmodeDeclare(t *testing.T, project, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "trellis.toml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func stopmodeUserConfig(t *testing.T, text string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TRELLIS_CONFIG", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

var stopmodeHooks = []struct {
	event   StopEvent
	fixture string
}{
	{StopHookStop, "stop.json"},
	{StopHookSubagentStop, "subagentstop.json"},
}

// ratchet: test_removed TestStopMode_WithNothingDeclaredTheStopStillBlocks: the built-in is now warn, so this is the same case under its new name and expectation
func TestStopMode_WithNothingDeclaredOutsideALaneTheStopOnlyGuides(t *testing.T) {
	for _, tc := range stopmodeHooks {
		t.Run(string(tc.event), func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			crate := mkProject(t, "Cargo.toml")
			redJobAt(t, crate)
			if got := DecideStop(tc.event, stopPayload(t, tc.fixture, stopFields(crate))); got.Block {
				t.Fatalf("verdict = %+v, want no block: the built-in is warn, and a directory with no lane is in no arm", got)
			}
		})
	}
}

func TestStopMode_EnforceBlocksAsToday(t *testing.T) {
	for _, tc := range stopmodeHooks {
		t.Run(string(tc.event), func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			crate := mkProject(t, "Cargo.toml")
			stopmodeDeclare(t, crate, "tdd = \"enforce\"\n")
			redJobAt(t, crate)
			got := DecideStop(tc.event, stopPayload(t, tc.fixture, stopFields(crate)))
			if !got.Block || !strings.Contains(got.Reason, "tests::a_breaks") {
				t.Fatalf("verdict = %+v, want a block naming the red", got)
			}
		})
	}
}

func TestStopMode_WarnNeverBlocksGivesGuidanceAndLeavesTheRedForTheNextHook(t *testing.T) {
	for _, tc := range stopmodeHooks {
		t.Run(string(tc.event), func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			crate := mkProject(t, "Cargo.toml")
			stopmodeDeclare(t, crate, "tdd = \"warn\"\n")
			redJobAt(t, crate)

			got := DecideStop(tc.event, stopPayload(t, tc.fixture, stopFields(crate)))

			if got.Block || got.Reason != "" {
				t.Fatalf("verdict = %+v, want no block under warn", got)
			}
			if !strings.Contains(got.Guidance, "tdd = warn") {
				t.Fatalf("guidance = %q, want it to say the stop was let through by tdd = warn", got.Guidance)
			}
			if !got.Red {
				t.Fatal("the finding the shadow week records is aphrollo's own, whatever the setting does about it")
			}
			if _, ok := loadDeferredJob(stopSession, crate); !ok {
				t.Fatal("guidance must not consume the red: the agent has still not been told, and its next hook reports it")
			}
		})
	}
}

func TestStopMode_WarnWithNothingRedSaysNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate := mkProject(t, "Cargo.toml")
	stopmodeDeclare(t, crate, "tdd = \"warn\"\n")
	got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(crate)))
	if got.Block || got.Guidance != "" || got.Red {
		t.Fatalf("verdict = %+v, want nothing to say", got)
	}
}

func TestStopMode_WarnStaysQuietWhileTheTurnIsAlreadyContinuing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate := mkProject(t, "Cargo.toml")
	stopmodeDeclare(t, crate, "tdd = \"warn\"\n")
	redJobAt(t, crate)
	fields := stopFields(crate)
	fields["stop_hook_active"] = true
	if got := DecideStop(StopHookStop, stopPayload(t, "stop.json", fields)); got.Block || got.Guidance != "" {
		t.Fatalf("verdict = %+v, want nothing while stop_hook_active", got)
	}
}

func TestStopMode_OffNeverBlocksAndLeavesTheRedAlone(t *testing.T) {
	for _, tc := range stopmodeHooks {
		t.Run(string(tc.event), func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			crate := mkProject(t, "Cargo.toml")
			stopmodeDeclare(t, crate, "tdd = \"off\"\n")
			redJobAt(t, crate)
			got := DecideStop(tc.event, stopPayload(t, tc.fixture, stopFields(crate)))
			if got.Block || got.Guidance != "" || got.Reason != "" {
				t.Fatalf("verdict = %+v, want nothing under off", got)
			}
			if !got.Red {
				t.Fatal("the red fact is recorded whatever the setting does about it")
			}
			if _, ok := loadDeferredJob(stopSession, crate); !ok {
				t.Fatal("an off check must not consume the red")
			}
		})
	}
}

func TestStopMode_TheUserLayerSetsItWhereTheRepoDeclaresNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	stopmodeUserConfig(t, "tdd = \"off\"\n")
	crate := mkProject(t, "Cargo.toml")
	redJobAt(t, crate)
	if got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(crate))); got.Block {
		t.Fatalf("verdict = %+v, want the user's off to hold", got)
	}
	stopmodeDeclare(t, crate, "tdd = \"enforce\"\n")
	if got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(crate))); !got.Block {
		t.Fatalf("verdict = %+v, want the repo's enforce to beat the user's off", got)
	}
}

func TestStopMode_ABadValueFallsBackToTheBuiltInNeverToOff(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate := mkProject(t, "Cargo.toml")
	stopmodeDeclare(t, crate, "tdd = \"loud\"\n")
	redJobAt(t, crate)
	got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(crate)))
	if got.Block || got.Guidance == "" {
		t.Fatalf("verdict = %+v, want the built-in warn for a misspelt value: guidance, no block, and never the silence of off", got)
	}
}

func TestStopMode_TaskCompletedIsNotHeldOpenUnderWarnOrOff(t *testing.T) {
	for _, mode := range []string{"warn", "off"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			repo := makeGoRepo(t)
			stopmodeDeclare(t, repo, "tdd = \""+mode+"\"\n")
			stampProject(t, repo, "red", []string{"TestRetry/backoff"})
			if got := DecideStop(StopHookTaskCompleted, stopPayload(t, "handwritten/taskcompleted.json", taskFields(repo))); got.Block {
				t.Fatalf("verdict = %+v, want the task let through under %s", got, mode)
			}
		})
	}
}

func TestStopMode_OffForTheSessionStillWinsOverEnforce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate := mkProject(t, "Cargo.toml")
	stopmodeDeclare(t, crate, "tdd = \"enforce\"\n")
	redJobAt(t, crate)
	state, path := loadSession(stopSession)
	state.Overrides.Off = true
	if err := state.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(crate))); got.Block {
		t.Fatalf("verdict = %+v, want /tdd off to hold", got)
	}
}

func TestRenderStopVerdict_GuidanceIsAMessageNotADecision(t *testing.T) {
	for _, event := range []StopEvent{StopHookStop, StopHookSubagentStop} {
		stdout, stderr, code := RenderStopVerdict(event, StopVerdict{Guidance: "gate (tdd = warn): a red"})
		if code != 0 || len(stderr) != 0 {
			t.Fatalf("%s: code %d, stderr %q, want a clean exit", event, code, stderr)
		}
		var out map[string]any
		if err := json.Unmarshal(stdout, &out); err != nil {
			t.Fatalf("%s: stdout %q is not JSON: %v", event, stdout, err)
		}
		if out["systemMessage"] != "gate (tdd = warn): a red" {
			t.Fatalf("%s: stdout = %v, want the guidance as systemMessage", event, out)
		}
		if _, blocks := out["decision"]; blocks {
			t.Fatalf("%s: guidance must not carry a decision: %v", event, out)
		}
	}
}

func TestStopMode_TrellisOffSilencesTheStopAndTheEditHookLikeTddOff(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	crate := mkProject(t, "Cargo.toml")
	redJobAt(t, crate)
	t.Setenv("TRELLIS_OFF", "1")
	got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(crate)))
	if got.Block || got.Guidance != "" {
		t.Fatalf("verdict = %+v, want nothing under TRELLIS_OFF", got)
	}
	if _, ok := captureStateSnapshot(stopSession, filepath.Join(crate, "src", "lib.rs"), crate, nil); ok {
		t.Fatal("the edit hook must stay silent under TRELLIS_OFF, as it does under /tdd off")
	}
}

// A lane that pins nothing is checked at Stop under its arm, the same mode its code
// edits are: the experiment is of the tdd key, not of one rule.
func TestStopMode_AnUnpinnedLaneIsCheckedUnderItsArm(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("TRELLIS_CONFIG", t.TempDir())
	_, linked := primaryRepo(t)
	mustWrite(t, filepath.Join(linked, "Cargo.toml"), "[package]\nname = \"c\"\nversion = \"0.1.0\"\n")
	redJobAt(t, linked)
	arm := EffectiveTDD(linked).Arm
	if arm == "" {
		t.Fatalf("setup: the lane has no arm: %+v", EffectiveTDD(linked))
	}
	got := DecideStop(StopHookStop, stopPayload(t, "stop.json", stopFields(linked)))
	if got.Block != (arm == "enforce") {
		t.Errorf("verdict = %+v in the %s arm, want a block exactly in the enforce arm", got, arm)
	}
}
