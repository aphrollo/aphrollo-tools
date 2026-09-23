package tdd

import (
	"encoding/json"
	"fmt"
	"strings"
)

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
	if kind == Ignore {
		return Decision{Action: Allow}, nil
	}
	// Judged over the lines this edit ADDS, against the file as it will be:
	// a smell that already lived in the file is not this edit's, and denying
	// it leaves an author with no move but to stop working in that file.
	pre, post := editImages(in, path)
	added := addedLines(pre, post)
	var d Decision
	if kind == Test {
		d = evaluateAdded(post, added, langOf(path), testPolicies, editPhase)
	} else {
		d = evaluateSourceAdded(post, added, path, editPhase)
	}
	full := withQualityNotes(d, path, post, added)
	full.Escapes = d.Escapes
	return full, nil
}

// withQualityNotes attaches the advisory test-quality notes to a decision.
// They never raise a Block (a judgement call must not wedge a session) and
// never mask one: a real oracle smell keeps its own verdict and reason.
//
// The notes are judged over post, the file as the edit leaves it, and kept
// only for the lines the edit adds: a note names a line of the FILE, one the
// reader can open, never a line of the edit's new_string (issue #760).
func withQualityNotes(d Decision, path, post string, added map[int]bool) Decision {
	if d.Action == Block {
		return d
	}
	notes := testQualityNotesOn(path, post, added)
	if len(notes) == 0 {
		return d
	}
	joined := strings.Join(notes, "; ")
	if d.Action == Warn && d.Reason != "" {
		return Decision{Action: Warn, Reason: d.Reason + "; " + joined}
	}
	return Decision{Action: Warn, Reason: joined}
}

// evaluateSourceAdded gates a Source-file edit over the lines it ADDS. For
// most languages that is the suppressions only — oracle smells have no
// meaning outside test code. Zig is the exception: its tests are `test
// "..." {}` blocks INLINE in ordinary src/*.zig files, so the smells are
// evaluated over the added lines that fall inside such a block, and a real
// std.time.sleep in a production function still flows.
func evaluateSourceAdded(post string, added map[int]bool, path string, p phase) Decision {
	l := langOf(path)
	if !isZigPath(path) {
		return evaluateAdded(post, added, l, sourcePolicies, p)
	}
	best := evaluateAdded(post, added, l, sourcePolicies, p)
	if best.Action == Block {
		return best // a suppression already blocks; nothing outranks Block
	}
	inTests := intersectLines(added, zigTestLines(newView(post, l).code))
	if len(inTests) == 0 {
		return best // no inline test in this edit — production code only
	}
	if d := evaluateAdded(post, inTests, l, oracleSmells, p); d.Action > best.Action {
		d.Escapes = append(best.Escapes, d.Escapes...)
		return d
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

// editLogPath is the path an edit named, independent of ClassifyFile's
// ranking. editTarget answers "does this edit need a test run" and correctly
// reads a Markdown or plain-text file as Ignore for that question — but a
// denial's log line needs the file it fired on regardless, or a law whose
// whole domain is docs (doc_reference_exists) leaves most of its refusals
// with no path a review can trace back to a file.
func editLogPath(in preToolUseInput) string {
	if in.ToolInput.FilePath != "" {
		return in.ToolInput.FilePath
	}
	return in.ToolInput.NotebookPath
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
