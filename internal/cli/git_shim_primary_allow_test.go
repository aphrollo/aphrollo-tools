package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// #894: `aphrollo gate allow primary` reports "Primary-checkout edits ALLOWED
// for this session", but the refusal it names -- primaryRefusalLine's own
// tdd.PrimaryEditsAllowed("") -- never consulted the session at all, so the
// git queue shim kept refusing a non-merge commit, a move off main,
// `checkout -b`/`switch -c` and a mixed reset regardless. The Bash/PowerShell
// PreToolUse hook (postedit.PrimaryCheckoutDecision), which DOES see the
// session, now records the exact git invocation it approved as spent, and
// this shim -- meeting the SAME command a moment later as its own
// subprocess -- consumes that record instead of refusing.

// primaryBashPayload builds a Bash PreToolUse payload naming session and cmd,
// run in cwd -- the shape postedit.PrimaryCheckoutDecision reads.
func primaryBashPayload(t *testing.T, session, cwd, cmd string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name":   "Bash",
		"session_id":  session,
		"tool_use_id": "toolu_primary",
		"cwd":         cwd,
		"tool_input":  map[string]any{"command": cmd},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGitShim_AllowPrimaryCoversTheBashHookAndTheShimForOneCommand(t *testing.T) {
	cfgDir := gateConfigDir(t)
	primary, _, cfg := primaryShimRepo(t)
	const session = "s-primary-bash-then-shim"
	t.Setenv("CLAUDE_SESSION_ID", session)

	if _, err := tdd.AllowWall(tdd.WallPrimary); err != nil {
		t.Fatal(err)
	}

	// The Bash/PowerShell PreToolUse hook meets the command FIRST, exactly as
	// it would inside a real Bash tool call, and records it as spent against
	// the session-wide waiver.
	decision := tdd.PrimaryCheckoutDecision(primaryBashPayload(t, session, primary, "git checkout -b lane/waived"))
	if decision.Action == tdd.Block {
		t.Fatalf("the Bash hook denied a command in a session with `gate allow primary` active: %s", decision.Reason)
	}

	// The shell then actually runs `git checkout -b lane/waived`, which the
	// shim meets as a brand-new subprocess. Before the fix this refused:
	// "gate: primary checkout is merge-only ...".
	var out, errb bytes.Buffer
	code := runGitShim([]string{"checkout", "-b", "lane/waived"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("checkout -b after the Bash hook already approved it: exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if strings.Contains(errb.String(), "merge-only") {
		t.Fatalf("stderr = %q, must not refuse a command the session's `gate allow primary` waiver covers", errb.String())
	}
	if b := currentBranch(t, cfg.realGit, primary); b != "lane/waived" {
		t.Fatalf("branch = %q, want lane/waived — checkout -b must actually have run", b)
	}
	if log := readGateLog(t, cfgDir); !strings.Contains(log, "override-primary-bash-spent") {
		t.Fatalf("gate.log = %q, want the Bash-hook-spent override recorded", log)
	}
}

// A mixed reset (naming a path, not a ref) and a plain commit are two of the
// other forms primaryRefusedVerb classifies; both must pass under the same
// waiver.
func TestGitShim_AllowPrimaryCoversACommitAndAMixedReset(t *testing.T) {
	gateConfigDir(t)
	primary, _, cfg := primaryShimRepo(t)
	const session = "s-primary-commit-reset"
	t.Setenv("CLAUDE_SESSION_ID", session)
	if _, err := tdd.AllowWall(tdd.WallPrimary); err != nil {
		t.Fatal(err)
	}

	writeAndCommit(t, cfg.realGit, primary, "CLAUDE.md", "one\n", "add claude md")
	writeFixtureFile(t, primary, "CLAUDE.md", []string{"one", "edited"})

	// A non-merge commit.
	tdd.PrimaryCheckoutDecision(primaryBashPayload(t, session, primary, "git commit -a -m docs"))
	var cOut, cErr bytes.Buffer
	if code := runGitShim([]string{"commit", "-a", "-m", "docs"}, strings.NewReader(""), &cOut, &cErr, cfg); code != 0 {
		t.Fatalf("commit under the waiver: exit = %d, want 0\nstderr: %s", code, cErr.String())
	}
	if strings.Contains(cErr.String(), "merge-only") {
		t.Fatalf("commit stderr = %q, must not be refused", cErr.String())
	}

	// A mixed reset: `git reset CLAUDE.md` only unstages CLAUDE.md, but
	// primaryRefusedVerb's resetMovesMain classifies any bare operand as a
	// ref to move to, and refuses it exactly like the field report's `git
	// reset -q CLAUDE.md`.
	writeFixtureFile(t, primary, "CLAUDE.md", []string{"two"})
	runFixtureGit(t, cfg.realGit, primary, "add", "CLAUDE.md")
	tdd.PrimaryCheckoutDecision(primaryBashPayload(t, session, primary, "git reset CLAUDE.md"))
	var rOut, rErr bytes.Buffer
	if code := runGitShim([]string{"reset", "CLAUDE.md"}, strings.NewReader(""), &rOut, &rErr, cfg); code != 0 {
		t.Fatalf("reset CLAUDE.md under the waiver: exit = %d, want 0\nstderr: %s", code, rErr.String())
	}
	if strings.Contains(rErr.String(), "merge-only") {
		t.Fatalf("reset CLAUDE.md stderr = %q, must not be refused", rErr.String())
	}
}

// `gate revoke primary` must restore the refusal: a spent record only ever
// covers the ONE command the Bash hook already approved, and once the
// session-wide waiver itself is gone, a NEW command gets no record at all.
func TestGitShim_RevokePrimaryRestoresTheRefusal(t *testing.T) {
	gateConfigDir(t)
	_, _, cfg := primaryShimRepo(t)
	const session = "s-primary-revoke"
	t.Setenv("CLAUDE_SESSION_ID", session)
	if _, err := tdd.AllowWall(tdd.WallPrimary); err != nil {
		t.Fatal(err)
	}
	if _, err := tdd.Revoke(tdd.WallPrimary); err != nil {
		t.Fatal(err)
	}

	decision := tdd.PrimaryCheckoutDecision(primaryBashPayload(t, session, "/repo", "git checkout -b lane/after-revoke"))
	if decision.Action == tdd.Block {
		t.Fatalf("PrimaryCheckoutDecision judges a Bash command by write targets, not verbs, so it must not itself block here: %s", decision.Reason)
	}

	var out, errb bytes.Buffer
	code := runGitShim([]string{"checkout", "-b", "lane/after-revoke"}, strings.NewReader(""), &out, &errb, cfg)
	if code == 0 {
		t.Fatalf("checkout -b after `gate revoke primary` should be refused, got exit 0")
	}
	if !strings.Contains(errb.String(), "primary checkout is merge-only") {
		t.Fatalf("stderr = %q, want the merge-only refusal restored", errb.String())
	}
}

// A different session's own commands must never ride another session's
// `gate allow primary` waiver or its spent records.
func TestGitShim_AllowPrimaryDoesNotCoverADifferentSession(t *testing.T) {
	gateConfigDir(t)
	primary, _, cfg := primaryShimRepo(t)
	t.Setenv("CLAUDE_SESSION_ID", "s-primary-owner")
	if _, err := tdd.AllowWall(tdd.WallPrimary); err != nil {
		t.Fatal(err)
	}

	// A different session never ran `gate allow primary` and never had its
	// own Bash hook mark anything spent.
	t.Setenv("CLAUDE_SESSION_ID", "s-primary-stranger")
	decision := tdd.PrimaryCheckoutDecision(primaryBashPayload(t, "s-primary-stranger", primary, "git checkout -b lane/stranger"))
	if decision.Action == tdd.Block {
		t.Fatalf("PrimaryCheckoutDecision must not itself block a Bash command by verb: %s", decision.Reason)
	}

	var out, errb bytes.Buffer
	code := runGitShim([]string{"checkout", "-b", "lane/stranger"}, strings.NewReader(""), &out, &errb, cfg)
	if code == 0 {
		t.Fatalf("a session that never ran `gate allow primary` should be refused, got exit 0")
	}
	if !strings.Contains(errb.String(), "primary checkout is merge-only") {
		t.Fatalf("stderr = %q, want the merge-only refusal", errb.String())
	}
}
