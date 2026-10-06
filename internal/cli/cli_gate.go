package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/config"
	"github.com/aphrollo/aphrollo-tools/internal/shadow"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const gateUsage = `usage: aphrollo gate <subcommand>

Subcommands:
  sessionstart      Inject the build-skill nudge at session start
  pretooluse        Evaluate a Claude Code PreToolUse edit payload from stdin
  posttooluse       Run related tests after an edit and report RED/GREEN
  posttoolusefailure  PostToolUseFailure hook: count a suite the agent ran by hand that
                    exited non-zero as a run of the units it covered
  userpromptsubmit  Handle the /gate command and re-inject a RED reminder
  sessionend        Drop the session's state file
  stop              Stop hook: block the end of a turn once when a deferred run
                    finished red after Claude's last hook (never twice in a row)
  subagentstop      SubagentStop hook: the same check, for the subagent's lane
  taskcompleted     TaskCompleted hook: exit 2 with the failing tests while the
                    task's tests are red
  precommit         Git pre-commit gate: fail-first + mechanical (run in the repo)
  premerge          Git pre-merge-commit gate: mechanical ONLY, no fail-first/anti-cheat
  premergecommit    (alias of premerge; retiring next release)
  allow             allow [primary|discard|red-green]: waive a wall for this session (bare: list waivers)
  revoke            revoke [primary|discard|red-green]: restore a wall waived by allow (bare: list waivers)
  primary-edits     on|off (alias of allow/revoke primary; retiring next release)
  postcommit        Git post-commit hook: write the refs/notes/gate note on the
                    commit just made — what lets CI tell a red on a gated tip
                    from a red on an ungated one — and, in a repo declaring
                    prune-lanes-on-merge = true, sweep the lanes a conflict
                    resolved by hand and concluded with "git commit" just
                    landed (the shape post-merge never sees). Never blocks,
                    never fails
  postmerge         Git post-merge hook: in an aphrollo repo, records the
                    merges this one brought onto trunk that workspace merge
                    did not make; in a repo declaring prune-lanes-on-merge =
                    true, sweeps the lanes it landed (guarded, never the
                    worktree the hook fired in). Otherwise inert. Never blocks
  postrewrite       Git post-rewrite hook: records the new commits a rebase or an
                    amend wrote (stdin: "<old> <new>" lines) in the canary's
                    record of commits made through the real path. Never blocks
  mutants           run measures THIS checkout's lane against its base in the
                    FOREGROUND and is the check — exit 1 on an unaccepted
                    survivor, a mutant that stayed unmeasured, or a run that
                    reached no verdict; exit 2 on a bad invocation.
                    run --base <ref> measures against that ref instead, which
                    is what nightly CI on main passes its checkpoint to.
                    prove is the HAND mutation proof for existing code
  prepush           No-op (mechanical-only mode); kept for back-compat with a
                    lingering pre-push shim. Never blocks.
  runphase          Run one deferred build/run phase from its job record (--job);
                    spawned by posttooluse, not typed by hand
  commitmsg         commit-msg hook: reject a message carrying a deny pattern
                    (opt-in per workspace: undercover = true)
  doctor            Report one line per install check (hooks, shims, locks, managed
                    skills/agents, the primary checkout branch, the golangci-lint
                    version CI pins, CI clippy list); exit 1 on any FAIL
  statusline        Render the one-line gate badge from a statusline payload on
                    stdin: the colour is the state (green armed, red standing
                    failure, yellow running, gray off), with a tag inside the
                    brackets when yellow needs naming; wired into settings.json
                    by init
  stats             Tally the gate stage lines (event logs) by stage and outcome (--since 7d), and the open
                    escape count
  output            Read-only: print the TEXT of the last settled suite run the
                    gate made for this repo root — header (when, which stage,
                    which command, which verdict, how long) then the run's own
                    bytes, unfiltered. stats answers what the verdict WAS;
                    this answers what the run PRINTED, so reading one assertion
                    line never costs a re-run. Exits non-zero, saying which,
                    when no run is recorded for this root or the record is
                    older than the freshness window
  status            Read-only: deferred edit jobs on this box, every build slot's
                    holder, this checkout's own position in the cargo-shim queue
                    (queued, and behind what), and this checkout's own
                    mutation-run state — what an inconclusive
                    BUILDING/TIMEOUT/QUEUED-SKIPPED gate line points at instead
                    of a rerun. --wait [<dir>] blocks until the deferred edit
                    job of this checkout, or of <dir> (the path a BUILDING
                    line names), has a verdict and prints it verbatim
  issue             (alias of aphrollo issue; retiring next release) Open one
                    labelled issue against the repo's GitHub remote and print
                    its URL (--label, --body, --repo, --new-label). An open
                    point is an issue, never a markdown follow-up
  feedback          Report a defect in the GATE ITSELF to the tool's tracker, with
                    the reporting repo and tip attached
  escape            The escape loop: record | sync | list | verify-closure <pr> |
                    check-closes <pr>. A red after a local green is recorded
                    and opened as a labelled issue; verify-closure refuses a PR
                    that closes one without changing a law, a gate stage or a
                    named test; check-closes warns on a bare issue mention and
                    errors on a comma list after one closing keyword
  classify-diff     Read-only: [--json] <base> [<head>] prints the change's class
                    (docs-only, comment-only, workflow-only, code) from the
                    commit gate's own per-file rules; any failure prints code.
                    CI's changes job sizes the run by it
  split-commit      [--dry] [-m <message>]: when fail-first refuses a commit because
                    its staged tests already pass at HEAD, commit those tests alone
                    and leave the rest staged. Writes the first commit from the
                    index only and never touches the working tree; --dry names
                    both commits and writes nothing
  probe             discard [--dry] <file>...: restore exactly the named files
                    to HEAD, the route for stripping a refused probe arm. Writes
                    the full diff to a backup under the gate state dir first;
                    --dry prints each file's loss and the backup path and stops.
                    Refuses staged content, paths outside the repo, directories
                    and globs
  gc                Reclaim stale build dirs: idle incremental caches, dead gate dirs,
                    orphan worktree builds (--repo, --older-than 3d, --dry)
  install           (alias of aphrollo install; retiring next release) Install
                    the git-hook shims into a repo (--repo, --dry)
  init              (alias of aphrollo install; retiring next release) Set up TDD:
                    session hooks in settings.json + the global git gate
                    (--no-git, --uninstall). ALSO EDITS FILES IN A REPO: the managed
                    block in <repo>/CLAUDE.md and <repo>/.ratchet/README.md, where
                    <repo> is --repo (default: the working directory's repo)
  selfcheck         Install-time smoke test: build a marker-less temp tree and require
                    FindProjectRoot to come back empty for it. aphrollo update runs
                    this against the CANDIDATE binary before swapping it in (#532)
  cargo             cargo-queue shim: queue a DIRECT cargo invocation behind the same
                    per-target-dir build slots the hooks/gates use (config: budgets.cargo_wait_s,
                    box.build_slots)
  git               git-queue shim: queue a DIRECT index-mutating git invocation behind a
                    per-repo lock so concurrent sessions sharing one checkout don't collide
                    on .git/index.lock (config: budgets.git_wait_s)
  lint              lint wrapper: run golangci-lint behind the box-wide, cross-account
                    lint lock (config: budgets.lint_wait_s) so a local commit gate and a
                    CI runner sharing this box never collide on golangci-lint's own
                    lock instead of finding it clean or dirty

Autonomous TDD gates. pretooluse reads the hook JSON on stdin; on a smell in a
test file (real-time sleep, tautological assertion, focused/disabled test) it
exits 2 with a deny envelope, and warns on a suppression; otherwise it is
silent. posttooluse runs the project's related tests after an edit and surfaces
a failure summary (silent unless RED). userpromptsubmit intercepts
/gate [status|off|on|reset] and otherwise re-injects the last RED outcome.
sessionend cleans up the per-session state file. stop and subagentstop block the
end of a turn once, with the red's gate line as the reason, when a deferred run
finished red after the last hook (stop_hook_active true allows); taskcompleted
exits 2 with the failing tests while the task's tests are red; /gate off allows
all three. precommit verifies fail-first,
blocks a newly-added suppression, and runs the suite, exiting non-zero to block.
Commands a root declares under [aphrollo.precommit] in aphrollo.toml have no
HEAD baseline: any failure blocks, including one HEAD already had, unless the
command is written { argv = [...], baseline = "lines" }. Then a failure is run
again on HEAD's tree and blocks only over output lines HEAD's run did not print
(each checkout's path and trailing whitespace aside), or when HEAD's run cannot
be made.
premerge (alias: premergecommit) runs ONLY the mechanical stage over the
merge's staged files — no fail-first (a fresh test's RED/GREEN belongs to the
authoring commit, already proven by precommit there) and no anti-cheat
suppression scan (same reasoning) — so a git merge, which never fires
pre-commit, still proves the COMBINED result compiles and passes before it
lands. allow/revoke waive or restore a wall (currently just primary, the
merge-only primary-checkout rule) for the session; primary-edits on|off is
the pre-rename alias. prepush is a
mechanical-only no-op (adversarial review lives in the separate reviewer
agent now), kept only so a lingering pre-push shim exits cleanly. cargo is
the cargo-queue shim: a session that prepends the installed cargo-queue dir
to its OWN PATH gets a DIRECT cargo invocation queued behind the same
machine-wide lock the hooks/gates use, instead of silently waiting on
cargo's own build-dir lock with zero visibility — silent when the lock is
free, one line when it has to wait, one line when it acquires, exits 75
(EX_TEMPFAIL) on giving up. git is the analogous shim for git: only
index-mutating verbs (add, commit, merge, checkout, switch, restore
--staged, reset, stash, rm, mv, rebase, cherry-pick, revert, am, apply
--index/--cached, worktree add/remove, pull) queue behind a per-repo lock;
read-only verbs (status, diff, log, show, ...) pass straight through
untouched. Source edits always flow.
`

// postEditTimeout bounds a PostToolUse suite run so a hung test can't wedge the
// session. Bumped 60s -> 100s 2026-08-15 (build-infra-fix task A2): a scoped
// per-edit cargo build on a Bevy-sized crate routinely blew the old 60s
// budget on a cold cache, which — before PostEdit became loud on every
// outcome — silently read as "nothing to report" instead of the TIMEOUT it
// actually was. precommitTimeout is longer: the commit gate runs the staged
// crates' suites (and the fail-first worktree build), and on heavy-dependency
// repos a first-warm build alone can pass five minutes; a timeout fails open,
// so the ceiling only caps how long a commit can stall, never what it
// proves.
//
// Both alias the canonical tdd.Default*Timeout constants (single source of
// truth, tdd package) rather than redeclaring the numbers here — a 2026-08-15
// review found init.go's PostToolUse hook-TEMPLATE timeout had silently
// drifted to 90s after this Go-side value moved to 100s, so the Claude Code
// harness was killing the hook process from OUTSIDE before RunSuite's own
// context deadline ever fired. init.go's template is now DERIVED from
// tdd.DefaultPostEditTimeout too, so the two can't drift apart again.
const (
	postEditTimeout  = tdd.DefaultPostEditTimeout
	precommitTimeout = tdd.DefaultPrecommitTimeout
)

// defaultPrecommitLockWait is how long a commit's cargo stage queues for a
// build slot before REJECTING the commit. Twenty minutes: the gate's target
// dir is one per repo, so two lanes committing at once serialise behind each
// other's full suite, and a short wait would throw away a legitimate commit.
// It mirrors the tdd package's own default; the knob below is what an
// operator on a busy box turns.
const defaultPrecommitLockWait = 1200 * time.Second

// The operator budget knobs are keys of the box's config (the user's
// config.toml, [budgets]), with their old APHROLLO_* variables still read for
// one more release:
//
//	budgets.edit_s       the edit hook's suite budget (default 110s)
//	budgets.lock_wait_s  the commit gate's build-slot wait (default 1200s)
//
// The edit hook's own build-slot wait is deliberately NOT tunable: it is
// zero by contract (one try, then QUEUED-SKIPPED), because an edit that
// waits spends its whole test budget losing a race to a multi-minute build.
func postEditBudget() time.Duration {
	return tdd.PostEditBudget()
}

func precommitLockWait() time.Duration {
	return config.Box().Seconds("budgets.lock_wait_s")
}

// isHelpArg reports whether s asks for help. Every subcommand below that
// parses its own flag.FlagSet already gets -h/--help handling for free from
// the stdlib (it prints usage and returns flag.ErrHelp before anything
// runs); the git-hook subcommands below take no flags at all and dispatch
// straight to a function that mutates or gates the tree, so THEY have to
// check by hand or a stray --help reaches the body (reported from the
// field: `aphrollo gate precommit --help` ran the real gate against cwd).
func isHelpArg(s string) bool {
	return s == "-h" || s == "--help" || s == "help"
}

// gateHelpRequested reports whether rest (a subcommand's own args, i.e.
// args[1:] of the dispatch below) is a bare help flag.
func gateHelpRequested(rest []string) bool {
	return len(rest) > 0 && isHelpArg(rest[0])
}

// runGate dispatches the TDD hook subcommands. Like the guardrail hook, every
// path reads from the provided reader and a parse error fails OPEN (exit 0) so
// a malformed payload can never wedge the session.
func runGate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		w, code := stderr, 2
		if len(args) > 0 {
			w, code = stdout, 0
		}
		fmt.Fprint(w, gateUsage)
		return code
	}
	if args[0] == "install" {
		return runGateInstall(args[1:], stdout, stderr)
	}
	if args[0] == "init" {
		return runGateInit(args[1:], stdout, stderr)
	}
	if args[0] == "primary-edits" {
		// Pre-rename spelling, retiring next release: dispatches to the same
		// allow/revoke code as `gate allow primary` / `gate revoke primary`.
		return runGatePrimaryEdits(args[1:], stdout, stderr)
	}
	if args[0] == "allow" {
		return runGateAllow(args[1:], stdout, stderr)
	}
	if args[0] == "revoke" {
		return runGateRevoke(args[1:], stdout, stderr)
	}
	if args[0] == "selfcheck" {
		// The install-time smoke test swapBinary runs against a candidate
		// before `aphrollo update` replaces the binary with it (#532).
		return runGateSelfCheck(args[1:], stdout, stderr)
	}
	if args[0] == "commitmsg" {
		// The commit-msg git hook: git hands it the message file path.
		return runGateCommitMsg(args[1:], stderr)
	}
	if args[0] == "postcommit" {
		// The post-commit git hook: it writes the gate note on the commit
		// just made, and — opt-in, same key as postmerge — sweeps a lane
		// landed by a conflict resolved and concluded with `git commit`,
		// the one shape post-merge never fires for. It cannot block — the
		// commit is made.
		if gateHelpRequested(args[1:]) {
			fmt.Fprint(stdout, gateUsage)
			return 0
		}
		return runPostCommit(stdout, stderr)
	}
	if args[0] == "postrewrite" {
		// The post-rewrite git hook: records the new commits a rebase or an
		// amend wrote. Reads one "<old> <new> [extra]" line per commit.
		if gateHelpRequested(args[1:]) {
			fmt.Fprint(stdout, gateUsage)
			return 0
		}
		return runPostRewrite(stdin)
	}
	if args[0] == "postmerge" {
		// The post-merge git hook: the opt-in lane sweep, in the repo the
		// merge landed in. It cannot block either — the merge is made. It
		// DOES mutate (removes worktrees, deletes branches), so --help must
		// never reach it.
		if gateHelpRequested(args[1:]) {
			fmt.Fprint(stdout, gateUsage)
			return 0
		}
		return runPostMerge(stdout, stderr)
	}
	if args[0] == "doctor" {
		// Read-only install report: one line per check, exit 1 on any FAIL.
		return runGateDoctor(args[1:], stdout, stderr)
	}
	if args[0] == "statusline" {
		// The statusline: one badge line on stdout, per prompt render. It
		// never blocks and never errors — there is nowhere to report one.
		raw, _ := io.ReadAll(stdin)
		fmt.Fprintln(stdout, tdd.StatusLine(raw))
		return 0
	}
	if args[0] == "stats" {
		// Read-only report over the gate stage lines in the event logs: pipeline health as a number.
		return runGateStats(args[1:], stdout, stderr)
	}
	if args[0] == "output" {
		// Read-only: the TEXT of the run the gate itself last made here —
		// the half `gate stats` cannot answer, and the reason a session no
		// longer has to re-run a suite to read one assertion line.
		return runGateOutput(args[1:], stdout, stderr)
	}
	if args[0] == "status" {
		// Read-only: what an inconclusive gate line points at instead of a
		// rerun — deferred edit jobs, build slots, this checkout's mutation run.
		return runGateStatus(args[1:], stdout, stderr)
	}
	if args[0] == "classify-diff" {
		// Read-only: the class CI's `changes` job sizes the run by.
		return runGateClassifyDiff(args[1:], stdout, stderr)
	}
	if args[0] == "split-commit" {
		// Splits a mixed commit whose tests already pass at HEAD into a
		// test-only commit and the rest: writes by default, --dry previews.
		return runGateSplitCommit(args[1:], stdout, stderr)
	}
	if args[0] == "probe" {
		// The sanctioned route back to HEAD for a refused probe arm:
		// backs the diff up and discards by default, --dry previews.
		return runGateProbe(args[1:], stdout, stderr)
	}
	if args[0] == "gc" {
		// Disk hygiene: reclaims by default, --dry reports.
		return runGateGC(args[1:], stdout, stderr)
	}
	if args[0] == "issue" {
		// The general issue verb: an open point is a row somebody can
		// filter, not a line in a markdown list.
		return runGateIssue(args[1:], stdout, stderr)
	}
	if args[0] == "feedback" {
		// The same, aimed the other way: a defect in the TOOL belongs on the
		// tool's tracker, not in front of maintainers who cannot fix it.
		return runGateFeedback(args[1:], stdout, stderr)
	}
	if args[0] == "escape" {
		// The escape loop: record a red that got past a local green, and
		// refuse a PR that closes one without changing a check.
		return runGateEscape(args[1:], stdout, stderr)
	}
	if args[0] == "runphase" {
		// The detached build/run phase's wrapper: it holds the build slot,
		// logs, and writes the result file the next hook harvests. It never
		// blocks anything, so its exit code is always 0.
		return runPhase(args[1:], stderr)
	}
	if args[0] == "mutants" {
		// The mutation job's own verbs, addressed by a job file.
		return runGateMutants(args[1:], stdout, stderr)
	}
	if args[0] == "cargo" {
		// The cargo-queue shim (task A7): real terminal stdio, not the hook
		// JSON protocol the rest of this switch reads.
		return runGateCargo(args[1:], stdin, stdout, stderr)
	}
	if args[0] == "git" {
		// The git-queue shim (task A11): same shape as cargo above -- real
		// terminal stdio, not the hook JSON protocol.
		return runGateGit(args[1:], stdin, stdout, stderr)
	}
	if args[0] == "lint" {
		// The golangci-lint wrapper: real terminal stdio, same shape as
		// cargo/git above. Local commit gates and CI's self-hosted `lint`
		// job both invoke golangci-lint through this one entry point so a
		// runner-user lint and a debian-user lint contend for the SAME
		// cross-account lock instead of colliding on golangci-lint's own.
		return runGateLint(args[1:], stdin, stdout, stderr)
	}

	// precommit/premerge/prepush are git hooks: no stdin, exit non-zero to
	// block. "premergecommit" is the pre-rename spelling, a silent alias for
	// one release. See gatehooks.go for the routine itself.
	if args[0] == "precommit" || args[0] == "premergecommit" || args[0] == "premerge" || args[0] == "prepush" {
		if gateHelpRequested(args[1:]) {
			fmt.Fprint(stdout, gateUsage)
			return 0
		}
		return runGateMergeHook(args[0], stderr)
	}

	if _, ok := sessionHooks[args[0]]; !ok {
		fmt.Fprintf(stderr, "aphrollo gate: unknown subcommand %q\n\n%s", args[0], gateUsage)
		return 2
	}

	raw, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: reading hook input: %v\n", err)
		return 1
	}
	// Who the hook serves, for every event it appends: set once, undone last.
	defer bindHookActor(raw)()
	defer recordHookTiming(args[0], raw, time.Now())
	// Runs before the timing above: the records the hook queued are written after
	// its answer, and the wait is left out of its time.
	defer shadow.Flush()

	// The one place a switched-off session is handled: every session hook is
	// silent and decides nothing, but the walls that block every author.
	if mode, off := offSession(args[0], raw); off {
		return runWhenOff(mode, raw, stdout)
	}

	switch args[0] {
	case "sessionstart":
		// SessionStart only ever emits advisory context; it never blocks.
		payload, code := tdd.RenderSessionStart(tdd.HandleSessionStart(raw))
		if len(payload) > 0 {
			stdout.Write(payload)
		}
		return code
	case "posttoolusefailure":
		// A shell call that exited non-zero: when it was a suite the agent ran itself,
		// its red is a run like any other. It answers nothing.
		tdd.FoldBashRun(raw)
		return 0
	case "posttooluse":
		return runPostToolUse(raw, stdout)
	case "userpromptsubmit":
		// Handles the /tdd command and the RED reminder; never errors the turn.
		payload, code := tdd.RenderPrompt(tdd.HandlePrompt(raw))
		if len(payload) > 0 {
			stdout.Write(payload)
		}
		return code
	case "sessionend":
		tdd.EndSession(raw)
		return 0
	case "stop", "subagentstop", "taskcompleted":
		return runStopCheck(args[0], raw, stdout, stderr)
	}

	// PreToolUse: the guardrail rules come first, then the gate's own checks;
	// the stronger verdict is rendered with both reasons.
	guard := guardrailDecision(raw)
	tdd.LogEditDecision(raw, guard)
	obs := newPreShadow(raw)
	final := mergeGuardrail(guard, gatePreToolUse(raw, stderr, obs))
	final = obs.redGreenLive(final)
	obs.settle(final)
	payload, code := tdd.RenderPreToolUse(final)
	if len(payload) > 0 {
		stdout.Write(payload)
	}
	// After the answer is written: the shadow record never changes it.
	obs.record()
	return code
}

// gatePreToolUse is the gate's own PreToolUse judgement of one payload,
// without the guardrail rules: the walls, the redundant-suite refusal, the
// shell snapshot, and the edit's smells and laws. It logs what it denies, and
// returns the decision unrendered so runGate can fold the guardrail's in. It
// also tells obs, a collector for the shadow record, what each judgement found;
// obs is only ever written to.
func gatePreToolUse(raw []byte, stderr io.Writer, obs *preShadow) tdd.Decision {
	// The primary-checkout wall is judged first and answers what it resolved, so the
	// shadow record asks nothing of the repository again.
	obs.judgePrimary(raw)
	if d := obs.primary.Decision; d.Action == tdd.Block {
		tdd.LogEditDecision(raw, d)
		return d
	}
	for _, wall := range preToolUseWalls {
		if decision := wall(raw); decision.Action == tdd.Block {
			tdd.LogEditDecision(raw, decision)
			obs.wall(decision)
			return decision
		}
	}

	// A redundant whole-suite invocation (`go test`, `cargo test`, `cargo
	// nextest run`, no narrowing) is judged before the snapshot/diff pair
	// runs at all: PreBash only ever records a snapshot, so nothing else
	// would ever refuse the run itself. Anything that is not a whole-suite
	// invocation, or carries no fresh verdict to be redundant against, falls
	// through to PreBash/DecidePreEdit exactly as before.
	if bashDecision, judged := tdd.DecideBashSuite(raw); judged {
		tdd.LogBashSuiteDecision(raw, bashDecision)
		if bashDecision.Action == tdd.Block {
			obs.add(shadow.Rerun(shadow.Block))
			return bashDecision
		}
	}

	// A Bash call gets a snapshot, not a verdict: what it will write does not
	// exist yet, so the pre-edit half only records the tree for PostToolUse
	// to diff. It never blocks.
	if tdd.IsBashHook(raw) {
		tdd.PreBash(raw)
		return tdd.Decision{}
	}

	decision, err := tdd.DecidePreEdit(raw)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate: %v (allowing)\n", err)
		return tdd.Decision{}
	}
	// The declared laws judge the content this edit WOULD write. A content
	// smell already blocking keeps its own reason; otherwise the more severe
	// verdict wins, so a deny law denies the write before it lands.
	var found []tdd.LawFinding
	if decision.Action != tdd.Block {
		var advisory tdd.Decision
		advisory, found = tdd.RatchetAdvisoryFindings(raw)
		obs.law(found)
		decision = mergeRatchetAdvisory(decision, advisory)
	}
	// When everything above allows the edit, fall through to the worktree
	// advisory: a once-per-session nudge when the edit lands in a main clone
	// rather than a prepared worktree.
	if decision.Action == tdd.Allow {
		decision = tdd.WorktreeAdvisory(raw)
	}
	tdd.LogEditDecision(raw, decision)
	tdd.LogLawGuides(raw, decision, found)
	return decision
}

// mergeRatchetAdvisory folds a ratchet-law verdict r into the edit-time
// decision. A strictly more severe r replaces it outright — a deny law wins
// over a suppression-only warn, as before. But a TIE used to discard r
// entirely (r.Action > decision.Action was the only branch that fired), so a
// warn-severity law hit on the same edit as an already-Warn decision (a
// suppression note, a test-quality note) vanished — its law name, location
// and remedy never reached the model, though the commit-time stage still
// enforced the law (issue #296, the "lossless advisory" contract). A tie
// keeps BOTH: they are two independently true facts about this edit, not a
// choice between them.
func mergeRatchetAdvisory(decision, r tdd.Decision) tdd.Decision {
	switch {
	case r.Action > decision.Action:
		return r
	case r.Action == decision.Action && r.Action != tdd.Allow:
		merged := decision
		switch {
		case merged.Reason == "":
			merged.Reason = r.Reason
		case r.Reason != "":
			merged.Reason = strings.TrimRight(merged.Reason, "\n") + "\n" + r.Reason
		}
		if merged.Policy == "" {
			merged.Policy = r.Policy
		}
		if len(r.Escapes) > 0 {
			merged.Escapes = append(append([]string{}, decision.Escapes...), r.Escapes...)
		}
		return merged
	default:
		return decision
	}
}
