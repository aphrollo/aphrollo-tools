package kernel

import (
	"slices"
	"time"
)

// Guard narrows a row beyond its From and Kind. It sees the state before the
// event (old), the state after the event's facts were folded in (next) and the
// event.
type Guard func(old, next State, e Event) bool

// Row is one transition: in any From life, an event of Kind whose guard holds
// moves the lane to To and asks for Fx. The first matching row wins. Rule
// names where the row comes from.
type Row struct {
	Rule string
	From []Life
	Kind Kind
	When Guard // nil: always
	To   Life
	Fx   []EffectKind
}

var (
	ciLives = []Life{LifeCIPending, LifeCIGreen, LifeCIRed, LifeCIUnavailable}
	// beforeMerge is every life a merge can still be recorded from.
	beforeMerge = slices.Concat([]Life{LifeOpen, LifeCommitted, LifeAbandoned, LifePR}, ciLives)
	deps        = []EffectKind{EffectInstallDeps, EffectWarmBuild}
	news        = []EffectKind{EffectLaneNews}
	// startsLane are the events that prove an actor is working in a lane.
	startsLane = []Kind{KindLaneOpened, KindLaneEntered, KindEdit}
)

// table is the lane machine: data, read only. Dependency install and warm-up
// start with the lane (§3, §6 "Lane warm-up"); a lane-news line goes out when
// CI concludes and when the lane is merged ("lane X closed", §4 post-merge).
var table = slices.Concat(
	each(startsLane, Row{Rule: "§3 lifecycle: first hook in a lane → open", From: []Life{LifeNone}, To: LifeOpen, Fx: deps}),
	each(startsLane, Row{Rule: "§3 lifecycle: an abandoned lane's actor is back → open", From: []Life{LifeAbandoned}, To: LifeOpen}),
	[]Row{
		{Rule: "§3 lifecycle: only a newer lane.opened starts a closed branch again", From: []Life{LifeMerged, LifeRemoved},
			Kind: KindLaneOpened, When: reopens, To: LifeOpen, Fx: deps},
		{Rule: "§3 lifecycle: open + gated commit → committed", From: []Life{LifeOpen, LifeAbandoned},
			Kind: KindCommitGated, To: LifeCommitted},
		{Rule: "§3 lifecycle: committed + pr.opened → pr", From: []Life{LifeOpen, LifeCommitted, LifeAbandoned},
			Kind: KindPROpened, To: LifePR},
		{Rule: "§3 lifecycle: ci_* + pr.opened on a new head → pr", From: ciLives,
			Kind: KindPROpened, When: headMoved, To: LifePR},
		{Rule: "§3 lifecycle: ci_* + a new head pushed → pr", From: ciLives,
			Kind: KindPush, When: headMoved, To: LifePR},
		{Rule: "§3 lifecycle: CI started or still waiting on a declared OS → ci_pending", From: beforeMerge,
			Kind: KindCIVerdict, When: ciIs(LifeCIPending), To: LifeCIPending},
		{Rule: "§3 lifecycle: CI green on every declared OS → ci_green", From: beforeMerge,
			Kind: KindCIVerdict, When: ciIs(LifeCIGreen), To: LifeCIGreen, Fx: news},
		{Rule: "§3 lifecycle: CI red on any OS → ci_red", From: beforeMerge,
			Kind: KindCIVerdict, When: ciIs(LifeCIRed), To: LifeCIRed, Fx: news},
		{Rule: "§3 lifecycle, §8: CI never started on a declared OS → ci_unavailable (not failed)", From: beforeMerge,
			Kind: KindCIVerdict, When: ciIs(LifeCIUnavailable), To: LifeCIUnavailable, Fx: news},
		{Rule: "§3 lifecycle, §8 escapes: a merge by any source is a fact → merged", From: slices.Concat([]Life{LifeNone}, beforeMerge),
			Kind: KindLaneMerged, To: LifeMerged, Fx: news},
		{Rule: "§3 lifecycle: merged, no actor for 30 min, clean tree, no worktree lock → removed",
			From: []Life{LifeMerged}, Kind: KindLaneTick, When: settled, To: LifeRemoved, Fx: []EffectKind{EffectRemoveWorktree}},
		{Rule: "§3 lifecycle: open or committed, no PR, idle for 14 days → abandoned", From: []Life{LifeOpen, LifeCommitted},
			Kind: KindLaneTick, When: idleSince(abandonAfter), To: LifeAbandoned},
		{Rule: "§3 lifecycle: the worktree is gone (removed by a verb or by hand) → removed", From: slices.Concat(beforeMerge, []Life{LifeMerged}),
			Kind: KindLaneRemoved, To: LifeRemoved},
	},
)

// each stamps the row once per kind, suffixing its rule with the kind.
func each(kinds []Kind, r Row) []Row {
	rows := make([]Row, 0, len(kinds))
	for _, k := range kinds {
		row := r
		row.Kind, row.Rule = k, r.Rule+" ["+string(k)+"]"
		rows = append(rows, row)
	}
	return rows
}

// reopens holds for a lane.opened newer than the lane's close. A lane.opened
// delivered again after the merge is older, so it cannot undo the merge.
func reopens(old, _ State, e Event) bool { return old.Life.closed() && e.At.After(old.ClosedAt) }

// headMoved holds when the event moved the PR's head: its CI results were reset.
func headMoved(old, next State, _ Event) bool { return old.PRHead != next.PRHead }

// ciIs holds when the lane's CI results, with this event folded in, add up to l.
func ciIs(l Life) Guard {
	return func(_, next State, _ Event) bool { return next.ciOutcome() == l }
}

// settled holds when a merged lane has seen no actor for settleAfter and the
// tick reports a clean tree that Claude Code holds no lock on.
func settled(old, next State, e Event) bool {
	return e.TreeClean && !e.Locked && idleSince(settleAfter)(old, next, e)
}

// idleSince holds when the tick comes d or more after the last actor activity.
func idleSince(d time.Duration) Guard {
	return func(old, _ State, e Event) bool { return e.At.Sub(old.LastActive) >= d }
}
