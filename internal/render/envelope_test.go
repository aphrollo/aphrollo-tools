package render

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPreToolUseDeny_isTheDenyEnvelopeAndNothingElse(t *testing.T) {
	got := string(PreToolUseDeny("trellis deny [primary-write] x <lane> & y"))
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"trellis deny [primary-write] x <lane> & y"}}`
	if got != want {
		t.Errorf("deny envelope\n got: %s\nwant: %s", got, want)
	}
}

func TestPreToolUseAllow_carriesOnlyTheReason(t *testing.T) {
	got := string(PreToolUseAllow("fine"))
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"fine"}}`
	if got != want {
		t.Errorf("allow envelope\n got: %s\nwant: %s", got, want)
	}
}

func TestContext_namesTheHookThatCarriesIt(t *testing.T) {
	for _, h := range []Hook{HookPreToolUse, HookPostToolUse, HookPostToolBatch, HookSubagentStart, HookUserPromptSubmit, HookSessionStart} {
		got := string(Context(h, "one line"))
		want := `{"hookSpecificOutput":{"hookEventName":"` + string(h) + `","additionalContext":"one line"}}`
		if got != want {
			t.Errorf("%s context\n got: %s\nwant: %s", h, got, want)
		}
	}
}

func TestContext_isSilentWhereNothingCanCarryIt(t *testing.T) {
	for _, h := range []Hook{HookStop, HookSubagentStop, HookSessionEnd, HookCwdChanged, "FromTheFuture", ""} {
		if got := Context(h, "x"); got != nil {
			t.Errorf("%q carries no context, but Context returned %s", h, got)
		}
	}
	if got := Context(HookPostToolBatch, ""); got != nil {
		t.Errorf("empty text rendered %s, want silence", got)
	}
}

func TestStopBlock_isATopLevelDecisionWithNoHookSpecificOutput(t *testing.T) {
	got := string(StopBlock("trellis: red internal/lane TestX"))
	want := `{"decision":"block","reason":"trellis: red internal/lane TestX"}`
	if got != want {
		t.Errorf("stop block\n got: %s\nwant: %s", got, want)
	}
	if StopBlock("") != nil {
		t.Error("a block with no reason must be silent: Stop would block with nothing to read")
	}
}

func TestEnvelopes_neverRewriteTheToolCall(t *testing.T) {
	all := [][]byte{PreToolUseDeny("d"), PreToolUseAllow("a"), StopBlock("s")}
	for _, h := range []Hook{HookPreToolUse, HookPostToolUse, HookPostToolBatch, HookSubagentStart, HookUserPromptSubmit, HookSessionStart} {
		all = append(all, Context(h, "c"))
	}
	for _, b := range all {
		if strings.Contains(string(b), "updatedInput") {
			t.Errorf("envelope rewrites the tool call: %s", b)
		}
		var v map[string]any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Errorf("not JSON: %s: %v", b, err)
		}
	}
}

func TestContext_clipsToTheHooksCapAndNamesTheCut(t *testing.T) {
	cases := []struct {
		hook  Hook
		limit int // tokens
	}{
		{HookSubagentStart, 250},
		{HookSessionStart, 400},
		{HookPreToolUse, 60},
	}
	for _, c := range cases {
		b := Context(c.hook, strings.Repeat("brief ", 2000))
		var v struct {
			Out struct {
				Text string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("%s: %v", c.hook, err)
		}
		if got := Tokens(len(v.Out.Text)); got > c.limit {
			t.Errorf("%s context is %d tokens, cap %d", c.hook, got, c.limit)
		}
		if !strings.Contains(v.Out.Text, "cut: text") || !utf8.ValidString(v.Out.Text) {
			t.Errorf("%s clipped without naming the cut: %q", c.hook, v.Out.Text[max(0, len(v.Out.Text)-40):])
		}
	}
}

func TestPreToolUseDeny_clipsToTheDenyCap(t *testing.T) {
	b := PreToolUseDeny(strings.Repeat("é", 1000))
	var v struct {
		Out struct {
			Reason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if got := Tokens(len(v.Out.Reason)); got > CapDeny || !utf8.ValidString(v.Out.Reason) {
		t.Errorf("deny reason is %d tokens (cap %d), valid=%v", got, CapDeny, utf8.ValidString(v.Out.Reason))
	}
}
