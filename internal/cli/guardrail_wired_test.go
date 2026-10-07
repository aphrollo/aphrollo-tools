package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// recordedBashPayload is the recorded PreToolUse payload of a shell call with
// its command and cwd swapped for the ones under test, as the harness would
// send it to an installed hook.
func recordedBashPayload(t *testing.T, cwd, command string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(recordedHookDir, "pretooluse_bash.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["cwd"] = cwd
	m["tool_input"].(map[string]any)["command"] = command
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func guardrailRepo(t *testing.T) string {
	t.Helper()
	gateConfigDir(t)
	dir := t.TempDir()
	gitInitRepo(t, dir)
	return dir
}

// The installed hook is `aphrollo gate pretooluse`; a long foreground wait has
// to be refused through it, not only through the standalone guardrail verb.
func TestRun_GatePreToolUse_DeniesALongWaitOnTheRecordedBashPayload(t *testing.T) {
	dir := guardrailRepo(t)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "pretooluse"}, strings.NewReader(recordedBashPayload(t, dir, "sleep 30")), &out, &errb)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (denied)\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if reason := denyReason(t, out.Bytes()); !strings.Contains(reason, "Blocking wait of 30s") {
		t.Fatalf("the deny must carry the guardrail's reason, got: %s", reason)
	}
}

// A guardrail warning is advice: the command goes through (exit 0, no deny)
// and the warning text reaches the model.
func TestRun_GatePreToolUse_LetsAGuardrailWarnThroughWithItsText(t *testing.T) {
	dir := guardrailRepo(t)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "pretooluse"}, strings.NewReader(recordedBashPayload(t, dir, "pytest tests")), &out, &errb)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (a warn never denies)\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	var env struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
			AdditionalContext  string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("a warn must print its advice as a hook envelope: %v; stdout: %q", err, out.String())
	}
	if env.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("a warn must not carry a permission decision, got %q", env.HookSpecificOutput.PermissionDecision)
	}
	if !strings.Contains(env.HookSpecificOutput.AdditionalContext, "pytest is verbose by default") {
		t.Fatalf("the warn text must reach the model, got %q", env.HookSpecificOutput.AdditionalContext)
	}
}

func TestRun_GatePreToolUse_StaysSilentOnACleanBashCall(t *testing.T) {
	dir := guardrailRepo(t)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "pretooluse"}, strings.NewReader(recordedBashPayload(t, dir, "ls")), &out, &errb)

	if code != 0 || out.Len() != 0 {
		t.Fatalf("clean call: exit %d, stdout %q, want 0 and no output", code, out.String())
	}
}

func TestRun_GuardrailPreToolUse_StaysAStandaloneVerb(t *testing.T) {
	dir := guardrailRepo(t)

	var out, errb bytes.Buffer
	code := Run([]string{"guardrail", "pretooluse"}, strings.NewReader(recordedBashPayload(t, dir, "sleep 30")), &out, &errb)

	if code != 2 || !strings.Contains(out.String(), "Blocking wait of 30s") {
		t.Fatalf("standalone guardrail: exit %d, stdout %q, want 2 and the wait reason", code, out.String())
	}
}

func TestMergeGuardrail_TheStrongerWinsAndBothReasonsShow(t *testing.T) {
	cases := []struct {
		name       string
		guardrail  tdd.Decision
		gate       tdd.Decision
		wantAction tdd.Decision
		wantIn     []string
	}{
		{"guardrail allows, gate decides", tdd.Decision{}, tdd.Decision{Action: tdd.Block, Reason: "wall"}, tdd.Decision{Action: tdd.Block}, []string{"wall"}},
		{"gate allows, guardrail blocks", tdd.Decision{Action: tdd.Block, Reason: "no sleep"}, tdd.Decision{}, tdd.Decision{Action: tdd.Block}, []string{"no sleep"}},
		{"gate allows, guardrail warns", tdd.Decision{Action: tdd.Warn, Reason: "use -q"}, tdd.Decision{}, tdd.Decision{Action: tdd.Warn}, []string{"use -q"}},
		{"guardrail warn never softens a gate block", tdd.Decision{Action: tdd.Warn, Reason: "use -q"}, tdd.Decision{Action: tdd.Block, Reason: "wall"}, tdd.Decision{Action: tdd.Block}, []string{"wall", "use -q"}},
		{"guardrail block over a gate warn keeps both", tdd.Decision{Action: tdd.Block, Reason: "no sleep"}, tdd.Decision{Action: tdd.Warn, Reason: "note"}, tdd.Decision{Action: tdd.Block}, []string{"no sleep", "note"}},
		{"both warn, both shown", tdd.Decision{Action: tdd.Warn, Reason: "use -q"}, tdd.Decision{Action: tdd.Warn, Reason: "note"}, tdd.Decision{Action: tdd.Warn}, []string{"use -q", "note"}},
		{"both block, both shown", tdd.Decision{Action: tdd.Block, Reason: "no sleep"}, tdd.Decision{Action: tdd.Block, Reason: "wall"}, tdd.Decision{Action: tdd.Block}, []string{"no sleep", "wall"}},
	}
	for _, c := range cases {
		got := mergeGuardrail(c.guardrail, c.gate)
		if got.Action != c.wantAction.Action {
			t.Errorf("%s: action %v, want %v", c.name, got.Action, c.wantAction.Action)
		}
		for _, want := range c.wantIn {
			if !strings.Contains(got.Reason, want) {
				t.Errorf("%s: reason %q lacks %q", c.name, got.Reason, want)
			}
		}
	}
}

var settingsHookCmd = regexp.MustCompile(`"\$x"\s+(gate [a-z]+)`)

// Every PreToolUse hook the installer writes for a shell call must be a verb
// that evaluates the guardrail rules: feed each one a command a rule refuses
// and require the deny. A rule shipped behind a verb no hook calls (#1163)
// fails here instead of being found by a hung session.
func TestInstalledPreToolUseHooks_EvaluateTheGuardrailRules(t *testing.T) {
	cfg := gateConfigDir(t)
	dir := t.TempDir()
	gitInitRepo(t, dir)
	if _, err := tdd.InitSettings(cfg, filepath.Join(t.TempDir(), "aphrollo"), false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cfg, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}

	checked := 0
	for _, group := range settings.Hooks["PreToolUse"] {
		if !regexp.MustCompile("^(" + group.Matcher + ")$").MatchString("Bash") {
			continue
		}
		for _, h := range group.Hooks {
			m := settingsHookCmd.FindStringSubmatch(h.Command)
			if m == nil {
				t.Fatalf("hook command %q does not run the resolved binary with a gate verb", h.Command)
			}
			var out, errb bytes.Buffer
			code := Run(strings.Fields(m[1]), strings.NewReader(recordedBashPayload(t, dir, "sleep 30")), &out, &errb)
			if code != 2 {
				t.Errorf("installed PreToolUse hook %q lets `sleep 30` through (exit %d, stdout %q): it does not evaluate the guardrail rules", h.Command, code, out.String())
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatalf("no installed PreToolUse hook matches Bash; settings:\n%s", data)
	}
}

// A call the harness runs in the background blocks nothing, so the foreground
// wait rule must not refuse it through the installed hook (#1170).
func TestRun_GatePreToolUse_LetsABackgroundWaitThroughAndStillDeniesAForegroundOne(t *testing.T) {
	dir := guardrailRepo(t)
	command := "sleep 45; aphrollo workspace merge 1169 --wait"
	for _, c := range []struct {
		name       string
		background string
		wantCode   int
	}{
		{"background true", `"run_in_background":true,`, 0},
		{"background false", `"run_in_background":false,`, 2},
	} {
		payload := strings.Replace(recordedBashPayload(t, dir, command), `"command"`, c.background+`"command"`, 1)

		var out, errb bytes.Buffer
		code := Run([]string{"gate", "pretooluse"}, strings.NewReader(payload), &out, &errb)

		if code != c.wantCode {
			t.Errorf("%s: exit %d, want %d\nstdout:%s\nstderr:%s", c.name, code, c.wantCode, out.String(), errb.String())
		}
	}
}
