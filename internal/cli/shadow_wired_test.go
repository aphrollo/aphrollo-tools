package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// shadowEvents are the shadow records the repo's event log holds.
func shadowEvents(root string) []tdd.Event {
	var out []tdd.Event
	for _, e := range tdd.ReadEvents(root) {
		if e.Kind == "shadow" {
			out = append(out, e)
		}
	}
	return out
}

func bashPayloadIn(t *testing.T, cwd, cmd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name": "Bash", "session_id": "cli-shadow", "cwd": cwd, "tool_input": map[string]any{"command": cmd},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRun_PreToolUse_RecordsAShadowEventBesideADiscardDeny(t *testing.T) {
	gateConfigDir(t)
	dir := gitInit(t, map[string]string{"aphrollo.toml": "[aphrollo]\n"})

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "pretooluse"}, strings.NewReader(bashPayloadIn(t, dir, "git checkout -- f")), &out, &errb); code != 2 {
		t.Fatalf("exit code = %d, want 2: the shadow record must not change the live decision\nstdout:%s", code, out.String())
	}
	got := shadowEvents(dir)
	if len(got) != 1 {
		t.Fatalf("%d shadow events, want 1: %+v", len(got), got)
	}
	d := got[0].Detail
	if d["rule"] != "discard-work" || d["live_rule"] != "discard-bash" || d["trellis"] != "block" || d["aphrollo"] != "block" || d["relation"] != "agree" {
		t.Errorf("shadow detail = %v, want discard-work blocked by both and agreeing", d)
	}
	if got[0].Cmd != "" || strings.Contains(got[0].Verdict+got[0].Stage, "checkout") {
		t.Errorf("a shadow event must carry no command text: %+v", got[0])
	}
}

func TestRun_PreToolUse_ShadowsAWaivedPrimaryWriteAsAWouldBeBlock(t *testing.T) {
	gateConfigDir(t)
	primary, _ := primaryWorktreeRepo(t)
	t.Setenv("APHROLLO_PRIMARY_EDITS", "1")

	var out, errb bytes.Buffer
	stdin := strings.NewReader(primaryEditPayload(t, filepath.Join(primary, "main.go")))
	if code := Run([]string{"gate", "pretooluse"}, stdin, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0: the waiver lets the write through\nstdout:%s", code, out.String())
	}
	got := shadowEvents(primary)
	if len(got) != 1 {
		t.Fatalf("%d shadow events, want 1: %+v", len(got), got)
	}
	d := got[0].Detail
	if d["rule"] != "primary-write" || d["trellis"] != "block" || d["aphrollo"] != "warn" || d["relation"] != "trellis-stricter" {
		t.Errorf("shadow detail = %v, want primary-write: trellis blocks, aphrollo only warned (the worktree advisory is the call's final answer), trellis-stricter", d)
	}
}

func TestRun_PreToolUse_RecordsNothingForACallNoShadowedRuleReads(t *testing.T) {
	gateConfigDir(t)
	dir := gitInit(t, map[string]string{"aphrollo.toml": "[aphrollo]\n"})

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "pretooluse"}, strings.NewReader(bashPayloadIn(t, dir, "echo hi")), &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:%s", code, out.String())
	}
	if got := shadowEvents(dir); len(got) != 0 {
		t.Errorf("a clean call recorded %d shadow events: %+v", len(got), got)
	}
}

// A shell write is filed under the lane of the checkout it writes into, not the
// lane of the cwd the command ran from: a lane's shell that writes into the
// primary checkout wrote into main.
func TestRun_PreToolUse_ShadowsAShellWriteUnderTheLaneOfItsTarget(t *testing.T) {
	gateConfigDir(t)
	primary, linked := primaryWorktreeRepo(t)
	cmd := "echo hi > " + filepath.ToSlash(filepath.Join(primary, "notes.txt"))

	for _, waived := range []bool{false, true} {
		if waived {
			t.Setenv("APHROLLO_PRIMARY_EDITS", "1")
		}
		var out, errb bytes.Buffer
		Run([]string{"gate", "pretooluse"}, strings.NewReader(bashPayloadIn(t, linked, cmd)), &out, &errb)
	}
	got := shadowEvents(primary)
	if len(got) != 2 {
		t.Fatalf("%d shadow events, want 2 (blocked, then waived): %+v", len(got), got)
	}
	for i, want := range []struct{ aphrollo, relation string }{{"block", "agree"}, {"allow", "trellis-stricter"}} {
		e := got[i]
		if e.Lane != "main" || filepath.Clean(e.Detail["wt"]) != filepath.Clean(primary) || e.Detail["primary"] != "true" {
			t.Errorf("event %d: lane %q wt %q primary %q, want main, the primary root %q, primary", i, e.Lane, e.Detail["wt"], e.Detail["primary"], primary)
		}
		if e.Detail["aphrollo"] != want.aphrollo || e.Detail["relation"] != want.relation {
			t.Errorf("event %d: aphrollo %q relation %q, want %q %q", i, e.Detail["aphrollo"], e.Detail["relation"], want.aphrollo, want.relation)
		}
	}
}

// Each finding of the law engine is its own fact: a deny finding is the
// deny-law-edit rule and a warn finding the warn-law rule, each naming its law.
func TestPreShadow_LawFindingsAreOneFactEachByTheirOwnSeverity(t *testing.T) {
	p := newPreShadow([]byte(`{"tool_name":"Write"}`))
	p.law([]tdd.LawFinding{{Law: "module_size", Deny: true}, {Law: "doc_path", Deny: false}, {Law: "nan-guard", Deny: true}})
	if len(p.facts) != 3 {
		t.Fatalf("%d facts for 3 findings, want 3: %+v", len(p.facts), p.facts)
	}
	for i, want := range []struct {
		rule, live string
		actual     shadow.Action
	}{
		{"deny-law-edit", "ratchet:module_size", shadow.Block},
		{"warn-law", "ratchet:doc_path", shadow.Warn},
		{"deny-law-edit", "ratchet:nan-guard", shadow.Block},
	} {
		if f := p.facts[i]; f.Rule != want.rule || f.LiveRule != want.live || f.Actual != want.actual {
			t.Errorf("fact %d = %s/%s/%s, want %s/%s/%s", i, f.Rule, f.LiveRule, f.Actual, want.rule, want.live, want.actual)
		}
	}
}

func TestHookTimingEvent_LeavesTheShadowWaitOutOfTheSeconds(t *testing.T) {
	raw := []byte(`{"session_id":"s","agent_id":"a","cwd":"/r"}`)
	e := hookTimingEvent("pretooluse", raw, 3*time.Second, time.Second)
	if e.Secs != 2 || e.Detail["shadow_ms"] != "1000" || e.Detail["hook"] != "pretooluse" || e.Actor != "s/a" {
		t.Errorf("event = %+v, want 2 s of the hook's own, shadow_ms 1000", e)
	}
	if e := hookTimingEvent("stop", raw, 3*time.Second, 0); e.Secs != 3 || e.Detail["shadow_ms"] != "" {
		t.Errorf("no wait: %+v, want 3 s and no shadow_ms", e)
	}
	if e := hookTimingEvent("stop", raw, time.Second, 2*time.Second); e.Secs != 0 {
		t.Errorf("a wait longer than the hook's measured time left %v s, want 0", e.Secs)
	}
}
