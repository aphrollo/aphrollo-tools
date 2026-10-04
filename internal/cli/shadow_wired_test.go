package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

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
	if d["rule"] != "primary-write" || d["trellis"] != "block" || d["aphrollo"] != "allow" || d["relation"] != "trellis-stricter" {
		t.Errorf("shadow detail = %v, want primary-write: trellis blocks, aphrollo allows, trellis-stricter", d)
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
