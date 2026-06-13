package guardrail

import (
	"encoding/json"
	"fmt"
)

// hookInput is the subset of the Claude Code PreToolUse hook payload we need.
type hookInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// DecideFromHookInput parses a PreToolUse hook payload and evaluates the policy.
func DecideFromHookInput(raw []byte) (Decision, error) {
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return Decision{}, fmt.Errorf("parse hook input: %w", err)
	}
	return Evaluate(in.ToolName, in.ToolInput.Command), nil
}

// hookSpecificOutput mirrors the Claude Code PreToolUse hook output contract.
type hookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

type hookOutput struct {
	Decision           string             `json:"decision,omitempty"`
	Reason             string             `json:"reason,omitempty"`
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

// Render turns a Decision into the hook's stdout payload and process exit code:
// Block -> exit 2 with a deny envelope; Warn -> exit 0 with advisory context;
// Allow -> exit 0 and no output.
func Render(d Decision) ([]byte, int) {
	const event = "PreToolUse"
	switch d.Action {
	case Block:
		reason := "aphrollo guardrail: " + d.Reason
		out := hookOutput{
			Decision: "block",
			Reason:   reason,
			HookSpecificOutput: hookSpecificOutput{
				HookEventName:            event,
				PermissionDecision:       "deny",
				PermissionDecisionReason: reason,
			},
		}
		b, _ := json.Marshal(out)
		return b, 2
	case Warn:
		out := hookOutput{
			HookSpecificOutput: hookSpecificOutput{
				HookEventName:     event,
				AdditionalContext: "aphrollo guardrail: " + d.Reason,
			},
		}
		b, _ := json.Marshal(out)
		return b, 0
	default:
		return nil, 0
	}
}
