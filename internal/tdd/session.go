package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/config"
)

// This file holds the session-lifecycle hooks the edit/commit gates depend on:
// the `/tdd` control command (UserPromptSubmit) and session-state cleanup
// (SessionEnd). There is deliberately no full-suite SessionStart baseline — a
// full suite on every session start costs far more than the one first-edit
// false "RED" it would avoid. The first edit establishes its own baseline, as
// PostToolUse does for every edit after.

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
	} else if SessionOff(in.SessionID) {
		// An off session's prompt carries nothing of ours: no harvest, no red
		// reminder, no retro, no style block. Only the switch above answers.
		return PromptResult{}
	} else if harvested := promptHarvest(in.SessionID); harvested != "" {
		r = PromptResult{Message: harvested}
	} else {
		r = PromptResult{Message: reinforce(in.SessionID, in.Cwd)}
	}
	if !r.Block {
		r.Message = joinRetro(r.Message, TakeSessionRetros(in.SessionID))
	}
	// A command's answer replaces the turn, so the block is not spent on it: the
	// next ordinary prompt still carries it.
	if !r.Block && replyStyleFor(in.SessionID) == "terse" {
		r.Style = styleOnce(in.SessionID)
	}
	return r
}

// gateCommandNames are the slash commands that reach the session switch, each
// the same command: the session's `/aphrollo`, the `/gate` the hooks are named
// for, and the `/tdd` sessions have in their fingers. A `/trellis` alias is one
// more entry here.
var gateCommandNames = []string{"/aphrollo", "/" + CmdName, "/" + LegacyCmdName}

// isGateCommand recognises the control command under any of its names.
func isGateCommand(p string) bool {
	for _, name := range gateCommandNames {
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
		LogOverrideDetail("override-off", session, cwd, map[string]string{"switch": "session-off"})
		return "aphrollo OFF for this session — its hooks are silent and decide nothing; the git-side gates (commit, merge, push) stay on. Run `/aphrollo on` to turn it back on."
	case "on", "reset":
		// reset clears any override, which is identical to turning enforcement on.
		if err := setOff(session, false); err != nil {
			return "gate: could not persist the override (" + err.Error() + ")"
		}
		LogOverrideDetail("override-on", session, cwd, map[string]string{"switch": "session-on"})
		return "aphrollo ON for this session."
	case "primary-edits":
		// Pre-rename spelling, retiring next release: same wall, same
		// storage as /tdd allow|revoke primary below.
		switch arg {
		case "on", "off":
			if err := setPrimaryEdits(session, arg == "on"); err != nil {
				return "gate: could not persist the override (" + err.Error() + ")"
			}
			LogOverride("override-primary-edits-"+arg, session, cwd)
			if arg == "on" {
				return "Primary-checkout edits ALLOWED for this session — the merge-only rule is waived. Run `/gate primary-edits off` to restore it."
			}
			return "Primary-checkout edits refused again for this session."
		default:
			return "gate: /tdd primary-edits needs on or off, got " + arg
		}
	case "allow":
		if !KnownWall(arg) {
			return "gate: /tdd allow needs a wall (" + WallNames() + "), got " + arg
		}
		msg, err := AllowWallForSession(session, cwd, arg)
		if err != nil {
			return "gate: could not persist the override (" + err.Error() + ")"
		}
		return msg
	case "revoke":
		if !KnownWall(arg) {
			return "gate: /tdd revoke needs a wall (" + WallNames() + "), got " + arg
		}
		msg, err := RevokeForSession(session, cwd, arg)
		if err != nil {
			return "gate: could not persist the override (" + err.Error() + ")"
		}
		return msg
	case "style":
		switch arg {
		case "terse", "plain":
			if err := setReplyStyle(session, arg); err != nil {
				return "gate: could not persist the style (" + err.Error() + ")"
			}
			LogOverride("override-style-"+arg, session, cwd)
			return "Reply style set to " + arg + " for this session."
		default:
			return "gate: /tdd style needs terse or plain, got " + arg
		}
	default:
		return "gate: unknown subcommand " + sub + " — valid: /gate [status|off|on|reset|style|allow|revoke|primary-edits]"
	}
}

// tddStatus is the one line `/aphrollo status` prints: whether the session's
// hooks are on, the reply style, and what stays on whatever the switch says.
func tddStatus(session string) string {
	s, _ := loadSession(session)
	if s == nil {
		return "aphrollo: no session id, so no per-session switch to read; TRELLIS_OFF=1 is the whole-process one."
	}
	state := "ON"
	if s.GateOff() {
		state = "OFF"
	}
	return fmt.Sprintf("aphrollo: enforcement %s for this session (reply style %s); the git-side gates (commit, merge, push) stay on. Switch: /aphrollo off|on.",
		state, effectiveReplyStyle(s))
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
	if s == nil || s.GateOff() {
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

// EndSession reaps any deferred build/run phase this session left running —
// nothing else will ever harvest or kill it once the session is gone (see
// reapSessionDeferredJobs) — then removes the session's state file so the
// per-session caches do not accumulate in the state directory. Best-effort
// and silent: a missing file or absent session id is a no-op.
func EndSession(raw []byte) {
	var in sessionEndInput
	if err := json.Unmarshal(raw, &in); err != nil || in.SessionID == "" {
		return
	}
	reapSessionDeferredJobs(in.SessionID)
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
// demand. The skill named here is the one `aphrollo install` writes into the
// config dir, so the nudge cannot outlive its target — and it states that
// exact resolved path, not just the skill's name: an agent whose toolset has
// no Skill tool (Claude Code's `builder`/`researcher` types) cannot invoke a
// skill by name at all, and with no path in the nudge it has nothing to open
// but a filesystem-wide search. skillNudge, the managed CLAUDE.md block, and
// WriteTDDSkill all resolve the path through the one resolvedTDDSkillPath, so
// the writer and its readers can never name three different files. When the
// file is not actually on disk, the nudge says so and names the command that
// writes it instead — a path to a file that does not exist is worse than no
// path. It also states the loud-gates contract directly: the hooks run the
// tests, not the model, so re-running a suite by hand after every edit "to
// check" is redundant work — read the `gate:` line the PostToolUse hook
// already printed instead (the hooks print `gate:`, never `tdd:` — issue
// #121).
func skillNudge() string {
	invite := "invoke the `tdd` skill"
	if path, installed := resolvedTDDSkillPath(); installed {
		invite += " (read " + path + ")"
	} else if path != "" {
		invite += " — not installed yet; run `aphrollo install` to write it"
	}
	// One line: the session start is paid for by every session, and the rest
	// (the verdict words, the commit gate, the merge gate) is in the skill and
	// the managed block.
	return "gate: before changing code, " + invite + ". The hooks run the tests, not you: read each `gate:` line after an edit " +
		"(TIMEOUT/SKIPPED = not tested; BUILDING (deferred) = `aphrollo gate status --wait`), never re-run a suite by hand; " +
		"one test+impl commit per task."
}

// HandleSessionStart returns the context injected at session start. It is silent
// when the session has TDD enforcement turned off (`/tdd off`), matching the
// rest of the gate — a session that opted out of the gate should not be nudged
// by it either.
func HandleSessionStart(raw []byte) string {
	var in sessionStartInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ""
	}
	s, statePath := loadSession(in.SessionID)
	if s != nil && s.GateOff() {
		return ""
	}
	// A session start (a resume, a compaction) may have dropped the style block
	// from the context: the next prompt owes it again.
	if s != nil && s.StyleSent {
		s.StyleSent = false
		_ = s.Save(statePath)
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
	parts := []string{skillNudge()}
	// An identity the commit gate will refuse outranks everything else here:
	// it goes first, where it cannot scroll away.
	if alarm := identityAlarmLine(in.Cwd); alarm != "" {
		parts = append([]string{alarm}, parts...)
	}
	if hint := ratchetHintLine(in.Cwd); hint != "" {
		parts = append(parts, hint)
	}
	// The open points live on GitHub, where a session never looks. One line,
	// cached per repo for an hour so it costs no network call per prompt.
	// Off unless the repo or the user opts in with issue-prompt: the line is
	// unasked text, and a repo that never asked for it pays no gh call either.
	issuePrompt := config.ForDir(in.Cwd).Get("issue-prompt").Value.B
	if issuePrompt {
		if issues := issueSummaryLine(RepoRoot(in.Cwd), time.Now()); issues != "" {
			parts = append(parts, issues)
		}
	}
	if digest := maybeWeeklyDigest(time.Now(), issuePrompt); digest != "" {
		parts = append(parts, digest)
	}
	if behind := BinaryBehindLine(time.Now()); behind != "" {
		parts = append(parts, behind)
	}
	exe, _ := os.Executable()
	if bypass := shimBypassLineFn(exe); bypass != "" {
		parts = append(parts, bypass)
	}
	if line != "" {
		parts = append(parts, line)
	}
	// A merge queue that died with the session that started it: its PRs
	// wait unmerged until someone resumes them.
	if queue := MergeQueueStoppedLine(in.Cwd); queue != "" {
		parts = append(parts, queue)
	}
	// A retro a merge left while no session was running, for this repo.
	if retro := TakeRepoRetros(in.Cwd); retro != "" {
		parts = append(parts, retro)
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

// styleOnce is the reply-style block for a prompt of the session, "" once it
// went out: the block is sent with the first prompt after a session start and
// not again, where it used to ride on every prompt. A prompt with no session
// has nowhere to remember it was sent, so it carries the block.
func styleOnce(session string) string {
	s, path := loadSession(session)
	if s == nil {
		return StyleBlock()
	}
	if s.StyleSent {
		return ""
	}
	s.StyleSent = true
	_ = s.Save(path)
	return StyleBlock()
}
