package tdd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// This file holds the session-lifecycle hooks the edit/commit gates depend on:
// the `/tdd` control command (UserPromptSubmit) and session-state cleanup
// (SessionEnd). There is deliberately no full-suite SessionStart baseline — a
// full suite on every session start costs far more than the one first-edit
// false "RED" it would avoid. The first edit establishes its own baseline, as
// PostToolUse does for every edit after.

var errNoSession = errors.New("no session id")

// --- UserPromptSubmit: the /tdd control command + RED reinforcement ----------

type promptInput struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
}

// PromptResult is the UserPromptSubmit verdict. Block is set for an explicit
// `/tdd` command — the command's output replaces the turn instead of reaching
// the model. Message is advisory context injected ahead of the prompt (empty =
// nothing to say). Style is the reply-style block (see style.go), appended
// after Message in additionalContext on every prompt unless the session's
// effective style is "plain" (empty = nothing to add).
type PromptResult struct {
	Block   bool
	Message string
	Style   string
}

// HandlePrompt implements the UserPromptSubmit hook. It intercepts
// `/tdd off|on|status|reset` as a per-session enforcement override, and on any
// other prompt re-injects the last RED outcome for the current project so the
// gate survives context compaction. It is silent unless the last outcome was
// RED — the same bias as PostToolUse, so a healthy project adds no noise.
func HandlePrompt(raw []byte) PromptResult {
	var in promptInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return PromptResult{}
	}
	p := strings.TrimSpace(in.Prompt)
	var r PromptResult
	if isGateCommand(p) {
		f := strings.Fields(p)
		sub, arg := "", ""
		if len(f) > 1 {
			sub = strings.ToLower(f[1])
		}
		if len(f) > 2 {
			arg = strings.ToLower(f[2])
		}
		r = PromptResult{Block: true, Message: tddCommand(sub, arg, in.SessionID, in.Cwd)}
	} else if harvested := promptHarvest(in.SessionID, in.Cwd); harvested != "" {
		r = PromptResult{Message: harvested}
	} else {
		r = PromptResult{Message: reinforce(in.SessionID, in.Cwd)}
	}
	if replyStyleFor(in.SessionID) == "terse" {
		r.Style = StyleBlock()
	}
	return r
}

// isGateCommand recognises the control command under both its new name and the
// one sessions have in their fingers.
func isGateCommand(p string) bool {
	for _, name := range []string{"/" + CmdName, "/" + LegacyCmdName} {
		if p == name || strings.HasPrefix(p, name+" ") {
			return true
		}
	}
	return false
}

// tddCommand handles a /gate subcommand and returns the message to surface.
// Flipping enforcement is the one thing a session can do to the gate itself,
// so both directions leave a line in gate.log: an override nobody counts is
// an override nobody manages. cwd only names WHERE it was flipped.
func tddCommand(sub, arg, session, cwd string) string {
	switch sub {
	case "", "status":
		return tddStatus(session)
	case "off":
		if err := setOff(session, true); err != nil {
			return "gate: could not persist the override (" + err.Error() + ")"
		}
		logOverride("override-off", session, cwd)
		return "TDD enforcement OFF for this session — edits are no longer gated. Run `/gate on` to re-enable."
	case "on", "reset":
		// reset clears any override, which is identical to turning enforcement on.
		if err := setOff(session, false); err != nil {
			return "gate: could not persist the override (" + err.Error() + ")"
		}
		logOverride("override-on", session, cwd)
		return "TDD enforcement ON for this session."
	case "primary-edits":
		switch arg {
		case "on", "off":
			if err := setPrimaryEdits(session, arg == "on"); err != nil {
				return "gate: could not persist the override (" + err.Error() + ")"
			}
			logOverride("override-primary-edits-"+arg, session, cwd)
			if arg == "on" {
				return "Primary-checkout edits ALLOWED for this session — the merge-only rule is waived. Run `/gate primary-edits off` to restore it."
			}
			return "Primary-checkout edits refused again for this session."
		default:
			return "gate: /tdd primary-edits needs on or off, got " + arg
		}
	case "style":
		switch arg {
		case "terse", "plain":
			if err := setReplyStyle(session, arg); err != nil {
				return "gate: could not persist the style (" + err.Error() + ")"
			}
			logOverride("override-style-"+arg, session, cwd)
			return "Reply style set to " + arg + " for this session."
		default:
			return "gate: /tdd style needs terse or plain, got " + arg
		}
	default:
		return "gate: unknown subcommand " + sub + " — valid: /gate [status|off|on|reset|style|primary-edits]"
	}
}

// tddStatus renders the current enforcement flag and the per-project outcomes,
// sorted by root for a stable display.
func tddStatus(session string) string {
	s, _ := loadSession(session)
	if s == nil {
		return "TDD: no session id, enforcement state unavailable."
	}
	state := "ON"
	if s.Overrides.Off {
		state = "OFF"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "TDD enforcement: %s", state)
	fmt.Fprintf(&b, "\n  reply style: %s", effectiveReplyStyle(s))
	if len(s.ByProject) == 0 {
		b.WriteString("\n  no test outcomes observed yet this session")
		return b.String()
	}
	roots := make([]string, 0, len(s.ByProject))
	for r := range s.ByProject {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	for _, r := range roots {
		fmt.Fprintf(&b, "\n  %s: outcome=%s", r, s.ByProject[r].Outcome)
	}
	return b.String()
}

// reinforce returns a one-line reminder when the current project's last recorded
// outcome was RED, so the model is renudged after a context compaction. It is
// silent when enforcement is off, the project is unknown, or the last outcome
// was not RED.
func reinforce(session, cwd string) string {
	if cwd == "" {
		return ""
	}
	s, _ := loadSession(session)
	if s == nil || s.Overrides.Off {
		return ""
	}
	root := findRootFrom(cwd)
	ps, ok := s.ByProject[root]
	if !ok || !Outcome(ps.Outcome).IsRed() {
		return ""
	}
	return fmt.Sprintf("gate: last test outcome on %s was RED (%s) — make it green before adding behavior.",
		filepath.Base(root), ps.Outcome)
}

// RenderPrompt turns a PromptResult into the UserPromptSubmit hook payload. A
// blocking command emits a deny envelope (the message replaces the turn, and
// Reason carries it unstyled); Message and Style are joined into
// additionalContext, Style trailing so it reads as a reminder rather than the
// point of the turn; both empty is silent. The exit code is always 0 — the
// prompt hook never errors the session.
func RenderPrompt(r PromptResult) ([]byte, int) {
	ctx := r.Message
	if r.Style != "" {
		if ctx != "" {
			ctx += "\n\n" + r.Style
		} else {
			ctx = r.Style
		}
	}
	if ctx == "" {
		return nil, 0
	}
	out := promptOutput{}
	out.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	out.HookSpecificOutput.AdditionalContext = ctx
	if r.Block {
		out.Decision = "block"
		out.Reason = r.Message
	}
	b, _ := json.Marshal(out)
	return b, 0
}

type promptOutput struct {
	Decision           string `json:"decision,omitempty"`
	Reason             string `json:"reason,omitempty"`
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// --- SessionEnd: drop the per-session state file -----------------------------

type sessionEndInput struct {
	SessionID string `json:"session_id"`
}

// EndSession removes the session's state file so the per-session caches do not
// accumulate in the state directory. Best-effort and silent: a missing file or
// absent session id is a no-op.
func EndSession(raw []byte) {
	var in sessionEndInput
	if err := json.Unmarshal(raw, &in); err != nil || in.SessionID == "" {
		return
	}
	if _, path := loadSession(in.SessionID); path != "" {
		_ = os.Remove(path)
	}
}

// --- SessionStart: surface the build skills the gates can't encode -----------

type sessionStartInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
}

// skillNudge is injected at session start. The commit gate enforces the
// RED→GREEN OUTCOME, but a gated session otherwise trains the model to lean on
// the gate and skip the skill entirely — the nuances the gate can't check
// (test sizing, the pyramid ratio, DAMP-over-DRY, thin vertical slices) live
// only there. So the directive is to INVOKE it, not a paraphrase of its
// contents: the skill body stays out of context until the model reads it on
// demand. The skill named here is the one `gate init` writes into the config
// dir, so the nudge cannot outlive its target. It also states the loud-gates
// contract directly: the hooks run the tests, not the model, so re-running a
// suite by hand after every edit "to check" is redundant work — read the
// `gate:` line the PostToolUse hook already printed instead (the hooks print
// `gate:`, never `tdd:` — issue #121).
const skillNudge = "gate: before writing or changing any code this session, invoke the " +
	"`tdd` skill (read its SKILL.md). The hooks run the tests, not you: " +
	"after every Edit/Write, read the `gate:` line the PostToolUse hook prints (green with count / " +
	"red-missing-impl / red / TIMEOUT / SKIPPED / QUEUED-SKIPPED) instead of running a suite by hand to " +
	"check — the only manual runs are mutation proofs, soaks, or a targeted rerun after the hook said " +
	"TIMEOUT or SKIPPED. Commit ONE mixed test+impl commit per task; the pre-commit gate re-proves RED " +
	"and runs the touched crates' suites, and merges are gated by pre-merge-commit."

// HandleSessionStart returns the context injected at session start. It is silent
// when the session has TDD enforcement turned off (`/tdd off`), matching the
// rest of the gate — a session that opted out of the gate should not be nudged
// by it either.
func HandleSessionStart(raw []byte) string {
	var in sessionStartInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ""
	}
	s, _ := loadSession(in.SessionID)
	if s != nil && s.Overrides.Off {
		return ""
	}
	// Disk hygiene rides along here because session start is the only
	// moment nobody is waiting on a build: the sweep itself is detached
	// (see maybeStartBackgroundGC), so what this session surfaces is the
	// PREVIOUS one's result — one line, only when it actually freed
	// something.
	line := gcReportLine()
	maybeStartBackgroundGC(in.Cwd)
	// At most one extra line each, in a fixed order: a session start that
	// scrolls is a session start nobody reads. The style block rides here so
	// it is present from the very first turn, not just from the second one.
	parts := []string{skillNudge}
	if effectiveReplyStyle(s) == "terse" {
		parts = append(parts, StyleBlock())
	}
	if hint := ratchetHintLine(in.Cwd); hint != "" {
		parts = append(parts, hint)
	}
	// The open points live on GitHub, where a session never looks. One line,
	// cached per repo for an hour so it costs no network call per prompt.
	if issues := issueSummaryLine(RepoRoot(in.Cwd), time.Now()); issues != "" {
		parts = append(parts, issues)
	}
	if digest := maybeWeeklyDigest(time.Now()); digest != "" {
		parts = append(parts, digest)
	}
	if line != "" {
		parts = append(parts, line)
	}
	return strings.Join(parts, "\n\n")
}

// RenderSessionStart turns the nudge into the SessionStart hook payload: a
// non-empty message becomes additionalContext; empty is silent. The exit code
// is always 0 — a session-start hook never errors the session.
func RenderSessionStart(msg string) ([]byte, int) {
	if msg == "" {
		return nil, 0
	}
	out := sessionStartOutput{}
	out.HookSpecificOutput.HookEventName = "SessionStart"
	out.HookSpecificOutput.AdditionalContext = msg
	b, _ := json.Marshal(out)
	return b, 0
}

type sessionStartOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}
