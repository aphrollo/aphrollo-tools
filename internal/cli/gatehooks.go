package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// premergeRoutineSeam is called once, naming the routine, every time the
// merge gate actually runs Mechanical. Tests substitute it to prove that
// "premerge" and its "premergecommit" alias reach the exact same code path
// rather than two copies that happen to agree.
var premergeRoutineSeam = func(routine string) {}

// runGateMergeHook dispatches precommit, premerge (and its "premergecommit"
// alias, retiring next release) and prepush — the git hook subcommands, exit
// non-zero to block. Only prepush reads the hook's stdin: git's ref lines.
func runGateMergeHook(name string, stderr io.Writer) int {
	// prepush runs no suite: the tdd gate is mechanical-only and adversarial
	// review lives in the separate reviewer agent, not this binary. Its one
	// check is the undercover ref wall, which reads git's ref lines from the
	// hook's own stdin and is inert unless the repo set `undercover = true`.
	if name == "prepush" {
		prepushRoot := tdd.RepoRoot(".")
		code := runGatePrepush(os.Stdin, stderr, prepushRoot)
		tdd.AppendEvent(tdd.Event{Kind: "push", Root: prepushRoot, Stage: "prepush", Verdict: gateVerdictWord(code),
			Detail: map[string]string{"sha": headSHA(prepushRoot)}})
		return code
	}
	isMerge := name == "premergecommit" || name == "premerge"
	root := tdd.RepoRoot(".")
	if root == "" {
		return 0 // not in a git repo — nothing to gate
	}
	// The merge gate runs ONLY the mechanical stage: a git merge never fires
	// pre-commit, so nothing else has proven the COMBINED tree still
	// compiles and passes — fail-first and the anti-cheat scan are both
	// judgments about how a change was AUTHORED, already settled by
	// precommit on the commits being merged.
	defer tdd.SetPrecommitLockWait(precommitLockWait())()
	var res tdd.GateResult
	if isMerge {
		premergeRoutineSeam("runGatePremerge")
		// Mechanical prints "gate premerge:" itself now — every stage
		// function that builds a message takes the display name, not the
		// pre-rename "premergecommit", so there is nothing to rewrite here.
		// The routine's own internals (gate.log stage tokens and escape
		// fingerprints) still index on "premergecommit" — see
		// premergeLogToken and appendGateLog's remap — because those are
		// read by tooling, never by a human staring at this stderr line.
		res = tdd.Mechanical(root, tdd.RunSuite(precommitTimeout))
		// A rejection HERE is the pre-merge-commit hook blocking an
		// automatic, conflict-free merge — the one case where git still
		// leaves MERGE_HEAD and the merged index in the checkout ("Not
		// committing merge; use 'git commit' to complete the merge."),
		// refusing every OTHER session sharing it until a human runs
		// `git merge --abort`. The marker lets the git-queue shim recognise
		// its own rejection and clean that up automatically. Concluding a
		// CONFLICTED merge fires pre-commit instead (routed to Mechanical
		// internally by Precommit, task A10) and must never reach here —
		// scoping the write to this branch is what keeps that path
		// untouched.
		if res.Blocked {
			tdd.WriteMergeRejectedMarker(root, res.Message)
			// Two gates disagreeing about one tree, or a survivor reaching
			// the last gate that could stop it, is the loop's own evidence
			// about a missing stage. Nothing recorded it before; now it
			// records itself, deduped by fingerprint.
			tdd.NoteMergeGateEscape(root, res.Message, stderr)
		}
	} else if refusal := tdd.ManagedBlockRefusal(root); refusal != "" {
		// Ahead of every other stage and on every fast path: an
		// aphrollo.toml-only commit reaches none of the package stages.
		res = tdd.GateResult{Blocked: true, Message: "gate precommit: managed-block-stale: " + refusal}
		tdd.AppendGateLog("precommit", root, "managed block", "managed-block-stale", 0)
	} else {
		res = tdd.Precommit(root, tdd.RunSuite(precommitTimeout))
	}
	if !res.Blocked {
		// Stamp the tree a suite actually RAN GREEN on, so the post-commit
		// hook can put the gate note on the commit and CI can tell a red on
		// a proven tip from a red on an ungated one. A gate that allowed the
		// commit because there was nothing to test has proven nothing and
		// stamps nothing.
		//
		// The merge gate stamps too (#749): its suites are the ones that ran
		// on the merged tree, and the stamp is keyed on that tree, so a
		// commit-msg judging an amend that leaves the tree unchanged reads
		// the merge's own verdict instead of whichever hook ran last. An
		// automatic merge fires no post-commit, so the stamp outlives the
		// merge until the next commit's post-commit consumes it; it vouches
		// for its own tree and no other.
		tdd.StampGreenSuiteIfProven(root)
	}
	// Surface the note (e.g. a fail-open skip) even when allowing — the gate
	// is never silent about why it did or didn't run.
	if res.Message != "" {
		fmt.Fprintln(stderr, res.Message)
	}
	code := 0
	if res.Blocked {
		code = 1
	}
	// The stage lines AppendGateLog wrote already carry commit_gate/merge_gate;
	// the run's overall verdict is its own kind so counting runs never counts
	// stages too.
	kind := "commit_gate_result"
	if isMerge {
		kind = "merge_gate_result"
	}
	tdd.AppendEvent(tdd.Event{Kind: kind, Root: root, Verdict: gateVerdictWord(code)})
	return code
}

// headSHA is the commit root has checked out, "" when it cannot be read: the
// push event names the commit the CI events that follow it are about.
func headSHA(root string) string {
	out, err := lightOutput(run.Spec{Name: "git", Args: []string{"-C", root, "rev-parse", "HEAD"}, Stderr: io.Discard})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gateVerdictWord is the outcome word an event carries for a hook's exit code.
func gateVerdictWord(code int) string {
	if code == 0 {
		return "pass"
	}
	return "blocked"
}

// recordHookTiming writes the hook.timing event of one Claude hook run: which
// hook, how long it took, and the actor (session, and agent when a subagent
// made the call). The userpromptsubmit ones are the message boundaries edits
// per message are counted between. An unreadable payload still gets its timing,
// with no actor and no repo. The time the hook spent waiting for its shadow
// records, which it does after answering, is left out of the seconds and said
// apart.
func recordHookTiming(hook string, raw []byte, start time.Time) {
	e := hookTimingEvent(hook, raw, time.Since(start), shadow.TakeWaited())
	tdd.AppendEvent(e)
}

// hookTimingEvent is the hook.timing event of a hook that took elapsed, of which
// waited was spent on shadow records: the seconds are the hook's own, and the wait
// is in detail shadow_ms.
func hookTimingEvent(hook string, raw []byte, elapsed, waited time.Duration) tdd.Event {
	var in struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
		Cwd       string `json:"cwd"`
	}
	_ = json.Unmarshal(raw, &in)
	actor := in.SessionID
	if in.AgentID != "" {
		actor += "/" + in.AgentID
	}
	detail := map[string]string{"hook": hook}
	if waited > 0 {
		detail["shadow_ms"] = strconv.FormatInt(waited.Milliseconds(), 10)
	}
	return tdd.Event{Kind: "hook.timing", Root: in.Cwd, Actor: actor, Secs: max(elapsed-waited, 0).Seconds(), Detail: detail}
}
