package tdd

import (
	"strings"
)

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
		OldString    string `json:"old_string"`
		ReplaceAll   bool   `json:"replace_all"`
		Content      string `json:"content"`
		Edits        []struct {
			NewString  string `json:"new_string"`
			OldString  string `json:"old_string"`
			ReplaceAll bool   `json:"replace_all"`
		} `json:"edits"`
	} `json:"tool_input"`
}
