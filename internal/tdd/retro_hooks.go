package tdd

import (
	"encoding/json"
	"strings"
)

// WithPendingRetro appends, after a PostToolUse hook's own text, the retro a
// merge in this session left pending, and marks it delivered: the Bash call
// that ran `workspace merge` is the first hook to see it.
func WithPendingRetro(raw []byte, text string) string {
	var in struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return text
	}
	return joinRetro(text, TakeSessionRetros(in.SessionID))
}

// joinRetro puts a retro after a hook's text, a blank line between them.
func joinRetro(text, retro string) string {
	switch {
	case retro == "":
		return text
	case text == "":
		return retro
	}
	return strings.TrimRight(text, "\n") + "\n\n" + retro
}
