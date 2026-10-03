package kernel

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"pgregory.net/rapid"
)

var allLives = []Life{
	LifeNone, LifeOpen, LifeCommitted, LifePR, LifeCIPending, LifeCIGreen,
	LifeCIRed, LifeCIUnavailable, LifeMerged, LifeRemoved, LifeAbandoned,
}

// allKinds is every kind the lane machine consumes, plus kinds it must
// tolerate: another machine's (run.result) and nonsense.
var allKinds = []Kind{
	KindLaneOpened, KindLaneEntered, KindLaneLeft, KindLaneMerged, KindLaneRemoved,
	KindLaneTick, KindEdit, KindCommitGated, KindPush, KindPROpened, KindCIVerdict,
	"run.result", "garbage",
}

var offsets = []time.Duration{
	-time.Hour, 0, time.Minute, 29 * time.Minute, 30 * time.Minute, 31 * time.Minute,
	2 * time.Hour, 14*24*time.Hour - time.Second, 14 * 24 * time.Hour, 20 * 24 * time.Hour,
}

func genAt() *rapid.Generator[time.Time] {
	random := rapid.Map(rapid.Int64Range(-60, 30*24*60), func(m int64) time.Duration { return time.Duration(m) * time.Minute })
	return rapid.Map(rapid.OneOf(rapid.SampledFrom(offsets), random), func(d time.Duration) time.Time { return t0.Add(d) })
}

func genEvent() *rapid.Generator[Event] {
	oses := []string{"", "linux", "windows"}
	return rapid.Custom(func(t *rapid.T) Event {
		return Event{
			Kind:       rapid.SampledFrom(allKinds).Draw(t, "kind"),
			Lane:       rapid.SampledFrom([]string{"", "fix", "fix", "fix", "other"}).Draw(t, "lane"),
			Actor:      rapid.SampledFrom([]string{"", "s/a", "s/b"}).Draw(t, "actor"),
			At:         genAt().Draw(t, "at"),
			Worktree:   rapid.SampledFrom([]string{"", "/w/fix"}).Draw(t, "worktree"),
			Base:       rapid.SampledFrom([]string{"", "abc", "def"}).Draw(t, "base"),
			Head:       rapid.SampledFrom([]string{"", "h1", "h2"}).Draw(t, "head"),
			Source:     rapid.SampledFrom([]string{"", "api", "local", "ancestry", "github", "odd"}).Draw(t, "source"),
			OS:         rapid.SampledFrom(oses).Draw(t, "os"),
			Conclusion: rapid.SampledFrom([]Conclusion{"", CIStarted, CIGreen, CIRed, CIUnavailable, "odd"}).Draw(t, "conclusion"),
			Required:   rapid.SliceOfN(rapid.SampledFrom(oses[1:]), 0, 3).Draw(t, "required"),
			TreeClean:  rapid.Bool().Draw(t, "clean"),
			Locked:     rapid.Bool().Draw(t, "locked"),
		}
	})
}

func genState() *rapid.Generator[State] {
	oses := []string{"", "linux", "windows"}
	return rapid.Custom(func(t *rapid.T) State {
		return State{
			Branch:     rapid.SampledFrom([]string{"", "fix", "other"}).Draw(t, "branch"),
			Worktree:   rapid.SampledFrom([]string{"", "/w/fix"}).Draw(t, "worktree"),
			Base:       rapid.SampledFrom([]string{"", "abc"}).Draw(t, "base"),
			Head:       rapid.SampledFrom([]string{"", "h1", "h2"}).Draw(t, "head"),
			PRHead:     rapid.SampledFrom([]string{"", "h1", "h2"}).Draw(t, "prhead"),
			Life:       rapid.SampledFrom(append([]Life{"", "garbled"}, allLives...)).Draw(t, "life"),
			Actors:     rapid.MapOfN(rapid.SampledFrom([]string{"s/a", "s/b"}), genAt(), 0, 2).Draw(t, "actors"),
			LastActive: genAt().Draw(t, "lastactive"),
			ClosedAt:   genAt().Draw(t, "closedat"),
			MergedBy:   rapid.SampledFrom([]string{"", "api", "github"}).Draw(t, "mergedby"),
			CI:         rapid.MapOfN(rapid.SampledFrom(oses), rapid.SampledFrom([]Conclusion{CIStarted, CIGreen, CIRed, CIUnavailable}), 0, 3).Draw(t, "ci"),
			CIRequired: rapid.SliceOfN(rapid.SampledFrom(oses[1:]), 0, 2).Draw(t, "ciRequired"),
		}
	})
}

// canon makes nil and empty collections compare equal.
func canon(s State) State {
	if s.Actors == nil {
		s.Actors = map[string]time.Time{}
	}
	if s.CI == nil {
		s.CI = map[string]Conclusion{}
	}
	if s.CIRequired == nil {
		s.CIRequired = []string{}
	}
	return s
}

func cloneState(s State) State {
	c := s
	c.Actors = make(map[string]time.Time, len(s.Actors))
	for k, v := range s.Actors {
		c.Actors[k] = v
	}
	c.CI = make(map[string]Conclusion, len(s.CI))
	for k, v := range s.CI {
		c.CI[k] = v
	}
	c.CIRequired = slices.Clone(s.CIRequired)
	return c
}

func closed(l Life) bool { return l == LifeMerged || l == LifeRemoved }

func TestStep_isTotalAndDeterministicForAnyStateAndEvent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, e := genState().Draw(t, "state"), genEvent().Draw(t, "event")
		a, fxA := Step(s, e)
		b, fxB := Step(s, e)
		if !reflect.DeepEqual(canon(a), canon(b)) || !reflect.DeepEqual(fxA, fxB) {
			t.Fatalf("two calls disagree: %+v %v vs %+v %v", a, fxA, b, fxB)
		}
		for _, f := range fxA {
			if f.Lane != a.Branch {
				t.Fatalf("effect %+v names a lane other than %q", f, a.Branch)
			}
		}
	})
}

func TestStep_neverMutatesItsInput(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, e := genState().Draw(t, "state"), genEvent().Draw(t, "event")
		before, beforeReq := cloneState(s), slices.Clone(e.Required)
		Step(s, e)
		if !reflect.DeepEqual(canon(s), canon(before)) || !slices.Equal(e.Required, beforeReq) {
			t.Fatalf("Step changed its input: state %+v -> %+v", before, s)
		}
	})
}

func TestStep_aDuplicateEventChangesNothingAndSaysNothing(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, e := genState().Draw(t, "state"), genEvent().Draw(t, "event")
		once, _ := Step(s, e)
		twice, fx := Step(once, e)
		if !reflect.DeepEqual(canon(once), canon(twice)) {
			t.Fatalf("event twice differs from once:\nonce  %+v\ntwice %+v", once, twice)
		}
		if len(fx) != 0 {
			t.Fatalf("the duplicate emitted %v", fx)
		}
	})
}

func TestStep_effectsComeOnlyWithALifeChange(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, e := genState().Draw(t, "state"), genEvent().Draw(t, "event")
		next, fx := Step(s, e)
		was := s.Life
		if was == "" {
			was = LifeNone
		}
		if len(fx) != 0 && next.Life == was {
			t.Fatalf("life stayed %q but effects %v came out", was, fx)
		}
	})
}

func TestStep_aClosedLaneOpensAgainOnlyThroughANewerLaneOpened(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genState().Draw(t, "state")
		for i, e := range rapid.SliceOfN(genEvent(), 1, 40).Draw(t, "events") {
			next, _ := Step(s, e)
			if closed(s.Life) && !closed(next.Life) {
				if e.Kind != KindLaneOpened || !e.At.After(s.ClosedAt) || (s.Branch != "" && e.Lane != "" && e.Lane != s.Branch) {
					t.Fatalf("event %d (%s at %v) took %q to %q; closed since %v", i, e.Kind, e.At, s.Life, next.Life, s.ClosedAt)
				}
			}
			s = next
		}
	})
}

func TestStep_ciGreenHoldsOnlyWhenEveryDeclaredOSIsGreen(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		declared := []string{"linux", "windows"}
		s, _ := Step(State{}, ev(KindLaneOpened, 0))
		s, _ = Step(s, ev(KindPROpened, 0, head("h1"), required(declared...)))
		verdicts := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) Event {
			return verdict(time.Minute, rapid.SampledFrom(declared).Draw(t, "os"),
				rapid.SampledFrom([]Conclusion{CIStarted, CIGreen, CIRed, CIUnavailable}).Draw(t, "c"), "h1")
		}), 1, 12).Draw(t, "verdicts")
		for _, e := range verdicts {
			s, _ = Step(s, e)
			anyRed := slices.Contains([]Conclusion{s.CI["linux"], s.CI["windows"]}, CIRed)
			allGreen := s.CI["linux"] == CIGreen && s.CI["windows"] == CIGreen
			switch {
			case s.Life == LifeCIGreen && !allGreen:
				t.Fatalf("ci_green with results %v", s.CI)
			case allGreen && s.Life != LifeCIGreen:
				t.Fatalf("every OS green but life %q", s.Life)
			case anyRed && s.Life != LifeCIRed:
				t.Fatalf("a red result %v but life %q", s.CI, s.Life)
			}
		}
	})
}

func TestStep_lifeStaysInTheKnownSetFromAnyReachableStart(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := State{}
		for i, e := range rapid.SliceOfN(genEvent(), 1, 40).Draw(t, "events") {
			s, _ = Step(s, e)
			if !slices.Contains(allLives, s.Life) {
				t.Fatalf("event %d (%s) left life %q outside the table", i, e.Kind, s.Life)
			}
		}
	})
}
