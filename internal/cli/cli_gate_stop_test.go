package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// The three turn-end hooks, run the way the harness runs them: the payload on
// stdin, the answer as an exit code and the streams the harness reads.

const stopCLISession = "stop-cli-sess"

// unseenRedProject records a finished red run for a project under a session
// no hook has reported, and returns the project.
func unseenRedProject(t *testing.T) string {
	t.Helper()
	gateConfigDir(t)
	project := t.TempDir()
	target := filepath.Join(project, "lib.rs")
	if err := os.WriteFile(target, []byte("pub fn a() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tdd.RecordFinishedRedDeferredJobForTest(project, target, stopCLISession, "tests::a_breaks")
	return project
}

func stopCLIPayload(t *testing.T, event, cwd string) *strings.Reader {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"session_id": stopCLISession, "cwd": cwd, "hook_event_name": event, "stop_hook_active": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(raw))
}

func TestRun_Gate_Stop_BlocksOnAnUnseenRedWithADecisionOnStdout(t *testing.T) {
	for verb, event := range map[string]string{"stop": "Stop", "subagentstop": "SubagentStop"} {
		t.Run(verb, func(t *testing.T) {
			project := unseenRedProject(t)
			var out, errb bytes.Buffer

			code := Run([]string{"gate", verb}, stopCLIPayload(t, event, project), &out, &errb)

			var got struct{ Decision, Reason string }
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("stdout %q is not a decision: %v", out.String(), err)
			}
			if code != 0 || got.Decision != "block" || !strings.Contains(got.Reason, "tests::a_breaks") {
				t.Fatalf("code=%d decision=%q reason=%q, want exit 0 and a block carrying the red's gate line", code, got.Decision, got.Reason)
			}
		})
	}
}

func TestRun_Gate_TaskCompleted_ExitsTwoWithTheRedOnStderr(t *testing.T) {
	project := unseenRedProject(t)
	var out, errb bytes.Buffer

	code := Run([]string{"gate", "taskcompleted"}, stopCLIPayload(t, "TaskCompleted", project), &out, &errb)

	if code != 2 || !strings.Contains(errb.String(), "tests::a_breaks") || out.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q, want exit 2 with the failing test on stderr", code, out.String(), errb.String())
	}
}

func TestRun_Gate_StopChecks_AreSilentWhenNothingFinishedRed(t *testing.T) {
	gateConfigDir(t)
	for verb, event := range map[string]string{"stop": "Stop", "subagentstop": "SubagentStop", "taskcompleted": "TaskCompleted"} {
		var out, errb bytes.Buffer

		code := Run([]string{"gate", verb}, stopCLIPayload(t, event, t.TempDir()), &out, &errb)

		if code != 0 || out.Len() != 0 || errb.Len() != 0 {
			t.Errorf("%s: code=%d stdout=%q stderr=%q, want silence and exit 0", verb, code, out.String(), errb.String())
		}
	}
}

func TestRun_Gate_StopChecks_FailOpenOnAMalformedPayload(t *testing.T) {
	gateConfigDir(t)
	for _, verb := range []string{"stop", "subagentstop", "taskcompleted"} {
		var out, errb bytes.Buffer

		code := Run([]string{"gate", verb}, strings.NewReader("{not json"), &out, &errb)

		if code != 0 || out.Len() != 0 {
			t.Errorf("%s: code=%d stdout=%q, want an allow", verb, code, out.String())
		}
	}
}

func TestGateVerbs_CarryTheThreeTurnEndHooks(t *testing.T) {
	verbs := GateVerbs()
	for _, want := range []string{"stop", "subagentstop", "taskcompleted"} {
		found := false
		for _, v := range verbs {
			found = found || v == want
		}
		if !found {
			t.Errorf("gate verb table = %v, want it to carry %s", verbs, want)
		}
		if !strings.Contains(gateUsage, "  "+want+" ") {
			t.Errorf("gate usage does not list %s", want)
		}
	}
}
