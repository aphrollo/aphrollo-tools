package render

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Hook is a Claude Code hook event, by the name the harness gives it (§4).
type Hook string

const (
	HookSessionStart     Hook = "SessionStart"
	HookUserPromptSubmit Hook = "UserPromptSubmit"
	HookPreToolUse       Hook = "PreToolUse"
	HookPostToolUse      Hook = "PostToolUse"
	HookPostToolBatch    Hook = "PostToolBatch"
	HookSubagentStart    Hook = "SubagentStart"
	HookSubagentStop     Hook = "SubagentStop"
	HookStop             Hook = "Stop"
	HookSessionEnd       Hook = "SessionEnd"
	HookTaskCompleted    Hook = "TaskCompleted"
	HookCwdChanged       Hook = "CwdChanged"
	HookDirectoryAdded   Hook = "DirectoryAdded"
	HookSetup            Hook = "Setup"
)

// specific is the hookSpecificOutput of the hook contract. There is no
// updatedInput field: nothing here rewrites a tool call (§4).
type specific struct {
	HookEventName            Hook   `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

func marshal(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return nil
	}
	return bytes.TrimRight(b.Bytes(), "\n")
}

// fitText clips free text to limit bytes, naming the cut the way a line does.
func fitText(text string, limit int) string {
	out, _ := compose(limit, flex("text", "", strings.ToValidUTF8(text, "�"), ""))
	return out
}

// PreToolUseDeny is the PreToolUse envelope of a deny: permissionDecision
// "deny" and the reason the agent reads, clipped to the deny cap. It is the
// exit-0 JSON form; no top-level decision rides with it.
func PreToolUseDeny(reason string) []byte {
	return marshal(struct {
		Out specific `json:"hookSpecificOutput"`
	}{specific{HookEventName: HookPreToolUse, PermissionDecision: "deny", PermissionDecisionReason: fitText(reason, CapDeny*4)}})
}

// PreToolUseAllow is the PreToolUse envelope of an explicit allow. The harness
// skips its own permission prompt for it, so the decision path never uses it:
// a guide goes out as Context, which decides nothing.
func PreToolUseAllow(reason string) []byte {
	return marshal(struct {
		Out specific `json:"hookSpecificOutput"`
	}{specific{HookEventName: HookPreToolUse, PermissionDecision: "allow", PermissionDecisionReason: fitText(reason, CapGuide*4)}})
}

// contextBytes is the cap on the additionalContext a hook carries; "" means
// the hook carries none. SubagentStart reaches the subagent only; Setup,
// CwdChanged and DirectoryAdded reach nobody (§4, §12 F3 results).
func contextBytes(h Hook) (int, bool) {
	switch h {
	case HookPreToolUse:
		return CapGuide * 4, true
	case HookSubagentStart:
		return CapSubagentBrief * 4, true
	case HookSessionStart:
		return CapBrief * 4, true
	case HookPostToolUse, HookPostToolBatch, HookUserPromptSubmit:
		return PlatformContextBytes, true
	}
	return 0, false
}

// Context is the additionalContext envelope of a hook that carries one, text
// clipped to the hook's cap. A hook that carries none, and empty text, give
// nil: silence.
func Context(h Hook, text string) []byte {
	limit, ok := contextBytes(h)
	if !ok || text == "" {
		return nil
	}
	return marshal(struct {
		Out specific `json:"hookSpecificOutput"`
	}{specific{HookEventName: h, AdditionalContext: fitText(text, limit)}})
}

// StopBlock is the Stop and SubagentStop decision: a top-level block with the
// red as its reason, clipped to the red cap. These hooks take no
// hookSpecificOutput. An empty reason is silence, never a block with nothing
// to read.
func StopBlock(reason string) []byte {
	if reason == "" {
		return nil
	}
	return marshal(struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}{"block", fitText(reason, CapRed*4)})
}
