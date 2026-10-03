package kernel

import "slices"

// Readers of the facts, shared by the rows below.

func ask(k Kind, ok func(Facts) bool) func(Facts) (string, bool) {
	return func(f Facts) (string, bool) { return "", f.Event.Kind == k && ok(f) }
}

// runs is a tool.pre whose command does any of cmds.
func runs(cmds Cmd) func(Facts) (string, bool) {
	return ask(KindPreTool, func(f Facts) bool { return f.Event.Cmds.has(cmds) })
}

// lawOf reads a law hit of the given severity at the given question.
func lawOf(k Kind, deny bool) func(Facts) (string, bool) {
	return func(f Facts) (string, bool) {
		e := f.Event
		return e.LawHit, e.Kind == k && e.LawHit != "" && e.LawDeny == deny
	}
}

func failed(checks ...Check) func(Facts) (string, bool) {
	return ask(KindPreCommit, func(f Facts) bool { return slices.Contains(checks, f.Event.Failed) })
}

func guidedBy(codes ...string) func(Facts) (string, bool) {
	return func(f Facts) (string, bool) { return "", f.guided(codes...) }
}

func onTrunkPrimary(f Facts) bool { return f.laneID() == TrunkLane && f.Event.Target == PathPrimary }

// tddLevel is red-green's level: the tdd mode (§7), whose default is warn.
func tddLevel(c Config) Level {
	switch c.tddMode() {
	case ModeEnforce:
		return LevelEnforce
	case ModeOff:
		return LevelOff
	}
	return LevelWarn
}

// stopLevel blocks at Stop only under tdd = enforce (§4 Stop).
func stopLevel(c Config) Level {
	if c.tddMode() == ModeEnforce {
		return LevelEnforce
	}
	return LevelOff
}

func enforceIf(on bool) Level {
	if on {
		return LevelEnforce
	}
	return LevelOff
}

// mutationLevel is the `mutation` key: off unless a repo opts in, and a value
// that is no level reads as off, the opt-in's own default (§6 "Mutation").
func mutationLevel(c Config) Level {
	if c.Mutation.valid() {
		return c.Mutation
	}
	return LevelOff
}

// ruleTable is the rule table: data, read only (§5). Its order is the
// precedence (see Decide): walls, earned blocks, the pinned block, guides.
// Each row's Section is the line of the document it comes from.
var ruleTable = []Rule{
	// Block always: real damage by Claude, never demoted (§5 "Block always").
	{ID: "primary-write", Section: "§4 PreToolUse, §5 walls", Class: RuleWall, Do: OutcomeDeny,
		Cause: "the write lands in the main checkout on trunk", Next: "EnterWorktree name=<lane>; the write is refused until the worktree is entered",
		Override: "trellis allow primary-write --once",
		Level:    func(c Config) Level { return enforceIf(!c.NoIsolation) },
		Reads: ask(KindPreTool, func(f Facts) bool {
			return (f.Event.Tool == ToolWrite || f.Event.Cmds.has(CmdWrite)) && onTrunkPrimary(f)
		})},
	{ID: "bypass-gate", Section: "§4 Shims, §5 walls", Class: RuleWall, Do: OutcomeDeny,
		Cause: "the command skips the gate (--no-verify or -c core.hooksPath)", Next: "run the git command without the flag and fix what the gate names",
		Override: "trellis allow bypass-gate --once", Reads: runs(CmdBypassGate)},
	{ID: "trunk-move", Section: "§4 Shims, §5 walls", Class: RuleWall, Do: OutcomeDeny,
		Cause: "the command moves the primary checkout off trunk", Next: "EnterWorktree name=<lane> instead of switching branches here",
		Override: "trellis allow trunk-move --once",
		Reads:    ask(KindPreTool, func(f Facts) bool { return f.Event.Cmds.has(CmdMoveOffTrunk) && onTrunkPrimary(f) })},
	{ID: "trunk-commit", Section: "§4 pre-commit, §5 walls", Class: RuleWall, Do: OutcomeDeny,
		Cause: "a non-merge commit on trunk in the primary checkout", Next: "commit in a lane (EnterWorktree name=<lane>)",
		Override: "trellis allow trunk-commit --once",
		Reads:    ask(KindPreCommit, func(f Facts) bool { return f.Event.NonMerge && onTrunkPrimary(f) })},
	{ID: "trunk-push", Section: "§4 pre-push, §5 walls (C17)", Class: RuleWall, Do: OutcomeDeny,
		Cause: "the push adds a non-merge commit to trunk's first-parent chain", Next: "ship the lane through the merge verb: trellis merge <lane>",
		Override: "trellis allow trunk-push --once",
		Reads:    ask(KindPrePush, func(f Facts) bool { return f.Event.NonMerge && f.Event.Cmds.has(CmdPushTrunk) })},
	{ID: "merge-bypass", Section: "§5 walls", Class: RuleWall, Do: OutcomeDeny,
		Cause: "a push to trunk or gh pr merge goes around the merge gate", Next: "trellis merge <lane>",
		Override: "trellis allow merge-bypass --once", Reads: runs(CmdPushTrunk | CmdMergePR)},
	{ID: "merge-no-green", Section: "§4 pre-merge-commit, §5 walls", Class: RuleWall, Do: OutcomeDeny,
		Cause: "no green verdict for the merged tree on every declared OS", Next: "wait for CI (trellis ci wait <lane> --merge) or run trellis ci",
		Override: "trellis allow merge-no-green --once",
		Reads: func(f Facts) (string, bool) {
			os := missingOS(f)
			return os, f.Event.Kind == KindPreMerge && os != ""
		}},
	{ID: "discard-work", Section: "§5 walls", Class: RuleWall, Do: OutcomeDeny,
		Cause: "the command discards uncommitted work", Next: "commit or stash it first",
		Override: "trellis allow discard-work --once", Reads: runs(CmdDiscard)},
	{ID: "attribution", Section: "§4 commit-msg and pre-push, §5 walls", Class: RuleWall, Do: OutcomeDeny,
		Cause: "attribution in an undercover repo", Next: "remove the trailer or footer from the message",
		Override: "trellis allow attribution --once",
		Level:    func(c Config) Level { return enforceIf(c.Undercover) },
		Reads:    func(f Facts) (string, bool) { return "", f.Event.Attribution }},
	{ID: "secrets", Section: "§5 walls (C14), Appendix C6", Class: RuleWall, Do: OutcomeDeny, AllAuthors: true,
		Cause: "a secret in the write or commit", Next: "remove it and read it from the environment",
		Override: "an escape comment on the line, with the reason, or a fixture path in the law's scope",
		Reads:    func(f Facts) (string, bool) { return "", f.Event.Secret }},

	// Block, earned: shadowed and demotable (§5 "Block, earned").
	{ID: "deny-law-edit", Section: "§4 PreToolUse, §5 earned", Class: RuleEarned, Do: OutcomeDeny,
		Cause: "the edit raises the weight of a deny law", Next: "fix the finding the law names",
		Override: "the law's escape comment, with the reason, or trellis allow deny-law-edit --once", Reads: lawOf(KindPreTool, true)},
	{ID: "deny-law-commit", Section: "§4 pre-commit, §5 earned", Class: RuleEarned, Do: OutcomeDeny,
		Cause: "a deny law regresses", Next: "fix the finding the law names",
		Override: "the law's escape comment, with the reason, or trellis allow deny-law-commit --once", Reads: lawOf(KindPreCommit, true)},
	{ID: "commit-proof", Section: "§4 pre-commit, §5 earned", Class: RuleEarned, Do: OutcomeDeny,
		Cause: "the red→green proof is missing or failed", Next: "write the test red first, then the change; the run proves the pair",
		Override: "trellis allow commit-proof --once", Reads: failed(CheckProof)},
	{ID: "commit-vet", Section: "§4 pre-commit, §5 earned", Class: RuleEarned, Do: OutcomeDeny,
		Cause: "vet failed", Next: "fix what vet names",
		Override: "trellis allow commit-vet --once", Reads: failed(CheckVet)},
	{ID: "bypass-verb", Section: "§5 earned", Class: RuleEarned, Do: OutcomeDeny,
		Cause: "an outward call does by hand what a verb does", Next: "use the verb (trellis pr, trellis merge)",
		Override: "trellis allow bypass-verb --once", Reads: runs(CmdOutward)},
	{ID: "red-green", Section: "§3 TDD machine, §5 earned (tdd = enforce)", Class: RuleEarned, Do: OutcomeDeny,
		Cause: "a code edit that is not tested code, with no open red", Next: "write the failing test first, then edit the code",
		Override: "trellis allow red-green --once, or tdd = warn", Level: tddLevel, Reads: guidedBy(GuideUntestedCode)},
	{ID: "stop-red", Section: "§4 Stop and SubagentStop, §5 earned (tdd = enforce)", Class: RuleEarned, Do: OutcomeDeny,
		Cause: "a red this actor has not seen is outstanding", Next: "read the red and fix it, then stop again",
		Override: "stop again: it blocks once (stop_hook_active)", Level: stopLevel,
		Reads: func(f Facts) (string, bool) {
			unit, open := firstOpen(f.Units)
			return unit, f.Event.Kind == KindStop && !f.Event.StopActive && f.Event.UnseenRed && open
		}},

	// Block only where a repo pins block (§5, §6 "Mutation").
	{ID: "mutation", Section: "§5 earned (pinned), §6 Mutation", Class: RulePinned, Do: OutcomeDeny,
		Cause: "mutation survivors or not-covered mutants on added lines", Next: "add the test that kills the survivor",
		Override: "[rules] mutation = \"guide\" in trellis.toml, or the accept-list", Level: mutationLevel,
		Reads: func(f Facts) (string, bool) {
			return "", (f.Event.Kind == KindPreCommit || f.Event.Kind == KindPreMerge) && f.Event.Survivors > 0
		}},

	// Guide: additionalContext, never a deny (§5 "Guide").
	{ID: "warn-law", Section: "§4 PostToolUse, §5 guide", Class: RuleGuide, Do: OutcomeGuide,
		Cause: "an edit or commit trips a warn law", Next: "fix the finding the law names",
		Reads: func(f Facts) (string, bool) {
			if d, ok := lawOf(KindPreTool, false)(f); ok {
				return d, true
			}
			return lawOf(KindPreCommit, false)(f)
		}},
	{ID: "lint", Section: "§4 pre-commit, §5 guide", Class: RuleGuide, Do: OutcomeGuide,
		Cause: "lint failed", Next: "fix what lint names", Reads: failed(CheckLint)},
	{ID: "commit-checks", Section: "§4 pre-commit; §5 names no level (open point)", Class: RuleGuide, Do: OutcomeGuide,
		Cause: "the baseline guard, docs check or suppression check failed", Next: "fix what the check names",
		Reads: failed(CheckBaseline, CheckDocs, CheckSuppress)},
	{ID: "long-wait", Section: "§5 guide", Class: RuleGuide, Do: OutcomeGuide,
		Cause: "a long foreground wait", Next: "run it in the background and read the result at the next hook", Reads: runs(CmdLongWait)},
	{ID: "rerun-suite", Section: "§5 guide", Class: RuleGuide, Do: OutcomeGuide,
		Cause: "the run already covered this suite", Next: "read the verdict (trellis output <run>)", Reads: runs(CmdRerun)},
	{ID: "noisy-output", Section: "§5 guide", Class: RuleGuide, Do: OutcomeGuide,
		Cause: "the command prints more than it needs to", Next: "narrow it or pipe it to a file", Reads: runs(CmdNoisy)},
	{ID: "not-tested", Section: "§5 guide, §6 Tiers", Class: RuleGuide, Do: OutcomeGuide,
		Cause: "the run did not test; the cause is named and the last real verdict stands", Next: "fix the setup or retry the run",
		Reads: guidedBy(GuideNotTested)},
	{ID: "escape-hold", Section: "§3 TDD machine (C9), §5 guide", Class: RuleGuide, Do: OutcomeGuide,
		Cause: "a trunk escape holds this unit", Next: "reproduce it with a failing test, which releases the hold",
		Reads: guidedBy(GuideHeld)},
	{ID: "run-result", Section: "§5 guide, §6 Caching and Tiers", Class: RuleGuide, Do: OutcomeGuide,
		Cause: "a run that is not a red: bogus, pending, passed at once, flaky or stale", Next: "read the line; the cause is named",
		Reads: guidedBy(GuideRedBogus, GuidePending, GuidePassedAtOnce, GuideFlaky, GuideStale)},
}
