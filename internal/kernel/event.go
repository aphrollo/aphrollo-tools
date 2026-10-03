package kernel

import "time"

// Kind names an event the way the event log does (architecture §3, "Events").
// The lane machine reads the kinds below; any other kind (another machine's,
// or one a newer binary wrote) is tolerated: it counts as its actor's activity
// and moves nothing.
type Kind string

const (
	KindLaneOpened  Kind = "lane.opened"
	KindLaneEntered Kind = "lane.entered"
	KindLaneLeft    Kind = "lane.left"
	KindLaneMerged  Kind = "lane.merged"
	KindLaneRemoved Kind = "lane.removed"
	KindEdit        Kind = "edit"
	KindCommitGated Kind = "commit.gated"
	KindPROpened    Kind = "pr.opened"
	KindCIVerdict   Kind = "ci.verdict"

	// KindPush is a push that carries a head to the remote (§3: "A new head is
	// pushed"). It is a trigger of the lane table, not a name in the §3 kind list.
	KindPush Kind = "push"

	// KindLaneTick carries the facts only the world knows (is the tree clean,
	// does Claude Code hold a worktree lock) together with the time, so the
	// kernel can judge idleness without reading a clock. Adapters send it at the
	// hooks that already run (session start, prompt submit, post-merge, stats).
	KindLaneTick Kind = "lane.tick"

	// The TDD machine's kinds (§3 "Events"). KindEdit, KindCommitGated,
	// KindCIVerdict and KindLaneMerged are shared with the lane machine.
	KindRunRequested Kind = "run.requested"
	KindRunResult    Kind = "run.result"
	// KindEscape carries a trunk product escape (§8): a revert, a regression
	// issue, a red trunk CI run or `escape record`. A CI test red on a gated
	// head arrives as the ci.verdict it is; other escape classes hold nothing
	// and need not reach the machine.
	KindEscape Kind = "escape"

	// The rule table's questions (§4): an adapter asks the kernel whether a
	// call, a commit, a push, a merge or a stop may go ahead. A question is
	// not a fact: it moves no machine, and only a question can be denied.
	KindPreTool   Kind = "tool.pre"
	KindPreCommit Kind = "commit.pre"
	KindPrePush   Kind = "push.pre"
	KindPreMerge  Kind = "merge.pre"
	KindStop      Kind = "stop"
)

// question reports whether the kind asks for a decision rather than states a fact.
func (k Kind) question() bool {
	switch k {
	case KindPreTool, KindPreCommit, KindPrePush, KindPreMerge, KindStop:
		return true
	}
	return false
}

// FileClass is what the adapter's parser made of an edited file. The zero
// value means nobody parsed the write (§3 "Writes nobody parsed").
type FileClass string

const (
	ClassCode  FileClass = "code"
	ClassTest  FileClass = "test"
	ClassOther FileClass = "other" // docs, config: moves no TDD state, asks for no run
)

// Verdict is one run's result for one tree (§3 Verdict, §6). Only green and
// the two reds are real; a bogus red and anything else (including a value a
// newer binary wrote) is a run that did not test.
type Verdict string

const (
	VerdictGreen          Verdict = "green"
	VerdictRed            Verdict = "red"
	VerdictRedMissingImpl Verdict = "red-missing-impl"
	VerdictRedBogus       Verdict = "red-bogus"
	VerdictNotTested      Verdict = "not-tested"
)

// Causes of a not-tested run (§6 "Tiers"). Deferred is pending, not untested.
const (
	CauseTimeout       = "timeout"
	CauseSkipped       = "skipped"
	CauseQueuedSkipped = "queued-skipped"
	CauseDeferred      = "deferred"
	CauseInfra         = "infra"
)

// TDDMode is the `tdd` key (§7): warn is the default and off freezes the
// machine. An empty or unknown value reads as warn, never as off.
type TDDMode string

const (
	ModeWarn    TDDMode = "warn"
	ModeEnforce TDDMode = "enforce"
	ModeOff     TDDMode = "off"
)

// What went red in CI (§8 "Escapes"): only a test red on a gated head opens a
// unit; every other failure is a disagreement that holds nothing.
const (
	FailureTest     = "test"
	FailureMutation = "mutation"
	FailureCanary   = "canary"
	FailureTimeout  = "timeout"
	FailureFlaky    = "flaky"
)

// StageTrunk marks a KindEscape that holds a unit (§8 "On trunk").
const StageTrunk = "trunk"

// Conclusion is one OS's CI result for the lane's pushed head (§3, §8).
type Conclusion string

const (
	CIStarted     Conclusion = "started"
	CIGreen       Conclusion = "green"
	CIRed         Conclusion = "red"
	CIUnavailable Conclusion = "unavailable"
)

// Merge sources a lane.merged event may name (§3 table, §8 "Escapes"). A
// merge is never an escape whatever its source: the lane machine has no
// escape effect at all.
const (
	SourceAPI      = "api"
	SourceLocal    = "local"
	SourceAncestry = "ancestry"
	SourceGitHub   = "github"
)

// Event is one fact that reached the kernel. The adapter that built it owns
// every read of the world (clock, git, config); the kernel sees only these
// fields. Later machines (TDD, rules) add their fields and kinds here.
type Event struct {
	Kind  Kind
	Lane  string    // branch; "" when the adapter could not name it
	Actor string    // "session_id/agent_id"; "" for CI, git hooks without a session
	At    time.Time // the event's own time: Step never reads a clock

	Worktree, Base string // lane.opened
	Head           string // commit.gated, push, pr.opened, ci.verdict: the head the fact is about
	Source         string // lane.merged
	OS             string // ci.verdict
	Conclusion     Conclusion
	Required       []string // pr.opened: the OSes the repo declares in ci.os
	TreeClean      bool     // lane.tick
	Locked         bool     // lane.tick: Claude Code holds a worktree lock

	// The TDD machine's facts (§3 "TDD machine"). Unit is the unit the event is
	// about; an event with none moves no unit, except commit.gated, which
	// seeds every unit. Mode is the repo's `tdd` key as the adapter read it.
	Unit string
	Mode TDDMode
	// Tree is the worktree key the fact is about (edit: the key after the
	// write; run.requested, run.result: the key measured). A verdict is a fact
	// about a tree (§1).
	Tree string
	Job  string // run.requested, run.result: the job id, so a retry is dropped

	File       FileClass // edit
	Test       string    // edit: the test added, changed or removed; run.result, ci.verdict, escape: the test named
	Removed    bool      // edit: the test is gone
	Covered    bool      // edit: every changed line lies in a function a passing test of the unit executes (§3 "Tested code")
	AddsSymbol bool      // edit: adds an exported symbol or a new function

	Verdict Verdict // run.result
	Cause   string  // run.result: why a run did not test, or why a build broke

	Gated    bool     // ci.verdict: the head's note says gated green
	Failure  string   // ci.verdict: what went red (Failure* constants)
	Stage    string   // escape
	EscapeID string   // escape: the id a hold is released by
	Closes   []string // lane.merged: the escapes the merged lane's closes-by names

	// The rule table's facts (§4, §5), read by the adapter from the payload and
	// the box. Claude is CLAUDECODE=1: a human at a terminal is advised, never
	// blocked. A fact left at its zero value reads as "no damage".
	Claude      bool
	Tool        Tool      // tool.pre
	Target      PathClass // where the write or command lands
	Cmds        Cmd       // tool.pre: what the Bash or PowerShell command does
	NonMerge    bool      // commit.pre, push.pre: a non-merge commit (pushed: one on the first-parent chain)
	LawHit      string    // tool.pre, commit.pre: the law whose weight rose or regressed
	LawDeny     bool      // LawHit is a deny law, not a warn law
	Secret      bool      // tool.pre, commit.pre: a secret in the write or commit
	Attribution bool      // commit.pre, push.pre: attribution in the message or ref
	Failed      Check     // commit.pre: the stage that failed
	Survivors   int       // commit.pre, merge.pre: mutation survivors and not-covered mutants on added lines
	GreenOS     []string  // merge.pre: the OSes with a green verdict for exactly the merged tree
	UnseenRed   bool      // stop: this actor has not seen an outstanding red
	StopActive  bool      // stop: stop_hook_active
}

// EffectKind names work the engine runs after the lock is released. The
// kernel never runs it; whatever comes of it returns as an event.
type EffectKind string

const (
	EffectInstallDeps    EffectKind = "install-deps"
	EffectWarmBuild      EffectKind = "warm-build"
	EffectRemoveWorktree EffectKind = "remove-worktree"
	// EffectLaneNews asks render for one line of lane news; Detail is the life
	// the lane just entered.
	EffectLaneNews EffectKind = "lane-news"

	// EffectRequestRun asks the engine for the unit's run at Tree (§4
	// PostToolUse); the governor coalesces requests per lane and unit.
	EffectRequestRun EffectKind = "request-run"
	// EffectGuide asks render for one line; Detail is a Guide* code.
	EffectGuide EffectKind = "guide"
)

// Effect is one request out of a transition.
type Effect struct {
	Kind   EffectKind
	Lane   string
	Detail string

	Unit  string // the TDD machine's effects name their unit
	Test  string // guide: the unit's T
	Tree  string
	Cause string // guide: the named cause of a run that did not test
	// WouldDeny marks a guide the §3 table turns into a deny under
	// tdd = enforce. The kernel never denies: the rule table (F15) decides.
	WouldDeny bool
}
