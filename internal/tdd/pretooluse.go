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

// DecidePreEdit parses a PreToolUse payload and evaluates the edit-time gate.
// The policy set depends on the file kind: a test edit runs the oracle smells
// AND the suppressions; a source edit runs the suppressions only (oracle smells
// have no meaning in source). Non-edit tools, edits with no recognised code
// path, and ignored files all Allow. Smells block; suppressions warn at edit
// (the commit gate is where a new suppression actually stops a change).
func DecidePreEdit(raw []byte) (Decision, error) {
	var in preToolUseInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return Decision{}, fmt.Errorf("parse PreToolUse input: %w", err)
	}
	if !gatedEditTools[in.ToolName] {
		return Decision{Action: Allow}, nil
	}

	kind, path := editTarget(in)
	var d Decision
	switch kind {
	case Test:
		d = evaluate(newContent(in), testPolicies, editPhase, langOf(path))
	case Source:
		d = evaluateSource(newContent(in), path, editPhase)
	default:
		return Decision{Action: Allow}, nil
	}
	return withQualityNotes(d, path, newContent(in)), nil
}

// withQualityNotes attaches the advisory test-quality notes to a decision.
// They never raise a Block (a judgement call must not wedge a session) and
// never mask one: a real oracle smell keeps its own verdict and reason.
func withQualityNotes(d Decision, path, content string) Decision {
	if d.Action == Block {
		return d
	}
	notes := TestQualityNotes(path, content)
	if len(notes) == 0 {
		return d
	}
	joined := strings.Join(notes, "; ")
	if d.Action == Warn && d.Reason != "" {
		return Decision{Action: Warn, Reason: d.Reason + "; " + joined}
	}
	return Decision{Action: Warn, Reason: joined}
}

// evaluateSource gates a Source-file edit. For most languages a source edit runs
// the suppressions only (oracle smells have no meaning outside test code). Zig is
// the exception: its tests live as inline `test "..." {}` blocks in ordinary
// src/*.zig files, so the oracle smells must reach those blocks WITHOUT gating
// the surrounding production code — a real std.time.sleep in a production
// function must still flow. So for a .zig file the smells are evaluated only over
// the inline-test line ranges (extracted block-scoped), while suppressions still
// run over the whole edit; the most severe Decision wins.
func evaluateSource(content, path string, p phase) Decision {
	l := langOf(path)
	if !isZigPath(path) {
		return evaluate(content, sourcePolicies, p, l)
	}
	full := newView(content, l)
	best := evaluateView(full, sourcePolicies, p)
	if best.Action == Block {
		return best // a suppression already blocks; nothing outranks Block
	}
	testLines := zigTestLines(full.code)
	if len(testLines) == 0 {
		return best // no inline test in this edit — production code only
	}
	scoped := view{
		code:       keepLines(full.code, testLines),
		directives: keepLines(full.directives, testLines),
	}
	if d := evaluateView(scoped, oracleSmells, p); d.Action > best.Action {
		best = d
	}
	return best
}

// isZigPath reports whether path is a Zig source file, for which the Source gate
// must reach inline test blocks.
func isZigPath(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".zig")
}

// editTarget classifies the file an edit targets and returns its path, taking
// the strongest role among the candidate paths (Test outranks Source outranks
// Ignore) so an edit naming both a notebook and a file path is gated — and its
// language resolved — by the more meaningful one.
func editTarget(in preToolUseInput) (Kind, string) {
	kind, path := Ignore, ""
	for _, p := range []string{in.ToolInput.FilePath, in.ToolInput.NotebookPath} {
		if p == "" {
			continue
		}
		if k := ClassifyFile(p); k > kind {
			kind, path = k, p
		}
	}
	return kind, path
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
		reason := hookPrefix(d.Reason)
		out.Decision = "block"
		out.Reason = reason
		out.HookSpecificOutput.PermissionDecision = "deny"
		out.HookSpecificOutput.PermissionDecisionReason = reason
		b, _ := json.Marshal(out)
		return b, 2
	case Warn:
		out.HookSpecificOutput.AdditionalContext = hookPrefix(d.Reason)
		b, _ := json.Marshal(out)
		return b, 0
	default:
		return nil, 0
	}
}

// hookPrefix labels a hook line with the gate that produced it. A line the law
// engine already named ("ratchet: ...") keeps its own label rather than
// stacking a second one.
func hookPrefix(reason string) string {
	if strings.HasPrefix(reason, "ratchet:") {
		return reason
	}
	return "gate: " + reason
}
