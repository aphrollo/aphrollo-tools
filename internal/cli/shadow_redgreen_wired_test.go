package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// goEditPayload is an Edit of a Go source file of dir.
func goEditPayload(t *testing.T, dir, rel string) string {
	return goEditPayloadAs(t, "cli-redgreen", dir, rel)
}

func goEditPayloadAs(t *testing.T, session, dir, rel string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name": "Edit", "session_id": session, "cwd": dir, "hook_event_name": "PreToolUse",
		"tool_input": map[string]any{"file_path": filepath.Join(dir, filepath.FromSlash(rel)), "old_string": "a", "new_string": "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func shadowOfRule(root, rule string) []tdd.Event {
	var out []tdd.Event
	for _, e := range shadowEvents(root) {
		if e.Detail["rule"] == rule {
			out = append(out, e)
		}
	}
	return out
}

func goRepo(t *testing.T) string {
	t.Helper()
	return gitInit(t, map[string]string{
		"aphrollo.toml": "[aphrollo]\n",
		"go.mod":        "module example.com/m\n\ngo 1.22\n",
		"pkg/p.go":      "package pkg\n\nvar a = 1\n",
	})
}

// A code edit is a red-green fact: aphrollo allows it (the proof is held at the
// commit) and the kernel, with no red open in the unit, would not, so it is
// recorded as a would-be block of the unit's package.
func TestRun_PreToolUse_ShadowsACodeEditAsARedGreenWouldBeBlock(t *testing.T) {
	gateConfigDir(t)
	dir := goRepo(t)

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "pretooluse"}, strings.NewReader(goEditPayload(t, dir, "pkg/p.go")), &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0: a code edit is allowed\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	got := shadowOfRule(dir, "red-green")
	if len(got) != 1 {
		t.Fatalf("%d red-green shadow events, want 1: %+v", len(got), shadowEvents(dir))
	}
	d := got[0].Detail
	if d["aphrollo"] != "allow" || d["relation"] != "trellis-stricter" || d["unit"] != "pkg" || d["hook"] != "pretooluse" || d["live_rule"] != "commit-proof" {
		t.Errorf("shadow detail = %v, want an allow beside a would-be block of unit pkg", d)
	}
	if d["trellis"] != "block" && d["trellis"] != "warn" {
		t.Errorf("trellis = %q, want a block (or the holdout arm's warn)", d["trellis"])
	}
	if got[0].Cmd != "" {
		t.Errorf("a shadow event carries no command: %+v", got[0])
	}
}

// A test file, a document and a shell command that writes nothing ask the rule
// nothing, and start nothing.
func TestRun_PreToolUse_ShadowsNoRedGreenForATestAFileOfDocsOrAReadOnlyCommand(t *testing.T) {
	gateConfigDir(t)
	dir := goRepo(t)
	for _, payload := range []string{
		goEditPayload(t, dir, "pkg/p_test.go"),
		goEditPayload(t, dir, "README.md"),
		bashPayloadIn(t, dir, "go vet ./..."),
	} {
		var out, errb bytes.Buffer
		Run([]string{"gate", "pretooluse"}, strings.NewReader(payload), &out, &errb)
	}
	if got := shadowOfRule(dir, "red-green"); len(got) != 0 {
		t.Errorf("%d red-green events for calls that write no code: %+v", len(got), got)
	}
}

// A Bash command that writes a code file asks the rule of that file's unit.
func TestRun_PreToolUse_ShadowsAShellWriteOfACodeFileAsRedGreen(t *testing.T) {
	gateConfigDir(t)
	dir := goRepo(t)
	var out, errb bytes.Buffer
	Run([]string{"gate", "pretooluse"}, strings.NewReader(bashPayloadIn(t, dir, "printf x > pkg/p.go")), &out, &errb)
	got := shadowOfRule(dir, "red-green")
	if len(got) != 1 || got[0].Detail["unit"] != "pkg" {
		t.Errorf("red-green events = %+v, want one for unit pkg", got)
	}
}

// The record never reaches the hook's answer: stdout, stderr and the exit code are the
// same with shadow recording on and off, for an edit that records red-green.
func TestRun_PreToolUse_AnswersTheSameBytesForACodeEditWithShadowOnAndOff(t *testing.T) {
	gateConfigDir(t)
	dir := goRepo(t)
	answer := func(on bool) (int, string, string) {
		old := shadow.Enabled
		shadow.Enabled = on
		t.Cleanup(func() { shadow.Enabled = old })
		// One session each, for the gate's once-per-session advisory is in the first
		// answer of a session only.
		payload := goEditPayloadAs(t, map[bool]string{true: "on", false: "off"}[on], dir, "pkg/p.go")
		var out, errb bytes.Buffer
		code := Run([]string{"gate", "pretooluse"}, strings.NewReader(payload), &out, &errb)
		return code, out.String(), errb.String()
	}
	codeOn, outOn, errOn := answer(true)
	codeOff, outOff, errOff := answer(false)
	if codeOn != codeOff || outOn != outOff || errOn != errOff {
		t.Errorf("shadow on (%d %q %q) and off (%d %q %q) answered differently", codeOn, outOn, errOn, codeOff, outOff, errOff)
	}
	if got := len(shadowOfRule(dir, "red-green")); got != 1 {
		t.Errorf("%d red-green records over an on run and an off run, want 1 (the on run's)", got)
	}
}

// A call a deny law stopped is not asked: the edit it was about does not happen.
func TestRun_PreToolUse_ShadowsNoRedGreenForACallTheGateBlocked(t *testing.T) {
	gateConfigDir(t)
	dir := denyLawRepo(t)
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n\ngo 1.22\n")
	// The repo's one law, widened over Go files, blocks the write below.
	writeFile(t, filepath.Join(dir, ".ratchet", "laws", "no-forbidden.toml"), `
name = "no-forbidden"
description = "FORBIDDEN is not allowed"
severity = "deny"
escape = "// forbidden-ok:"
baseline = ".ratchet/baselines/no-forbidden.txt"

[scope]
include = ["**/*.go"]

[matcher]
kind = "regex-absent"
pattern = "FORBIDDEN"
`)
	b, err := json.Marshal(map[string]any{
		"tool_name": "Write", "session_id": "cli-law", "cwd": dir,
		"tool_input": map[string]any{"file_path": filepath.Join(dir, "notes.go"), "content": "package m\n// FORBIDDEN\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "pretooluse"}, bytes.NewReader(b), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2: the deny law blocks the write\nstdout:%s", code, out.String())
	}
	if got := shadowOfRule(dir, "red-green"); len(got) != 0 {
		t.Errorf("red-green recorded for a write the gate did not let through: %+v", got)
	}
}

// A Stop that blocks on an unseen red is recorded beside the kernel's stop-red
// answer; with no open red in the lane's record the kernel would not block, and the
// two sides read differently. The answer is the same bytes with recording off.
func TestRun_Stop_ShadowsABlockOnAnUnseenRedAsStopRed(t *testing.T) {
	gateConfigDir(t)
	dir := goRepo(t)
	target := filepath.Join(dir, "pkg", "p.go")
	if err := os.WriteFile(target, []byte("package pkg\n\nvar a = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tdd.RecordFinishedRedDeferredJobForTest(dir, target, stopCLISession, "TestA")
	payload, err := json.Marshal(map[string]any{"session_id": stopCLISession, "cwd": dir, "hook_event_name": "Stop", "stop_hook_active": false})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "stop"}, bytes.NewReader(payload), &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0 with a block decision on stdout", code)
	}
	if !strings.Contains(out.String(), `"decision":"block"`) {
		t.Fatalf("stop did not block on the unseen red: %q", out.String())
	}
	got := shadowOfRule(dir, "stop-red")
	if len(got) != 1 {
		t.Fatalf("%d stop-red events, want 1: %+v", len(got), shadowEvents(dir))
	}
	if d := got[0].Detail; d["hook"] != "stop" || d["aphrollo"] != "block" || d["trellis"] != "allow" || d["relation"] != "trellis-softer" {
		t.Errorf("stop-red detail = %v, want aphrollo's block beside a kernel that holds no open red (softer)", d)
	}
}
