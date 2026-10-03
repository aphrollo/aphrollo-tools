package kernel

import (
	"maps"
	"slices"
	"time"
)

// Life is where a lane stands (§3 lifecycle table). The zero value reads as
// LifeNone.
type Life string

const (
	LifeNone          Life = "none"
	LifeOpen          Life = "open"
	LifeCommitted     Life = "committed"
	LifePR            Life = "pr"
	LifeCIPending     Life = "ci_pending"
	LifeCIGreen       Life = "ci_green"
	LifeCIRed         Life = "ci_red"
	LifeCIUnavailable Life = "ci_unavailable"
	LifeMerged        Life = "merged"
	LifeRemoved       Life = "removed"
	LifeAbandoned     Life = "abandoned"
)

const (
	// settleAfter is how long a merged lane must see no actor before it is
	// removed (§3: "No actor for 30 min").
	settleAfter = 30 * time.Minute
	// abandonAfter is the idleness after which an open or committed lane is
	// listed as abandoned (§3: "idle for 14 days").
	abandonAfter = 14 * 24 * time.Hour
)

func (l Life) closed() bool { return l == LifeMerged || l == LifeRemoved }

// State is one lane's record (§3 "Lane record"), restricted to what the lane
// machine decides on. The TDD machine and the rule table add their fields.
// Step returns a new State and never writes into the maps of its argument.
type State struct {
	Branch   string
	Worktree string
	Base     string // recorded once, at lane.opened (C5)
	Head     string // the lane's newest known commit
	PRHead   string // the head the PR and its CI results are about
	Life     Life

	// Actors is the session registry: "session/agent" to the time it was last
	// seen on this lane (§3, §12 F3: agents are keyed by agent_id).
	Actors     map[string]time.Time
	LastActive time.Time // the newest actor activity; idleness is measured from it
	ClosedAt   time.Time // the newest lane.merged or lane.removed
	MergedBy   string    // the Source of the merge that closed the lane

	CI         map[string]Conclusion // OS to its result for PRHead
	CIRequired []string              // the OSes the repo declares, sorted; empty reads as "every OS heard from"
}

// Step is the lane machine: pure and total. It reads no clock, file or
// environment, and holds no state between calls. Folding the event into the
// state's facts comes first (actor activity, heads, CI results, merge source),
// then the table decides whether the life moves. Effects come out only when
// the life moves, so an event applied twice says nothing the second time.
func Step(s State, e Event) (State, []Effect) {
	next, fx, _ := advance(s, e)
	return next, fx
}

// advance is Step plus the index of the table row that decided, -1 when none did.
func advance(s State, e Event) (State, []Effect, int) {
	if s.Life == "" {
		s.Life = LifeNone
	}
	if s.Branch != "" && e.Lane != "" && e.Lane != s.Branch {
		return s, nil, -1
	}
	next := absorb(s, e)
	i := match(s, next, e)
	if i < 0 {
		if s.Life == LifeNone {
			return s, nil, -1
		}
		return next, nil, -1
	}
	row := table[i]
	next.Life = row.To
	if row.To == s.Life {
		return next, nil, i
	}
	var fx []Effect
	for _, k := range row.Fx {
		f := Effect{Kind: k, Lane: next.Branch}
		if k == EffectLaneNews {
			f.Detail = string(row.To)
		}
		fx = append(fx, f)
	}
	return next, fx, i
}

// match returns the first row for the pre-event life and the event's kind
// whose guard holds, or -1. old is the state before the event, next the state
// after its facts were folded in.
func match(old, next State, e Event) int {
	for i, r := range table {
		if r.Kind == e.Kind && slices.Contains(r.From, old.Life) && (r.When == nil || r.When(old, next, e)) {
			return i
		}
	}
	return -1
}

// absorb folds the event's facts into a copy of the state. Every fold is a
// "set" or a "latest wins" (a max of times, a head that replaces a head, a
// first-recorded source), so folding the same event again changes nothing.
func absorb(s State, e Event) State {
	if e.Kind == KindLaneOpened && reopens(s, s, e) {
		s = State{Branch: s.Branch, Life: LifeNone}
	}
	s.Actors, s.CI = cloneMap(s.Actors), cloneMap(s.CI)
	switch e.Kind {
	case KindLaneOpened:
		s.Worktree, s.Base, s.Head = first(s.Worktree, e.Worktree), first(s.Base, e.Base), first(s.Head, e.Head)
		s.LastActive = later(s.LastActive, e.At)
	case KindCommitGated:
		s.Head = first(e.Head, s.Head)
	case KindPush:
		s.Head = first(e.Head, s.Head)
		s = moveCI(s, e.Head)
	case KindPROpened:
		s.Head = first(e.Head, s.Head)
		s = moveCI(s, s.Head)
		if len(e.Required) > 0 {
			s.CIRequired = slices.Compact(slices.Sorted(slices.Values(e.Required)))
		}
	case KindCIVerdict:
		s = takeVerdict(s, e)
	case KindLaneMerged:
		s.MergedBy = first(s.MergedBy, e.Source, SourceAncestry)
		s.ClosedAt = later(s.ClosedAt, e.At)
	case KindLaneRemoved:
		s.ClosedAt = later(s.ClosedAt, e.At)
	}
	return touch(s, e)
}

// touch records the event's actor and the lane's branch. A departure drops
// the actor but still counts as activity: "no actor for 30 min" runs from it.
func touch(s State, e Event) State {
	if s.Branch == "" {
		s.Branch = e.Lane
	}
	if e.Actor == "" {
		return s
	}
	if e.Kind == KindLaneLeft {
		delete(s.Actors, e.Actor)
	} else if seen, ok := s.Actors[e.Actor]; !ok || e.At.After(seen) {
		s.Actors[e.Actor] = e.At
	}
	s.LastActive = later(s.LastActive, e.At)
	return s
}

// moveCI points the lane's CI results at a new head and forgets the old
// head's results; the same head changes nothing.
func moveCI(s State, head string) State {
	if head != "" && head != s.PRHead {
		s.PRHead = head
		s.CI = map[string]Conclusion{}
	}
	return s
}

// takeVerdict records one OS's result. A verdict for another head than the
// PR's is stale and dropped: the pushed head it judged is gone. One with no
// head, or one that arrives before any PR head is known, is taken as the PR's.
func takeVerdict(s State, e Event) State {
	switch e.Conclusion {
	case CIStarted, CIGreen, CIRed, CIUnavailable:
	default:
		return s
	}
	if e.Head != "" {
		if s.PRHead != "" && e.Head != s.PRHead {
			return s
		}
		s.PRHead = e.Head
	}
	s.CI[e.OS] = e.Conclusion
	return s
}

// ciOutcome is the CI life the lane's results add up to, or "" with no
// results. A red on any OS wins at once; otherwise a declared OS not yet
// concluded keeps it pending; otherwise an unavailable one makes it
// unavailable (not failed, §8); otherwise it is green.
func (s State) ciOutcome() Life {
	if len(s.CI) == 0 {
		return ""
	}
	want := s.CIRequired
	if len(want) == 0 {
		want = slices.Collect(maps.Keys(s.CI))
	}
	var red, wait, unavailable bool
	for _, c := range s.CI {
		red = red || c == CIRed
	}
	for _, os := range want {
		switch s.CI[os] {
		case CIGreen:
		case CIUnavailable:
			unavailable = true
		default:
			wait = true
		}
	}
	switch {
	case red:
		return LifeCIRed
	case wait:
		return LifeCIPending
	case unavailable:
		return LifeCIUnavailable
	}
	return LifeCIGreen
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	maps.Copy(out, m)
	return out
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
