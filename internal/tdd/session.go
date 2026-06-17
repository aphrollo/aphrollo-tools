package tdd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This file ports the session-lifecycle hooks the edit/commit gates depend on
// but the first migration left behind: the `/tdd` control command
// (UserPromptSubmit) and session-state cleanup (SessionEnd). The original also
// ran a full-suite BASELINE at SessionStart so the first edit had a delta to
// diff against; that is deliberately NOT ported — a full suite on every session
// start costs far more than the one first-edit false "RED" it avoids, and the
// migration's whole bias is to drop accreted cost. The first edit simply
// establishes its own baseline, as PostToolUse already does for every edit after.

var errNoSession = errors.New("no session id")

// --- UserPromptSubmit: the /tdd control command + RED reinforcement ----------

type promptInput struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
}

// PromptResult is the UserPromptSubmit verdict. Block is set for an explicit
// `/tdd` command — the command's output replaces the turn instead of reaching
// the model. Otherwise Message is advisory context injected ahead of the prompt
// (empty = silent).
type PromptResult struct {
	Block   bool
	Message string
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
	if p == "/tdd" || strings.HasPrefix(p, "/tdd ") {
		sub := ""
		if f := strings.Fields(p); len(f) > 1 {
			sub = strings.ToLower(f[1])
		}
		return PromptResult{Block: true, Message: tddCommand(sub, in.SessionID)}
	}
	return PromptResult{Message: reinforce(in.SessionID, in.Cwd)}
}

// tddCommand handles a /tdd subcommand and returns the message to surface.
func tddCommand(sub, session string) string {
	switch sub {
	case "", "status":
		return tddStatus(session)
	case "off":
		if err := setOff(session, true); err != nil {
			return "tdd: could not persist the override (" + err.Error() + ")"
		}
		return "TDD enforcement OFF for this session — edits are no longer gated. Run `/tdd on` to re-enable."
	case "on", "reset":
		if err := setOff(session, false); err != nil {
			return "tdd: could not persist the override (" + err.Error() + ")"
		}
		return "TDD enforcement ON for this session."
	default:
		return "tdd: unknown subcommand " + sub + " — valid: /tdd [status|off|on|reset]"
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
	return fmt.Sprintf("tdd: last test outcome on %s was RED (%s) — make it green before adding behavior.",
		filepath.Base(root), ps.Outcome)
}

// RenderPrompt turns a PromptResult into the UserPromptSubmit hook payload. A
// blocking command emits a deny envelope (the message replaces the turn); a
// non-empty reinforcement is injected as additional context; an empty result is
// silent. The exit code is always 0 — the prompt hook never errors the session.
func RenderPrompt(r PromptResult) ([]byte, int) {
	if r.Message == "" {
		return nil, 0
	}
	out := promptOutput{}
	out.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	out.HookSpecificOutput.AdditionalContext = r.Message
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
}

// skillNudge is injected at session start. The commit gate enforces the
// RED→GREEN OUTCOME, but a gated session otherwise trains the model to lean on
// the gate and skip the skills entirely — and the nuances the gate can't check
// (test sizing, the pyramid ratio, DAMP-over-DRY, thin vertical slices) live
// only in those skills. So the directive is to INVOKE them, not a paraphrase of
// their contents: the skill bodies stay out of context until the model reads
// them on demand.
const skillNudge = "tdd: before writing or changing any code this session, invoke the " +
	"`test-driven-development` and `incremental-implementation` skills (read their SKILL.md). " +
	"The commit gate enforces RED→GREEN; the skills carry what it cannot check — test sizing, the " +
	"80/15/5 pyramid, DAMP-over-DRY, thin vertical slices. Treat reading them as a step, not a suggestion."

// HandleSessionStart returns the context injected at session start. It is silent
// when the session has TDD enforcement turned off (`/tdd off`), matching the
// rest of the gate — a session that opted out of the gate should not be nudged
// by it either.
func HandleSessionStart(raw []byte) string {
	var in sessionStartInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ""
	}
	if s, _ := loadSession(in.SessionID); s != nil && s.Overrides.Off {
		return ""
	}
	return skillNudge
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
