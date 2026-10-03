// Package kernel is the pure decision core of docs/trellis-architecture.md
// (§2): payload → event → Step → one rendered line. It imports the standard
// library and nothing from the box: no os, no os/exec, no clock. Time and every
// fact about the world arrive in the Event; whatever must run in the world
// leaves as an Effect, and what comes of it returns as another event.
//
// This package holds the lane machine (Step), the TDD machine of one unit
// (StepUnit, StepUnits) and the rule table (ruleTable, Decide); each tolerates
// the events the others add. The TDD machine renders decisions, never denials:
// a guide effect only says WouldDeny where the §3 table turns it into a deny
// under tdd = enforce, and the rule table decides. Decide is the one pure call
// that combines the three; nothing here is wired to a hook (the engine, F16,
// does that).
//
// # Where each rule comes from
//
//   - Lifecycle (§3 table): none → open on the first hook in a lane; open →
//     committed on a gated commit; pr on pr.opened; ci_pending, ci_green,
//     ci_red, ci_unavailable as CI reports per declared OS; a new head pushed
//     sends ci_* back to pr; merged on a merge; merged → removed after no actor
//     for 30 minutes with a clean tree and no worktree lock; open or committed
//     with no PR, idle 14 days → abandoned. Each Row in the table names its
//     line.
//   - A merge is a fact (§3, §8 "Escapes"): lane.merged is recorded from every
//     live life, whatever its Source (api, local, ancestry, github), and is
//     never an escape.
//   - A closed lane stays closed (§3): only a lane.opened newer than the close
//     starts the branch again, so a retried or replayed event cannot reopen it.
//   - Idempotence (§3 "Concurrency": results come back as events and a retried
//     transaction is idempotent): every fold is a set or a latest-wins, and
//     effects come only with a change of life, so an event applied twice equals
//     once.
//   - Actors (§3 Actor, §12 F3): "session/agent" strings, last seen per lane.
//     The kernel only counts their activity; it never reads a session id.
//   - Guide, not cage (§1): the machine never refuses anything. Where the
//     document is silent it takes the reading that lets work through: a CI
//     verdict implies a PR, an abandoned lane revives on activity, an event
//     for an unknown lane opens it, an unknown event is ignored.
//
// # Where each TDD rule comes from
//
// Each UnitRow in unitTable names its line; the rules in short:
//
//   - States and columns (§3 "TDD machine"): closed, pending(T), open(T),
//     held against test added or changed, real red, code edit, T green, bogus
//     or not-tested, product escape. A cell the table calls "Stay" has no row.
//   - Tested code (§3, S12): a code edit is allowed in a closed unit only when
//     the adapter reports it covered by a passing test of the unit and adding
//     no exported symbol and no new function; otherwise one guide per unit per
//     lane, marked WouldDeny under enforce. Pending and open allow code edits.
//   - A verdict is a fact about a tree (§1, §3 "Tree keys"): a run result for a
//     tree other than the unit's newest tree moves nothing and is delivered
//     labelled stale (§6 "Caching", #813); a retried job is dropped (§3
//     "Concurrency").
//   - Real and not real (§3 Verdict, §6): green, red and red-missing-impl are
//     real. A bogus red, a timeout, a skip, infra trouble, an unknown value
//     never move a state and never count as green; the cause is named; the
//     last real verdict stands. A deferred run is pending, not untested.
//   - The pair (§3): a pair is recorded only for a new or changed T, on a
//     green after a code change, at a tree other than the red it follows. A
//     green at the red's own tree is a flaky disagreement (§6) and closes
//     nothing. An open red also closes when T is removed or when T passes with
//     no code change, but then records no pair.
//   - Writes nobody parsed (§3): the unit is unproven until the next real run.
//   - Escapes (§8): a CI test red on a head whose note says gated green opens
//     the named T; any other CI red holds nothing. A trunk escape holds the
//     unit, as guidance at every level (C9). A hold is released by a local red
//     of the reproducing test, a CI green of the named test on a later head, or
//     the merge of a lane whose closes-by names it. A CI green of the named
//     test also closes an open unit, so a red that only reproduces on another
//     OS never strands a box.
//   - Seeding (§3 "Lane record", C13): a gated commit sets last_real where
//     there is none.
//   - tdd = off freezes the machine; an unknown mode reads as warn (§7 "No
//     silent misreads").
//
// # Where each rule comes from
//
// Each Rule in ruleTable names its section. The class is the row of the §5
// level table; the default level is enforce for a deny rule and warn for a
// guide rule, unless the config key in brackets moves it.
//
// Block always (RuleWall; Claude only, a human is advised; never shadowed):
//
//   - primary-write (§4 PreToolUse, §5) [isolation]: a Write or Bash write
//     that lands in the main checkout while the lane is trunk.
//   - bypass-gate (§4 Shims): --no-verify, -c core.hooksPath.
//   - trunk-move (§4 Shims): a move off trunk in the primary checkout.
//   - trunk-commit (§4 pre-commit): a non-merge commit on trunk there.
//   - trunk-push (§4 pre-push, C17): a push adding a non-merge commit to trunk.
//   - merge-bypass (§5): a push to trunk or gh pr merge around the merge gate.
//   - merge-no-green (§4 pre-merge-commit): a merge without a green verdict
//     for the merged tree on every declared OS.
//   - discard-work (§5): discarding uncommitted work.
//   - attribution (§4 commit-msg, pre-push) [undercover]: attribution in an
//     undercover repo.
//   - secrets (§5, C14): blocks every author, human included.
//
// Block, earned (RuleEarned; shadowed in 10% of lanes unless pinned):
//
//   - deny-law-edit (§4 PreToolUse), deny-law-commit (§4 pre-commit): a deny
//     law's weight rises at edit, or regresses at commit.
//   - commit-proof, commit-vet (§4 pre-commit): the red→green proof, vet.
//   - bypass-verb (§5): an outward call that does by hand what a verb does.
//   - red-green (§3 TDD machine) [tdd]: untested code under enforce; warn is
//     the default level, so it guides.
//   - stop-red (§4 Stop, SubagentStop) [tdd]: blocks once on an unseen red,
//     under enforce only.
//
// Block only where pinned (RulePinned; never shadowed):
//
//   - mutation (§5, §6) [mutation]: survivors on added lines; off unless the
//     repo opts in, a guide at warn, a block only at enforce.
//
// Guide (RuleGuide; at most additionalContext at any level): warn-law,
// lint, commit-checks (baseline, docs and suppression: §5 names no level),
// long-wait, rerun-suite, noisy-output, not-tested, escape-hold (C9) and
// run-result (bogus, pending, passed at once, flaky, stale).
//
// The §5 "real repo" rule is not here: it is the sealed environment's, in run.
//
// # How Decide decides
//
// The precedence is fixed (see Decide): rows in table order, which is the §4
// PreToolUse order; a row at off does not fire; a deny row only guides at
// warn, for a human, for an event that is a fact, or in the holdout arm (all
// marked WouldDeny, the last also HeldOut); the strongest outcome wins and the
// earlier row wins a tie. Only a question (tool.pre, commit.pre, push.pre,
// merge.pre, stop) can be denied, and a question moves no machine. The arm is
// FNV-1a of the lane id and the rule id, one in ten, so a lane lands in the
// same arm every time. A deny always carries its rule, cause, next step and
// override (§1, §4).
package kernel
