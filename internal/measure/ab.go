package measure

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/tddarm"
)

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
	// Versions are the binary versions the arm's lanes ran under, listed only when
	// there is more than one: a mixed arm is a comparison to read with care.
	Versions []string `json:"versions,omitempty"`
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
	// Metrics are the pre-registered metrics (abMetrics), primary first. Verdict is the
	// primary metric's; Decidable is whether it is anything but deciding.
	Metrics   []ABMetric `json:"metrics"`
	Verdict   string     `json:"verdict"`
	Decidable bool       `json:"decidable"`
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
	versions           versionSet
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
			f = &abLaneFacts{langs: map[string][2]int{}, versions: versionSet{}}
			lanes[lane] = f
		}
		return f
	}
	canon := mergeLanes(s.evs)
	for _, e := range s.evs {
		if e.Lane == "" || !s.in(e.at) {
			continue
		}
		lane := canon(e.Lane)
		get(lane).versions[binVersion(e.Event)] = true
		foldABEvent(get(lane), e)
	}
	if o.RepoKey != "" {
		for lane, f := range lanes {
			if f.arm == "" && !abTrunk(lane) {
				f.arm = tddarm.Of(o.RepoKey, lane) // a pure function of repo and lane: no event needed
			}
		}
	}
	return summariseAB(lanes)
}

// abTrunk is a lane name that is trunk, which no arm covers (tddarm.Resolve).
func abTrunk(lane string) bool {
	switch lane {
	case "main", "master", "@trunk":
		return true
	}
	return false
}

// mergeSuffix is the branch suffix a merge branch carries after its lane's name
// (ariadne: `git switch -c <lane>-merge`, its premerge gate firing there).
const mergeSuffix = "-merge"

// mergeLanes returns the lane a branch counts as. A `<lane>-merge` branch is its lane's
// merge when its base lane has events of its own or when a merge gate fired on it; any
// other lane that merely ends in -merge keeps its name.
func mergeLanes(evs []stamped) func(string) string {
	seen, gated := map[string]bool{}, map[string]bool{}
	for _, e := range evs {
		seen[e.Lane] = true
		if e.Kind == "merge_gate" {
			gated[e.Lane] = true
		}
	}
	return func(lane string) string {
		base, ok := strings.CutSuffix(lane, mergeSuffix)
		if !ok || base == "" || (!seen[base] && !gated[lane]) {
			return lane
		}
		return base
	}
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
	armVersions := map[string]versionSet{}
	armLanes := map[string][]*abLaneFacts{}
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
		armLanes[f.arm] = append(armLanes[f.arm], f)
		if armVersions[f.arm] == nil {
			armVersions[f.arm] = versionSet{}
		}
		for v := range f.versions {
			armVersions[f.arm][v] = true
		}
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
	for _, name := range abArms {
		a := arms[name]
		a.TimeToGreen = dist(samples[name])
		if vs := armVersions[name].list(); len(vs) > 1 {
			a.Versions = vs
		}
		out.Arms = append(out.Arms, *a)
	}
	out.Metrics, out.Verdict, out.Decidable = foldMetrics(armLanes)
	out.Languages = zeroFilledLanguages(langs)
	return out
}

// zeroFilledLanguages is one row per arm for every language either arm met, an arm
// that met none showing zeros, sorted by arm then language.
func zeroFilledLanguages(langs map[string]*ABLang) []ABLang {
	seen := map[string]bool{}
	for _, l := range langs {
		seen[l.Lang] = true
	}
	var out []ABLang
	for _, arm := range abArms {
		for lang := range seen {
			if l := langs[arm+"/"+lang]; l != nil {
				out = append(out, *l)
			} else {
				out = append(out, ABLang{Arm: arm, Lang: lang})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
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
		p("%-8s %d lanes (a decision is forced at %d per arm)", a.Arm, a.Lanes, MaxABLanes)
		p("  denies %d  warnings %d  overrides %d  dropped for the budget %d  held out %d", a.Denies, a.Warnings, a.Overrides, a.Dropped, a.HeldOut)
		p("  escapes %d (escape records %d, CI red after a local green %d)", a.Escapes, a.EscapeRecords, a.CIRedAfterGreen)
		p("  friction: denies %d + overrides %d; time to green n %d%s  p50 %s  p90 %s",
			a.Denies, a.Overrides, a.TimeToGreen.N, a.noGreenNote(), secs(a.TimeToGreen.P50), secs(a.TimeToGreen.P90))
		if len(a.Versions) > 1 {
			p("  note: %s lanes ran under different versions: %s", a.Arm, strings.Join(a.Versions, ", "))
		}
	}
	if len(ab.Languages) == 0 {
		p("  languages: none recorded (no red-green decision named a language)")
	}
	for _, l := range ab.Languages {
		p("  %-8s %-10s %d lanes  denies %d  warnings %d", l.Arm, l.Lang, l.Lanes, l.Denies, l.Warnings)
	}
	p("pinned lanes (outside both arms) %d", ab.Pinned)
	p("dropped for the budget: red-green decisions the PreToolUse hook did not finish in time; the lane stays in its arm, the decision is unrecorded")
	p("decision metrics (enforce minus warn; lower is better; deciding until an interval excludes 0 or lies inside the band):")
	for _, m := range ab.Metrics {
		p("  %s", m.text())
	}
	p("verdict (%s): %s", abMetrics[0].Name, ab.Verdict)
	if ab.Verdict == VerdictMaxReached {
		p("  %s", TooSmall)
	}
	return b.String()
}

// noGreenNote says why an arm's time to green has no sample, where it has none: a log
// that holds no red at all is a different finding from one that dropped its decisions.
func (a ABArm) noGreenNote() string {
	switch {
	case a.TimeToGreen.N > 0:
		return ""
	case a.Denies+a.Warnings > 0:
		return " (red recorded, no green after it yet)"
	case a.Dropped > 0:
		return fmt.Sprintf(" (not recorded: %d decision(s) dropped for the budget, so a red may have gone unseen)", a.Dropped)
	}
	return " (no red occurred: no deny or warning)"
}
