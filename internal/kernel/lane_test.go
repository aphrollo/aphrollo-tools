package kernel

import (
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)

// ev builds an event on lane "fix" by actor "s/a", after t0.
func ev(kind Kind, after time.Duration, opts ...func(*Event)) Event {
	e := Event{Kind: kind, Lane: "fix", Actor: "s/a", At: t0.Add(after)}
	for _, o := range opts {
		o(&e)
	}
	return e
}

func head(h string) func(*Event)   { return func(e *Event) { e.Head = h } }
func by(actor string) func(*Event) { return func(e *Event) { e.Actor = actor } }
func required(os ...string) func(*Event) {
	return func(e *Event) { e.Required = os }
}
func source(s string) func(*Event) { return func(e *Event) { e.Source = s } }
func tick(clean, locked bool) func(*Event) {
	return func(e *Event) { e.Actor, e.TreeClean, e.Locked = "", clean, locked }
}

// verdict builds a CI-side ci.verdict (no actor) for one OS and head.
func verdict(after time.Duration, os string, c Conclusion, h string) Event {
	return ev(KindCIVerdict, after, by(""), func(e *Event) { e.OS, e.Conclusion, e.Head = os, c, h })
}

func eff(kind EffectKind, detail string) Effect {
	return Effect{Kind: kind, Lane: "fix", Detail: detail}
}

type step struct {
	e    Event
	life Life
	fx   []Effect
}

// walk feeds each step's event to Step and holds the resulting life and effects
// to the step's literal expectation.
func walk(t *testing.T, s State, steps []step) State {
	t.Helper()
	for i, st := range steps {
		var got []Effect
		s, got = Step(s, st.e)
		if s.Life != st.life {
			t.Fatalf("step %d (%s): life = %q, want %q", i, st.e.Kind, s.Life, st.life)
		}
		if !reflect.DeepEqual(got, st.fx) {
			t.Fatalf("step %d (%s): effects = %v, want %v", i, st.e.Kind, got, st.fx)
		}
	}
	return s
}

func TestStep_lifecycleFromOpenToRemoved(t *testing.T) {
	m := time.Minute
	s := walk(t, State{}, []step{
		{ev(KindLaneOpened, 0, func(e *Event) { e.Worktree, e.Base = "/w/fix", "abc" }), LifeOpen,
			[]Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}},
		{ev(KindEdit, 1*m), LifeOpen, nil},
		{ev(KindCommitGated, 2*m, head("h1")), LifeCommitted, nil},
		{ev(KindPROpened, 3*m, head("h1"), required("linux", "windows")), LifePR, nil},
		{verdict(4*m, "linux", CIStarted, "h1"), LifeCIPending, nil},
		{verdict(4*m, "windows", CIStarted, "h1"), LifeCIPending, nil},
		{verdict(9*m, "linux", CIGreen, "h1"), LifeCIPending, nil},
		{verdict(12*m, "windows", CIGreen, "h1"), LifeCIGreen, []Effect{eff(EffectLaneNews, "ci_green")}},
		{ev(KindLaneMerged, 13*m, by(""), source("api")), LifeMerged, []Effect{eff(EffectLaneNews, "merged")}},
		{ev(KindLaneTick, 32*m, tick(true, false)), LifeMerged, nil},
		{ev(KindLaneTick, 33*m, tick(true, false)), LifeRemoved, []Effect{eff(EffectRemoveWorktree, "")}},
	})
	if s.Base != "abc" || s.Worktree != "/w/fix" || s.MergedBy != "api" {
		t.Fatalf("state = base %q worktree %q mergedBy %q, want abc /w/fix api", s.Base, s.Worktree, s.MergedBy)
	}
}

func TestStep_newHeadPushedSendsCIStatesBackToPR(t *testing.T) {
	m := time.Minute
	walk(t, State{}, []step{
		{ev(KindLaneOpened, 0), LifeOpen, []Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}},
		{ev(KindPROpened, 1*m, head("h1"), required("linux")), LifePR, nil},
		{verdict(2*m, "linux", CIRed, "h1"), LifeCIRed, []Effect{eff(EffectLaneNews, "ci_red")}},
		{ev(KindPush, 3*m, head("h1")), LifeCIRed, nil},
		{ev(KindPush, 4*m, head("h2")), LifePR, nil},
		{verdict(5*m, "linux", CIGreen, "h1"), LifePR, nil},
		{verdict(6*m, "linux", CIStarted, "h2"), LifeCIPending, nil},
		{verdict(7*m, "linux", CIGreen, "h2"), LifeCIGreen, []Effect{eff(EffectLaneNews, "ci_green")}},
		{ev(KindPush, 8*m, head("h3")), LifePR, nil},
	})
}

func TestStep_ciOutcomeFollowsEveryDeclaredOS(t *testing.T) {
	m := time.Minute
	walk(t, State{}, []step{
		{ev(KindLaneOpened, 0), LifeOpen, []Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}},
		{ev(KindPROpened, 1*m, head("h1"), required("linux", "windows")), LifePR, nil},
		{verdict(2*m, "linux", CIGreen, "h1"), LifeCIPending, nil},
		{verdict(3*m, "windows", CIUnavailable, "h1"), LifeCIUnavailable, []Effect{eff(EffectLaneNews, "ci_unavailable")}},
		{verdict(4*m, "windows", CIStarted, "h1"), LifeCIPending, nil},
		{verdict(5*m, "windows", CIRed, "h1"), LifeCIRed, []Effect{eff(EffectLaneNews, "ci_red")}},
		{verdict(6*m, "windows", CIGreen, "h1"), LifeCIGreen, []Effect{eff(EffectLaneNews, "ci_green")}},
	})
}

func TestStep_aRedOnOneOSWinsWhileAnotherIsStillMissing(t *testing.T) {
	m := time.Minute
	walk(t, State{}, []step{
		{ev(KindLaneOpened, 0), LifeOpen, []Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}},
		{ev(KindPROpened, 1*m, head("h1"), required("linux", "windows")), LifePR, nil},
		{verdict(2*m, "linux", CIRed, "h1"), LifeCIRed, []Effect{eff(EffectLaneNews, "ci_red")}},
	})
}

func TestStep_verdictWithoutAnOpenedPRIsTakenAsEvidenceOfOne(t *testing.T) {
	// A PR opened outside the verb still has CI: the lane must not stay
	// "committed" while a green verdict names its head.
	walk(t, State{}, []step{
		{ev(KindLaneOpened, 0), LifeOpen, []Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}},
		{ev(KindCommitGated, time.Minute, head("h1")), LifeCommitted, nil},
		{verdict(2*time.Minute, "linux", CIGreen, "h1"), LifeCIGreen, []Effect{eff(EffectLaneNews, "ci_green")}},
	})
}

func TestStep_removalNeedsIdleCleanUnlockedLane(t *testing.T) {
	m := time.Minute
	merged := walk(t, State{}, []step{
		{ev(KindLaneOpened, 0), LifeOpen, []Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}},
		{ev(KindLaneMerged, 1*m, by(""), source("github")), LifeMerged, []Effect{eff(EffectLaneNews, "merged")}},
		{ev(KindEdit, 10*m), LifeMerged, nil},
	})
	cases := []struct {
		name  string
		after time.Duration
		opt   func(*Event)
		life  Life
	}{
		{"29 minutes after the last actor", 39 * m, tick(true, false), LifeMerged},
		{"30 minutes after the last actor", 40 * m, tick(true, false), LifeRemoved},
		{"idle but a dirty tree", 90 * m, tick(false, false), LifeMerged},
		{"idle but a Claude Code worktree lock", 90 * m, tick(true, true), LifeMerged},
	}
	for _, c := range cases {
		got, _ := Step(merged, ev(KindLaneTick, c.after, c.opt))
		if got.Life != c.life {
			t.Errorf("%s: life = %q, want %q", c.name, got.Life, c.life)
		}
	}
}

func TestStep_openAndCommittedLanesAbandonAfterFourteenIdleDays(t *testing.T) {
	day := 24 * time.Hour
	open, _ := Step(State{}, ev(KindLaneOpened, 0))
	committed, _ := Step(open, ev(KindCommitGated, time.Hour, head("h1")))
	for name, c := range map[string]struct {
		s      State
		active time.Duration
	}{"open": {open, 0}, "committed": {committed, time.Hour}} {
		// The clean, unlocked tick must not matter: abandonment reads idleness only.
		if got, _ := Step(c.s, ev(KindLaneTick, c.active+14*day, tick(false, true))); got.Life != LifeAbandoned {
			t.Errorf("%s lane idle exactly 14d: life = %q, want abandoned", name, got.Life)
		}
		if got, _ := Step(c.s, ev(KindLaneTick, c.active+14*day-time.Second, tick(true, false))); got.Life == LifeAbandoned {
			t.Errorf("%s lane idle 14d minus 1s: abandoned too early", name)
		}
	}
}

func TestStep_aLaneWithAPRIsNeverAbandoned(t *testing.T) {
	s := walk(t, State{}, []step{
		{ev(KindLaneOpened, 0), LifeOpen, []Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}},
		{ev(KindPROpened, time.Minute, head("h1")), LifePR, nil},
	})
	if got, _ := Step(s, ev(KindLaneTick, 60*24*time.Hour, tick(true, false))); got.Life != LifePR {
		t.Fatalf("pr lane 60 idle days: life = %q, want pr", got.Life)
	}
}

func TestStep_anAbandonedLaneRevivesOnActivity(t *testing.T) {
	day := 24 * time.Hour
	abandoned := walk(t, State{}, []step{
		{ev(KindLaneOpened, 0), LifeOpen, []Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}},
		{ev(KindLaneTick, 15*day, tick(true, false)), LifeAbandoned, nil},
	})
	cases := []struct {
		e    Event
		life Life
	}{
		{ev(KindEdit, 16*day), LifeOpen},
		{ev(KindLaneEntered, 16*day), LifeOpen},
		{ev(KindCommitGated, 16*day, head("h2")), LifeCommitted},
		{ev(KindPROpened, 16*day, head("h2")), LifePR},
		{ev(KindLaneMerged, 16*day, by("")), LifeMerged},
	}
	for _, c := range cases {
		if got, _ := Step(abandoned, c.e); got.Life != c.life {
			t.Errorf("abandoned + %s: life = %q, want %q", c.e.Kind, got.Life, c.life)
		}
	}
}

func TestStep_aMergeIsRecordedFromEveryLiveLife(t *testing.T) {
	lives := []Life{LifeNone, LifeOpen, LifeCommitted, LifePR, LifeCIPending, LifeCIGreen, LifeCIRed, LifeCIUnavailable, LifeAbandoned}
	for _, src := range []string{"api", "local", "ancestry", "github"} {
		for _, l := range lives {
			got, fx := Step(State{Branch: "fix", Life: l}, ev(KindLaneMerged, 0, by(""), source(src)))
			if got.Life != LifeMerged || got.MergedBy != src {
				t.Errorf("%s + merged{%s}: life %q mergedBy %q, want merged %s", l, src, got.Life, got.MergedBy, src)
			}
			if want := []Effect{eff(EffectLaneNews, "merged")}; !reflect.DeepEqual(fx, want) {
				t.Errorf("%s + merged{%s}: effects = %v, want only the lane news line", l, src, fx)
			}
		}
	}
}

func TestStep_theFirstRecordedMergeSourceStandsWhenPostMergeFindsTheMergeAgain(t *testing.T) {
	s := walk(t, State{}, []step{
		{ev(KindLaneOpened, 0), LifeOpen, []Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}},
		{ev(KindLaneMerged, time.Minute, by(""), source(SourceAPI)), LifeMerged, []Effect{eff(EffectLaneNews, "merged")}},
		{ev(KindLaneMerged, 2*time.Minute, by(""), source(SourceAncestry)), LifeMerged, nil},
	})
	if s.MergedBy != SourceAPI {
		t.Fatalf("MergedBy = %q, want api", s.MergedBy)
	}
}

func TestStep_mergedLaneStaysMergedOnEveryEventButANewerLaneOpened(t *testing.T) {
	m := time.Minute
	merged := walk(t, State{}, []step{
		{ev(KindLaneOpened, 0, func(e *Event) { e.Base = "old" }), LifeOpen, []Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}},
		{ev(KindLaneMerged, 60*m, by(""), source("api")), LifeMerged, []Effect{eff(EffectLaneNews, "merged")}},
	})
	for _, e := range []Event{
		ev(KindEdit, 61*m), ev(KindLaneEntered, 61*m), ev(KindCommitGated, 61*m, head("h9")),
		ev(KindPROpened, 61*m, head("h9")), ev(KindPush, 61*m, head("h9")),
		verdict(61*m, "linux", CIGreen, "h9"), ev(KindLaneTick, 61*m, tick(false, true)),
		ev(KindLaneOpened, 0), // the original opened event delivered again
		ev(KindLaneOpened, 60*m),
	} {
		if got, _ := Step(merged, e); got.Life != LifeMerged {
			t.Errorf("merged + %s at %v: life = %q, want merged", e.Kind, e.At.Sub(t0), got.Life)
		}
	}
	got, fx := Step(merged, ev(KindLaneOpened, 61*m, by("s/b"), func(e *Event) { e.Base = "new" }))
	if got.Life != LifeOpen || got.Base != "new" || len(got.Actors) != 1 || got.MergedBy != "" {
		t.Fatalf("a newer lane.opened: life %q base %q actors %v mergedBy %q, want a fresh open lane", got.Life, got.Base, got.Actors, got.MergedBy)
	}
	if want := []Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}; !reflect.DeepEqual(fx, want) {
		t.Fatalf("a newer lane.opened: effects = %v, want deps install and warm-up", fx)
	}
}

func TestStep_removedLaneOpensAgainOnlyThroughANewLaneOpened(t *testing.T) {
	s := walk(t, State{}, []step{
		{ev(KindLaneOpened, 0), LifeOpen, []Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}},
		{ev(KindLaneRemoved, time.Hour, by("")), LifeRemoved, nil},
	})
	if got, _ := Step(s, ev(KindEdit, 2*time.Hour)); got.Life != LifeRemoved {
		t.Fatalf("removed + edit: life = %q, want removed", got.Life)
	}
	if got, _ := Step(s, ev(KindLaneOpened, 2*time.Hour)); got.Life != LifeOpen {
		t.Fatalf("removed + newer lane.opened: life = %q, want open", got.Life)
	}
}

func TestStep_firstHookOfAnUnknownLaneOpensIt(t *testing.T) {
	for _, k := range []Kind{KindLaneOpened, KindLaneEntered, KindEdit} {
		got, fx := Step(State{}, ev(k, 0))
		if got.Life != LifeOpen || got.Branch != "fix" {
			t.Errorf("none + %s: life %q branch %q, want open fix", k, got.Life, got.Branch)
		}
		if want := []Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}; !reflect.DeepEqual(fx, want) {
			t.Errorf("none + %s: effects = %v, want deps install and warm-up", k, fx)
		}
	}
}

func TestStep_zeroStateIsAnUnknownLaneAndOtherEventsLeaveItUnknown(t *testing.T) {
	for _, k := range []Kind{KindCommitGated, KindPROpened, KindPush, KindCIVerdict, KindLaneLeft, KindLaneTick, KindLaneRemoved, "run.result"} {
		got, fx := Step(State{}, ev(k, 0, head("h1")))
		if !reflect.DeepEqual(got, State{Life: LifeNone}) && !reflect.DeepEqual(got, State{}) {
			t.Errorf("none + %s: state = %+v, want it untouched", k, got)
		}
		if len(fx) != 0 {
			t.Errorf("none + %s: effects = %v, want none", k, fx)
		}
	}
}

func TestStep_eventForAnotherBranchChangesNothing(t *testing.T) {
	s, _ := Step(State{}, ev(KindLaneOpened, 0))
	other := ev(KindCommitGated, time.Minute, head("h1"))
	other.Lane = "elsewhere"
	got, fx := Step(s, other)
	if !reflect.DeepEqual(got, s) || len(fx) != 0 {
		t.Fatalf("event for another branch: state %+v effects %v, want %+v and none", got, fx, s)
	}
}

func TestAdvance_reportsNoRowForAnIgnoredEvent(t *testing.T) {
	s, _ := Step(State{}, ev(KindLaneOpened, 0))
	other := ev(KindEdit, time.Minute)
	other.Lane = "elsewhere"
	if _, _, row := advance(s, other); row != -1 {
		t.Fatalf("event for another branch: row = %d, want -1", row)
	}
}

func TestStep_aLaterPROpenedWithoutDeclaredOSesKeepsTheDeclaredSet(t *testing.T) {
	m := time.Minute
	s := walk(t, State{}, []step{
		{ev(KindLaneOpened, 0), LifeOpen, []Effect{eff(EffectInstallDeps, ""), eff(EffectWarmBuild, "")}},
		{ev(KindPROpened, 1*m, head("h1"), required("linux", "windows")), LifePR, nil},
		{ev(KindPROpened, 2*m, head("h2")), LifePR, nil},
		{verdict(3*m, "linux", CIGreen, "h2"), LifeCIPending, nil},
	})
	if !reflect.DeepEqual(s.CIRequired, []string{"linux", "windows"}) {
		t.Fatalf("CIRequired = %v, want linux and windows kept", s.CIRequired)
	}
}

func TestStep_aLeavingActorIsDroppedButItsDepartureCountsAsActivity(t *testing.T) {
	s, _ := Step(State{}, ev(KindLaneOpened, 0))
	s, _ = Step(s, ev(KindEdit, time.Minute, by("s/b")))
	if len(s.Actors) != 2 {
		t.Fatalf("actors = %v, want s/a and s/b", s.Actors)
	}
	s, _ = Step(s, ev(KindLaneLeft, 5*time.Minute, by("s/b")))
	if _, ok := s.Actors["s/b"]; ok || len(s.Actors) != 1 {
		t.Fatalf("actors after s/b left = %v, want only s/a", s.Actors)
	}
	if !s.LastActive.Equal(t0.Add(5 * time.Minute)) {
		t.Fatalf("LastActive = %v, want the departure time", s.LastActive)
	}
}

func TestStep_actorLastSeenOnlyMovesForward(t *testing.T) {
	s, _ := Step(State{}, ev(KindLaneOpened, 0))
	s, _ = Step(s, ev(KindEdit, 5*time.Minute))
	if got := s.Actors["s/a"]; !got.Equal(t0.Add(5 * time.Minute)) {
		t.Fatalf("after a later edit: last seen %v, want +5m", got.Sub(t0))
	}
	s, _ = Step(s, ev(KindEdit, 2*time.Minute)) // delivered late
	if got := s.Actors["s/a"]; !got.Equal(t0.Add(5 * time.Minute)) {
		t.Fatalf("after an older edit arrived late: last seen %v, want +5m", got.Sub(t0))
	}
}

func TestStep_unknownEventKindTouchesTheActorAndNothingElse(t *testing.T) {
	s, _ := Step(State{}, ev(KindLaneOpened, 0))
	got, fx := Step(s, ev("run.result", time.Minute, by("s/b")))
	if got.Life != LifeOpen || len(fx) != 0 {
		t.Fatalf("run.result: life %q effects %v, want open and none", got.Life, fx)
	}
	if !got.Actors["s/b"].Equal(t0.Add(time.Minute)) {
		t.Fatalf("actors = %v, want s/b seen at +1m", got.Actors)
	}
}

func TestStep_aGarbledLifeNeverPanicsAndKeepsItsLife(t *testing.T) {
	got, fx := Step(State{Branch: "fix", Life: "garbled"}, ev(KindCommitGated, 0, head("h1")))
	if got.Life != "garbled" || len(fx) != 0 {
		t.Fatalf("life %q effects %v, want garbled and none", got.Life, fx)
	}
}

// TestTable_everyRowIsReachableAndStepObeysIt walks a grid of states and events
// across the whole machine. Every row must win for some pair (a shadowed or
// dead row fails here), and whenever a row wins, Step must land on its To with
// its effects exactly when the life moved.
func TestTable_everyRowIsReachableAndStepObeysIt(t *testing.T) {
	day := 24 * time.Hour
	var states []State
	for _, l := range allLives {
		for _, ci := range []map[string]Conclusion{
			nil,
			{"linux": CIGreen}, {"linux": CIRed}, {"linux": CIStarted}, {"linux": CIUnavailable},
			{"linux": CIGreen, "windows": CIGreen},
		} {
			states = append(states, State{
				Branch: "fix", Life: l, Head: "h1", PRHead: "h1",
				LastActive: t0, ClosedAt: t0, CI: ci, CIRequired: []string{"linux", "windows"},
			})
		}
	}
	var events []Event
	for _, k := range allKinds {
		for _, after := range []time.Duration{-time.Hour, time.Minute, 31 * time.Minute, 15 * day} {
			for _, h := range []string{"", "h1", "h2"} {
				for _, os := range []string{"linux", "windows"} {
					for _, c := range []Conclusion{"", CIStarted, CIGreen, CIRed, CIUnavailable} {
						for _, clean := range []bool{false, true} {
							events = append(events, Event{Kind: k, Lane: "fix", Actor: "s/a", At: t0.Add(after),
								Head: h, OS: os, Conclusion: c, TreeClean: clean})
						}
					}
				}
			}
		}
	}
	won := map[int]bool{}
	for _, s := range states {
		for _, e := range events {
			next, fx, row := advance(s, e)
			if row < 0 {
				continue
			}
			won[row] = true
			r := table[row]
			if next.Life != r.To {
				t.Fatalf("row %q: %s + %s landed on %q, want %q", r.Rule, s.Life, e.Kind, next.Life, r.To)
			}
			if r.To == s.Life && len(fx) != 0 {
				t.Fatalf("row %q: %s + %s kept its life but emitted %v", r.Rule, s.Life, e.Kind, fx)
			}
			if r.To != s.Life && len(fx) != len(r.Fx) {
				t.Fatalf("row %q: %s + %s emitted %v, want %d effects", r.Rule, s.Life, e.Kind, fx, len(r.Fx))
			}
		}
	}
	for i, r := range table {
		if !won[i] {
			t.Errorf("row %d %q never wins for any state and event: dead or shadowed", i, r.Rule)
		}
	}
}

func TestTable_everyRowNamesItsSourceSection(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range table {
		if r.Rule == "" || seen[r.Rule] {
			t.Errorf("row rule %q is empty or repeated", r.Rule)
		}
		seen[r.Rule] = true
		if len(r.From) == 0 || r.Kind == "" || r.To == "" {
			t.Errorf("row %q misses a From, Kind or To", r.Rule)
		}
	}
}
