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
)

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
)

// Effect is one request out of a transition.
type Effect struct {
	Kind   EffectKind
	Lane   string
	Detail string
}
