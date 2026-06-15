package tdd

import (
	"encoding/json"
	"fmt"
	"strings"
)

// preToolUseInput is the subset of the Claude Code PreToolUse payload the
// edit-time gate needs. The gated tools are Edit / Write / MultiEdit /
// NotebookEdit; the new content can arrive as new_string, content, or a
// MultiEdit edits[] array, so all three are gathered.
type preToolUseInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		NewString    string `json:"new_string"`
		Content      string `json:"content"`
		Edits        []struct {
			NewString string `json:"new_string"`
		} `json:"edits"`
	} `json:"tool_input"`
}

var gatedEditTools = map[string]bool{
	"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true,
}

// DecidePreEdit parses a PreToolUse payload and evaluates the edit-time smell
// gate. Non-edit tools, edits with no path, and edits that touch no test file
// all Allow — the smells only gate test files, because a smell in a test is
// what makes the oracle untrustworthy.
func DecidePreEdit(raw []byte) (Decision, error) {
	var in preToolUseInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return Decision{}, fmt.Errorf("parse PreToolUse input: %w", err)
	}
	if !gatedEditTools[in.ToolName] {
		return Decision{Action: Allow}, nil
	}

	gatesTest := false
	for _, p := range []string{in.ToolInput.FilePath, in.ToolInput.NotebookPath} {
		if p != "" && ClassifyFile(p) == Test {
			gatesTest = true
		}
	}
	if !gatesTest {
		return Decision{Action: Allow}, nil
	}
	return smellCheck(newContent(in)), nil
}

// newContent concatenates every piece of new text an edit introduces, so the
// smell detectors see the whole proposed addition regardless of which tool
// shape delivered it.
func newContent(in preToolUseInput) string {
	var parts []string
	if in.ToolInput.NewString != "" {
		parts = append(parts, in.ToolInput.NewString)
	}
	if in.ToolInput.Content != "" {
		parts = append(parts, in.ToolInput.Content)
	}
	for _, e := range in.ToolInput.Edits {
		if e.NewString != "" {
			parts = append(parts, e.NewString)
		}
	}
	return strings.Join(parts, "\n")
}

// preToolUseOutput mirrors the Claude Code PreToolUse hook output contract.
type preToolUseOutput struct {
	Decision           string `json:"decision,omitempty"`
	Reason             string `json:"reason,omitempty"`
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision,omitempty"`
		PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
		AdditionalContext        string `json:"additionalContext,omitempty"`
	} `json:"hookSpecificOutput"`
}

// RenderPreToolUse turns a Decision into the hook's stdout payload and exit
// code: Block -> exit 2 with a deny envelope; Warn -> exit 0 with advisory
// context; Allow -> exit 0 and no output.
func RenderPreToolUse(d Decision) ([]byte, int) {
	const event = "PreToolUse"
	out := preToolUseOutput{}
	out.HookSpecificOutput.HookEventName = event
	switch d.Action {
	case Block:
		reason := "tdd: " + d.Reason
		out.Decision = "block"
		out.Reason = reason
		out.HookSpecificOutput.PermissionDecision = "deny"
		out.HookSpecificOutput.PermissionDecisionReason = reason
		b, _ := json.Marshal(out)
		return b, 2
	case Warn:
		out.HookSpecificOutput.AdditionalContext = "tdd: " + d.Reason
		b, _ := json.Marshal(out)
		return b, 0
	default:
		return nil, 0
	}
}
