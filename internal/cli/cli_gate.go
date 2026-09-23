package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const gateUsage = `usage: aphrollo gate <subcommand>

Subcommands:
  sessionstart      Inject the build-skill nudge at session start
  pretooluse        Evaluate a Claude Code PreToolUse edit payload from stdin
  posttooluse       Run related tests after an edit and report RED/GREEN
  userpromptsubmit  Handle the /gate command and re-inject a RED reminder
  sessionend        Drop the session's state file
  precommit         Git pre-commit gate: fail-first + mechanical (run in the repo)
  premerge          Git pre-merge-commit gate: mechanical ONLY, no fail-first/anti-cheat
  premergecommit    (alias of premerge; retiring next release)
  allow             allow [primary]: waive a wall for this session (bare: list waivers)
  revoke            revoke [primary]: restore a wall waived by allow (bare: list waivers)
  primary-edits     on|off (alias of allow/revoke primary; retiring next release)
  postcommit        Git post-commit hook: write the refs/notes/gate note on the
                    commit just made — what lets CI tell a red on a gated tip
                    from a red on an ungated one — and, in a repo declaring
                    prune-lanes-on-merge = true, sweep the lanes a conflict
                    resolved by hand and concluded with "git commit" just
                    landed (the shape post-merge never sees). Never blocks,
                    never fails
  postmerge         Git post-merge hook: in a repo declaring
                    prune-lanes-on-merge = true, sweep the lanes this merge
                    landed (the same guarded sweep workspace merge runs,
                    never the worktree the hook fired in). Silent and inert
                    in a repo that did not declare it. Never blocks
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
  stats             Tally gate.log by stage and outcome (--since 7d), and the open
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
  gc                Reclaim stale build dirs: idle incremental caches, dead gate dirs,
                    orphan worktree builds (--repo, --older-than 3d, --apply)
  install           (alias of aphrollo install; retiring next release) Install
                    the git-hook shims into a repo (--repo, --apply)
  init              (alias of aphrollo install; retiring next release) Set up TDD:
                    session hooks in settings.json + the global git gate
                    (--no-git, --uninstall). ALSO EDITS FILES IN A REPO: the managed
                    block in <repo>/CLAUDE.md and <repo>/.ratchet/README.md, where
                    <repo> is --repo (default: the working directory's repo)
  selfcheck         Install-time smoke test: build a marker-less temp tree and require
                    FindProjectRoot to come back empty for it. aphrollo update runs
                    this against the CANDIDATE binary before swapping it in (#532)
  cargo             cargo-queue shim: queue a DIRECT cargo invocation behind the same
                    per-target-dir build slots the hooks/gates use (APHROLLO_CARGO_WAIT_SECS,
                    APHROLLO_BUILD_SLOTS, APHROLLO_REAL_CARGO)
  git               git-queue shim: queue a DIRECT index-mutating git invocation behind a
                    per-repo lock so concurrent sessions sharing one checkout don't collide
                    on .git/index.lock (APHROLLO_GIT_WAIT_SECS, APHROLLO_REAL_GIT)
  lint              lint wrapper: run golangci-lint behind the box-wide, cross-account
                    lint lock (APHROLLO_LINT_WAIT_SECS) so a local commit gate and a
                    CI runner sharing this box never collide on golangci-lint's own
                    lock instead of finding it clean or dirty

Autonomous TDD gates. pretooluse reads the hook JSON on stdin; on a smell in a
test file (real-time sleep, tautological assertion, focused/disabled test) it
exits 2 with a deny envelope, and warns on a suppression; otherwise it is
silent. posttooluse runs the project's related tests after an edit and surfaces
a failure summary (silent unless RED). userpromptsubmit intercepts
/gate [status|off|on|reset] and otherwise re-injects the last RED outcome.
sessionend cleans up the per-session state file. precommit verifies fail-first,
blocks a newly-added suppression, and runs the suite, exiting non-zero to block.
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

// The operator budget knobs live HERE, beside the defaults they override,
// so every env switch this binary reads is declared in one place instead of
// wherever it happens to be used:
//
//	APHROLLO_POSTEDIT_BUDGET_SECS  the edit hook's suite budget (default 100s)
//	APHROLLO_LOCK_WAIT_SECS        the commit gate's build-slot wait (default 1200s)
//
// The edit hook's own build-slot wait is deliberately NOT tunable: it is
// zero by contract (one try, then QUEUED-SKIPPED), because an edit that
// waits spends its whole test budget losing a race to a multi-minute build.
func postEditBudget() time.Duration {
	return tdd.PostEditBudget()
}

func precommitLockWait() time.Duration {
	return envDurationSecs("APHROLLO_LOCK_WAIT_SECS", defaultPrecommitLockWait)
}

// envDurationSecs reads a whole-number-of-seconds env knob. Anything that is
// not one — unset, empty, negative, junk — keeps the shipped default: a
// mistyped budget must never silently become zero and turn every run into an
// instant timeout.
func envDurationSecs(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return def
	}
	return time.Duration(n) * time.Second
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
		// Read-only report over gate.log: pipeline health as a number.
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
	if args[0] == "gc" {
		// Disk hygiene: dry-run by default, --apply reclaims.
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

	switch args[0] {
	case "sessionstart", "pretooluse", "posttooluse", "userpromptsubmit", "sessionend":
	default:
		fmt.Fprintf(stderr, "aphrollo gate: unknown subcommand %q\n\n%s", args[0], gateUsage)
		return 2
	}

	raw, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: reading hook input: %v\n", err)
		return 1
	}

	switch args[0] {
	case "sessionstart":
		// SessionStart only ever emits advisory context; it never blocks.
		payload, code := tdd.RenderSessionStart(tdd.HandleSessionStart(raw))
		if len(payload) > 0 {
			stdout.Write(payload)
		}
		return code
	case "posttooluse":
		// PostToolUse never blocks: it only ever emits advisory context. It is
		// also the one hook allowed to leave work running past its budget — a
		// cold Bevy build does not fit in 110s and killing it establishes
		// nothing.
		tdd.EnableDeferredPhases(true)
		if tdd.IsBashHook(raw) {
			payload, code := tdd.RenderPostToolUse(tdd.PostBash(raw, tdd.RunSuite(postEditBudget())))
			if len(payload) > 0 {
				stdout.Write(payload)
			}
			return code
		}
		payload, code := tdd.RenderPostToolUse(tdd.PostEdit(raw, tdd.RunSuite(postEditBudget())))
		if len(payload) > 0 {
			stdout.Write(payload)
		}
		return code
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
	}

	// The primary checkout is merge-only, and that is decided before anything
	// reads the content: WHERE a write lands does not depend on what it says,
	// and it covers the shell too, which no content gate can judge.
	if decision := tdd.PrimaryCheckoutDecision(raw); decision.Action == tdd.Block {
		tdd.LogEditDecision(raw, decision)
		payload, code := tdd.RenderPreToolUse(decision)
		if len(payload) > 0 {
			stdout.Write(payload)
		}
		return code
	}

	// The operator's discard-wall directive is a blanket, no-override ban on
	// a handful of git verbs in ANY Bash/PowerShell call — judged on the same
	// footing as the primary-checkout wall above, before anything narrower
	// runs. This replaces the ad hoc `grep -P` hook that used to scan the raw
	// command TEXT and could not tell a real invocation from the same words
	// sitting inside a quoted argument (issue #725's class of bug).
	if decision := tdd.DiscardBashDecision(raw); decision.Action == tdd.Block {
		tdd.LogEditDecision(raw, decision)
		payload, code := tdd.RenderPreToolUse(decision)
		if len(payload) > 0 {
			stdout.Write(payload)
		}
		return code
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
			payload, code := tdd.RenderPreToolUse(bashDecision)
			if len(payload) > 0 {
				stdout.Write(payload)
			}
			return code
		}
	}

	// A Bash call gets a snapshot, not a verdict: what it will write does not
	// exist yet, so the pre-edit half only records the tree for PostToolUse
	// to diff. It never blocks.
	if tdd.IsBashHook(raw) {
		tdd.PreBash(raw)
		return 0
	}

	decision, err := tdd.DecidePreEdit(raw)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate: %v (allowing)\n", err)
		return 0
	}
	// The declared laws judge the content this edit WOULD write. A content
	// smell already blocking keeps its own reason; otherwise the more severe
	// verdict wins, so a deny law denies the write before it lands.
	if decision.Action != tdd.Block {
		decision = mergeRatchetAdvisory(decision, tdd.RatchetAdvisory(raw))
	}
	// When everything above allows the edit, fall through to the worktree
	// advisory: a once-per-session nudge when the edit lands in a main clone
	// rather than a prepared worktree.
	if decision.Action == tdd.Allow {
		decision = tdd.WorktreeAdvisory(raw)
	}
	tdd.LogEditDecision(raw, decision)
	payload, code := tdd.RenderPreToolUse(decision)
	if len(payload) > 0 {
		stdout.Write(payload)
	}
	return code
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

// runGateInstall writes the git-hook shims into a single repo. tdd/refactor
// mutations kept the older dry-run-by-default + --apply model, so this defaults
// to a dry-run and requires --apply; only the workspace verbs inverted to
// execute-by-default with --dry.
func runGateInstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		repo  = fs.String("repo", ".", "repository to install the hooks into")
		bin   = fs.String("bin", "", "aphrollo binary the repo's own git-hook shims invoke (default: this executable)")
		apply = fs.Bool("apply", false, "write the hooks (default: print the plan and stop)")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root := tdd.RepoRoot(*repo)
	if root == "" {
		fmt.Fprintf(stderr, "aphrollo: %s is not inside a git repository\n", *repo)
		return 1
	}
	binPath := *bin
	if binPath == "" {
		binPath = defaultBinPath()
	}
	plan, err := tdd.BuildInstallPlan(root, binPath)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, plan.Render(*apply))
	if !*apply {
		return 0
	}
	if err := plan.Apply(); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}
