package measure

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// MinABLanes is the lanes each arm needs before the A/B may decide anything
// (docs/trellis-architecture.md §5).
const MinABLanes = 30

// abArms are the two arms, in the order the report lists them.
var abArms = [...]string{"enforce", "warn"}

// ABArm is one arm of the red→green experiment: what its lanes met and what it cost.
type ABArm struct {
	Arm             string `json:"arm"`
	Lanes           int    `json:"lanes"`
	Denies          int    `json:"denies"`
	Warnings        int    `json:"warnings"`
	Overrides       int    `json:"overrides"`
	Escapes         int    `json:"escapes"`
	EscapeRecords   int    `json:"escape_records"`
	CIRedAfterGreen int    `json:"ci_red_after_green"`
	Dropped         int    `json:"dropped"`
	HeldOut         int    `json:"held_out"`
	TimeToGreen     Dist   `json:"time_to_green_secs"`
	Reached         bool   `json:"reached_min_lanes"`
}

// ABLang is an arm's lanes and decisions in one language.
type ABLang struct {
	Arm      string `json:"arm"`
	Lang     string `json:"lang"`
	Lanes    int    `json:"lanes"`
	Denies   int    `json:"denies"`
	Warnings int    `json:"warnings"`
}

// AB is the experiment's readout. Pinned lanes are outside both arms.
type AB struct {
	Arms      []ABArm  `json:"arms"`
	Languages []ABLang `json:"languages"`
	Pinned    int      `json:"pinned_lanes"`
	Decidable bool     `json:"decidable"`
}

// abLaneFacts is what the log says of one lane.
type abLaneFacts struct {
	arm                string
	pinned             bool
	denies, warnings   int
	overrides, dropped int
	heldOut            int
	records, ciRed     int
	firstAt            time.Time
	greenAfter         time.Time
	greenSeen          bool
	ciCounted          bool
	langs              map[string][2]int // lang -> denies, warnings
}

// ComputeAB folds the event log into the A/B readout. A lane is in the arm its first
// lane-arm event, or failing that its first red→green record, names; a pinned lane is
// in neither. Escapes are the lane's escape records that are no false positive plus a
// CI red after a local green of the same lane, counted once per lane.
func ComputeAB(events []tdd.Event, now time.Time, o Options) AB {
	s := newScope(events, now, o)
	lanes := map[string]*abLaneFacts{}
	get := func(lane string) *abLaneFacts {
		f := lanes[lane]
		if f == nil {
			f = &abLaneFacts{langs: map[string][2]int{}}
			lanes[lane] = f
		}
		return f
	}
	for _, e := range s.evs {
		if e.Lane == "" || !s.in(e.at) {
			continue
		}
		foldABEvent(get(e.Lane), e)
	}
	return summariseAB(lanes)
}

// foldABEvent adds one event to its lane's facts.
func foldABEvent(f *abLaneFacts, e stamped) {
	switch e.Kind {
	case "lane-arm":
		f.seeArm(e.Detail["arm"], e.Detail["why"])
	case "shadow":
		if e.Detail["rule"] == "red-green" {
			foldABDecision(f, e)
		}
	case "override":
		if e.Detail["override"] == "override-red-green-allow" {
			f.overrides++
		}
	case "escape":
		if e.Verdict != "false-positive" {
			f.records++
		}
	case "ci":
		if e.Verdict == "red" && f.greenSeen && !f.ciCounted {
			f.ciCounted = true
			f.ciRed++
		}
	case "stage.timing", "commit_gate", "merge_gate":
		if strings.HasPrefix(e.Verdict, "green") {
			f.greenSeen = true
			if !f.firstAt.IsZero() && f.greenAfter.IsZero() {
				f.greenAfter = e.at
			}
		}
	}
}

func (f *abLaneFacts) seeArm(arm, why string) {
	if why == "pinned" {
		f.pinned = true
	}
	if f.arm == "" && arm != "" {
		f.arm = arm
	}
}

// foldABDecision counts one red→green record: a deny, a warning, or a decision the hook
// dropped for its budget.
func foldABDecision(f *abLaneFacts, e stamped) {
	f.seeArm(e.Detail["arm"], e.Detail["arm_why"])
	if e.Detail["relation"] == "unjudged" {
		if e.Detail["cause"] == "budget" {
			f.dropped++
		}
		return
	}
	if e.Detail["held_out"] == "true" {
		f.heldOut++ // the kernel's holdout guided where enforce denies: no deny, warning or friction
		return
	}
	var col int
	switch e.Detail["aphrollo"] {
	case "block":
		f.denies++
		col = 0
	case "warn":
		f.warnings++
		col = 1
	default:
		return
	}
	if f.firstAt.IsZero() {
		f.firstAt = e.at
	}
	if lang := e.Detail["lang"]; lang != "" {
		c := f.langs[lang]
		c[col]++
		f.langs[lang] = c
	}
}

func summariseAB(lanes map[string]*abLaneFacts) AB {
	arms := map[string]*ABArm{}
	samples := map[string][]float64{}
	langs := map[string]*ABLang{}
	for _, name := range abArms {
		arms[name] = &ABArm{Arm: name}
	}
	var out AB
	for _, f := range lanes {
		if f.pinned {
			out.Pinned++
			continue
		}
		a := arms[f.arm]
		if a == nil {
			continue
		}
		a.Lanes++
		a.Denies += f.denies
		a.Warnings += f.warnings
		a.Overrides += f.overrides
		a.Dropped += f.dropped
		a.HeldOut += f.heldOut
		a.EscapeRecords += f.records
		a.CIRedAfterGreen += f.ciRed
		a.Escapes += f.records + f.ciRed
		if !f.greenAfter.IsZero() {
			samples[f.arm] = append(samples[f.arm], f.greenAfter.Sub(f.firstAt).Seconds())
		}
		for lang, c := range f.langs {
			key := f.arm + "/" + lang
			l := langs[key]
			if l == nil {
				l = &ABLang{Arm: f.arm, Lang: lang}
				langs[key] = l
			}
			l.Lanes++
			l.Denies += c[0]
			l.Warnings += c[1]
		}
	}
	out.Decidable = true
	for _, name := range abArms {
		a := arms[name]
		a.TimeToGreen = dist(samples[name])
		a.Reached = a.Lanes >= MinABLanes
		out.Decidable = out.Decidable && a.Reached
		out.Arms = append(out.Arms, *a)
	}
	for _, l := range langs {
		out.Languages = append(out.Languages, *l)
	}
	sort.Slice(out.Languages, func(i, j int) bool {
		a, b := out.Languages[i], out.Languages[j]
		if a.Arm != b.Arm {
			return a.Arm < b.Arm
		}
		return a.Lang < b.Lang
	})
	return out
}

// Text is the readout as the lines `aphrollo stats --ab` prints.
func (ab AB) Text() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	for _, a := range ab.Arms {
		p("%-8s %d of %d lanes (%s)", a.Arm, a.Lanes, MinABLanes, reachedText(a.Reached))
		p("  denies %d  warnings %d  overrides %d  dropped for the budget %d  held out %d", a.Denies, a.Warnings, a.Overrides, a.Dropped, a.HeldOut)
		p("  escapes %d (escape records %d, CI red after a local green %d)", a.Escapes, a.EscapeRecords, a.CIRedAfterGreen)
		p("  friction: denies %d + overrides %d; time to green n %d  p50 %s  p90 %s",
			a.Denies, a.Overrides, a.TimeToGreen.N, secs(a.TimeToGreen.P50), secs(a.TimeToGreen.P90))
	}
	for _, l := range ab.Languages {
		p("  %-8s %-10s %d lanes  denies %d  warnings %d", l.Arm, l.Lang, l.Lanes, l.Denies, l.Warnings)
	}
	p("pinned lanes (outside both arms) %d", ab.Pinned)
	if ab.Decidable {
		p("both arms have %d lanes or more: the A/B can decide", MinABLanes)
	} else {
		p("not decidable yet: each arm needs %d lanes", MinABLanes)
	}
	return b.String()
}

func reachedText(ok bool) string {
	if ok {
		return "enough"
	}
	return "too few"
}
