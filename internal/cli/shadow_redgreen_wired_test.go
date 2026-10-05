package cli

import (
	"bytes"
	"encoding/json"
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

// goRepo is a lane's worktree of a repo (its primary checkout on main), holding a Go
// module with one package: the shadow follows lanes, not the primary checkout.
func goRepo(t *testing.T) string {
	t.Helper()
	_, linked := primaryWorktreeRepo(t)
	writeFile(t, filepath.Join(linked, "aphrollo.toml"), "[aphrollo]\n")
	writeFile(t, filepath.Join(linked, "go.mod"), "module example.com/m\n\ngo 1.22\n")
	writeFile(t, filepath.Join(linked, "pkg", "p.go"), "package pkg\n\nvar a = 1\n")
	return linked
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
	dir := goRepo(t)
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
	writeFile(t, filepath.Join(dir, ".ratchet", "baselines", "no-forbidden.txt"), "")
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

// ratchet: test_removed TestRun_Stop_ShadowsABlockOnAnUnseenRedAsStopRed: replaced by TestRun_Stop_ShadowsEveryStopWithTheLiveUnseenRedFact, as every stop is recorded now
func stopPayloadIn(t *testing.T, dir string) string { return stopPayloadAs(t, stopCLISession, dir) }

func stopPayloadAs(t *testing.T, session, dir string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"session_id": session, "cwd": dir, "hook_event_name": "Stop", "stop_hook_active": false})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Every Stop is asked of the kernel, with aphrollo's own unseen-red fact: a stop with
// no red is an agreeing allow, and a stop that blocks on a red no run of the lane
// record was folded for is unjudged, never softer.
func TestRun_Stop_ShadowsEveryStopWithTheLiveUnseenRedFact(t *testing.T) {
	gateConfigDir(t)
	dir := goRepo(t)

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "stop"}, strings.NewReader(stopPayloadIn(t, dir)), &out, &errb); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	got := shadowOfRule(dir, "stop-red")
	if len(got) != 1 || got[0].Detail["relation"] != "agree" || got[0].Detail["trellis"] != "allow" || got[0].Detail["aphrollo"] != "allow" || got[0].Detail["hook"] != "stop" {
		t.Fatalf("a stop with no red: %+v, want one agreeing allow", got)
	}

	target := filepath.Join(dir, "pkg", "p.go")
	tdd.RecordFinishedRedDeferredJobForTest(dir, target, stopCLISession, "TestA")
	out.Reset()
	Run([]string{"gate", "stop"}, strings.NewReader(stopPayloadIn(t, dir)), &out, &errb)
	if !strings.Contains(out.String(), `"decision":"block"`) {
		t.Fatalf("stop did not block on the unseen red: %q", out.String())
	}
	got = shadowOfRule(dir, "stop-red")
	if len(got) != 2 || got[1].Detail["relation"] != "unjudged" || got[1].Detail["cause"] != "unfolded" {
		t.Errorf("a block on an unfolded red: %+v, want an unjudged record naming unfolded", got)
	}
}

// The record never reaches the answer of a Stop either.
func TestRun_Stop_AnswersTheSameBytesWithShadowOnAndOff(t *testing.T) {
	gateConfigDir(t)
	dir := goRepo(t)
	target := filepath.Join(dir, "pkg", "p.go")
	answer := func(on bool) (int, string, string) {
		old := shadow.Enabled
		shadow.Enabled = on
		t.Cleanup(func() { shadow.Enabled = old })
		// One session each: the second answer of a session reads the first's state.
		session := map[bool]string{true: "stop-on", false: "stop-off"}[on]
		tdd.RecordFinishedRedDeferredJobForTest(dir, target, session, "TestA")
		var out, errb bytes.Buffer
		code := Run([]string{"gate", "stop"}, strings.NewReader(stopPayloadAs(t, session, dir)), &out, &errb)
		return code, out.String(), errb.String()
	}
	codeOn, outOn, errOn := answer(true)
	codeOff, outOff, errOff := answer(false)
	if codeOn != codeOff || outOn != outOff || errOn != errOff {
		t.Errorf("shadow on (%d %q %q) and off (%d %q %q) answered differently", codeOn, outOn, errOn, codeOff, outOff, errOff)
	}
	if !strings.Contains(outOn, `"decision":"block"`) {
		t.Errorf("the probe stop was not a block: %q", outOn)
	}
	if got := len(shadowOfRule(dir, "stop-red")); got != 1 {
		t.Errorf("%d stop-red records over an on run and an off run, want 1 (the on run's)", got)
	}
}

// Trunk is not followed: an edit and a stop on a repo's main branch record nothing.
func TestRun_FollowsNoTrunkLane(t *testing.T) {
	gateConfigDir(t)
	dir := gitInit(t, map[string]string{
		"aphrollo.toml": "[aphrollo]\n",
		"go.mod":        "module example.com/m\n\ngo 1.22\n",
		"pkg/p.go":      "package pkg\n\nvar a = 1\n",
	})
	var out, errb bytes.Buffer
	Run([]string{"gate", "pretooluse"}, strings.NewReader(goEditPayload(t, dir, "pkg/p.go")), &out, &errb)
	Run([]string{"gate", "stop"}, strings.NewReader(stopPayloadIn(t, dir)), &out, &errb)
	if got := append(shadowOfRule(dir, "red-green"), shadowOfRule(dir, "stop-red")...); len(got) != 0 {
		t.Errorf("trunk records: %+v, want none", got)
	}
}
