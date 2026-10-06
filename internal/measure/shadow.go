package measure

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// MinShadowFires is how many shadow fires a rule needs before a rate is printed
// for it (docs/trellis-architecture.md §5: a rule keeps or changes its level only
// over its last 10 or more fires). Under it the report says so.
const MinShadowFires = 10

// ShadowHorizonDays is how long after a would-be block a downstream authority
// may still make it a catch, and a merge a pass: the document's 28 days of a
// merged change staying clean, as far as the log reaches.
const ShadowHorizonDays = 28

const shadowHorizon = ShadowHorizonDays * 24 * time.Hour

// ShadowRule is one rule's shadow fires: what the kernel would have decided
// beside what aphrollo did (internal/shadow). A disagreement is not a wrong
// block. Of the fires where trellis would block and aphrollo did not (Stricter,
// the would-be blocks), the log afterwards says which were wrong (an override on
// the lane within WrongBlockWindow), which were catches (a later commit gate
// refusal, red CI or escape on the lane), which passed (the lane merged with none
// of those) and which are still open. Softer is where aphrollo denies and trellis
// would only guide; SoftHeldOut those that were the holdout arm's by design.
type ShadowRule struct {
	Rule          string `json:"rule"`
	Fires         int    `json:"fires"`
	Agree         int    `json:"agree"`
	Stricter      int    `json:"trellis_stricter"`
	Softer        int    `json:"trellis_softer"`
	SoftHeldOut   int    `json:"trellis_softer_held_out"`
	Mismatch      int    `json:"verdict_mismatch"`
	NotComparable int    `json:"not_comparable"`
	Unjudged      int    `json:"unjudged"`
	HeldOut       int    `json:"held_out"`
	Catches       int    `json:"would_be_catches"`
	Wrong         int    `json:"would_be_wrong"`
	Passes        int    `json:"would_be_passes"`
	Open          int    `json:"would_be_open"`
	Overruns      int    `json:"budget_overruns"`
}

// Rate is the share of the rule's fires where both sides agreed, "" while the
// rule has fewer than MinShadowFires.
func (r ShadowRule) Rate() string {
	if r.Fires < MinShadowFires {
		return ""
	}
	return fmt.Sprintf("%.0f%%", share(r.Agree, r.Fires)*100)
}

// Shadow is the shadow fires of a window, by rule.
type Shadow struct {
	Window string       `json:"window"`
	Fires  int          `json:"fires"`
	Notes  []string     `json:"notes"`
	Rules  []ShadowRule `json:"rules"`
}

// shadowNotes say what the numbers are not: they are read from facts the hooks
// recorded, and the kernel is not running as it will once it decides.
var shadowNotes = []string{
	"observed only where a hook acted: no fact exists where aphrollo did nothing, so agreement is overstated; trellis acting alone is seen at a waived primary-checkout write and, for red-green, at each code edit",
	"the kernel ran on its default config, except red-green and stop-red, which it is asked under tdd = enforce (the level the document blocks them at): aphrollo declares no rule pins or isolation setting to read, so the other levels are the kernel's own",
	"red-green is observed: asked at PreToolUse of each code edit against the lane's record, where aphrollo always allows (it holds the proof at the commit), so every kernel guide or block is a would-be block; an edit's unit is covered when a run of it was green on a tree holding its newest edit, and a unit with no run yet reads as uncovered; whether an edit adds a symbol is read from the edit's text for Go (a new func, or a new exported type, var or const), Python (a new def, or a public class) and TypeScript or JavaScript (a new function, or a new exported declaration), and is false for every other language and for a Bash write; a Python or TypeScript unit is its whole project (the gate holds no per-test or per-symbol knowledge for them), so any green run of the project covers it, and its fires are joined to the commit proof run in the project root; edits and runs on trunk lanes and the primary checkout are not asked",
	"stop-red is asked at every Stop and SubagentStop with aphrollo's own unseen-red fact, so a block and an allow are both recorded; a red the lane record never folded, including one known only from a job record, is unjudged, never softer",
	"a red-green would-be block is a catch when the commit proof later refuses on the lane (a proof must name packages holding the edit's unit), a pass when such a proof passes or the lane merges, and wrong on /tdd off within the wrong-block window; a pass only says the commit gate later proved red→green, which aphrollo already enforces, so passes are near-tautological and catches are rare by structure; the other would-be blocks still come mostly from waived primary-checkout writes, and a law's escape comment is not logged as an override",
	"a run both sides read alike is an agreeing shadow event; a run no verdict was made of, and a red-green, stop-red or lane-fold record that lacked a lane, unit, tree or store or outran the budget, is counted unjudged with its cause and never as agreement",
}

// ComputeShadow folds the shadow events of the window. What follows a fire is
// read from the whole log, not only the window.
func ComputeShadow(events []tdd.Event, now time.Time, o Options) Shadow {
	s := newScope(events, now, o)
	out := Shadow{Window: "whole log", Notes: shadowNotes}
	if o.Window > 0 {
		out.Window = "last " + windowText(o.Window)
	}
	rules := map[string]*ShadowRule{}
	for i, e := range s.evs {
		if !s.in(e.at) {
			continue
		}
		if e.Kind != "shadow" {
			continue
		}
		rule := e.Detail["rule"]
		if rule == "" {
			rule = "unknown"
		}
		r := rules[rule]
		if r == nil {
			r = &ShadowRule{Rule: rule}
			rules[rule] = r
		}
		out.Fires++
		r.Fires++
		held := e.Detail["held_out"] == "true"
		if held {
			r.HeldOut++
		}
		switch e.Detail["relation"] {
		case "agree":
			r.Agree++
		case "trellis-stricter":
			r.Stricter++
			switch followOf(s.evs, i) {
			case followWrong:
				r.Wrong++
			case followCatch:
				r.Catches++
			case followPass:
				r.Passes++
			default:
				r.Open++
			}
		case "trellis-softer":
			r.Softer++
			if held {
				r.SoftHeldOut++
			}
		case "verdict-mismatch":
			r.Mismatch++
		case "not-comparable":
			r.NotComparable++
		case "unjudged":
			r.Unjudged++
			if e.Detail["cause"] == "budget" {
				r.Overruns++
			}
		}
	}
	for _, r := range rules {
		out.Rules = append(out.Rules, *r)
	}
	sort.Slice(out.Rules, func(i, j int) bool { return out.Rules[i].Rule < out.Rules[j].Rule })
	return out
}

type follow int

const (
	followOpen follow = iota
	followWrong
	followCatch
	followPass
)

// followOf is what the log says of the would-be block at evs[i], read from the
// events of its own lane: an override of the same rule within WrongBlockWindow
// makes it wrong, whatever else follows; else a commit gate refusal, red CI or
// escape on the lane makes it a catch; else the lane's merge makes it a pass.
//
// The lane's name, bounded by its merge, is the identity: the events carry no
// worktree root to tell two checkouts of one name apart. A merge ends what leads
// to a wrong block, but not what can still make a catch: an escape or a red CI
// run is made after the merge by definition, so the scan goes on within the
// horizon for them, and stops at the lane's next opening, its first edit, shadow
// fire or lane.opened after the merge, which is another lane of the same name.
// The result stays a pass only if nothing of the kind arrives. A fire on no lane,
// or in the primary checkout (its name is the trunk's, shared by every change
// made there), is never joined and stays open.
func followOf(evs []stamped, i int) follow {
	fire := evs[i]
	if fire.Lane == "" || fire.Detail["primary"] == "true" || isTrunkLane(fire.Lane) {
		return followOpen
	}
	// A red-green fire is judged by the commit proof (the stage that holds red→green
	// in aphrollo): a proof refusal on the lane is the catch, a proof that passed the
	// pass. Any other commit refusal (lint, vet, docs) says nothing of it.
	proof := fire.Detail["rule"] == "red-green"
	caught, merged, proved := false, false, false
	for _, e := range evs[i+1:] {
		if e.at.Sub(fire.at) > shadowHorizon {
			break
		}
		if e.Lane != fire.Lane {
			continue
		}
		if merged && (e.Kind == "edit" || e.Kind == "shadow" || e.Kind == "lane.opened") {
			break
		}
		switch {
		case !merged && e.Kind == "override" && !isAllowedRerun(e) && e.at.Sub(fire.at) <= WrongBlockWindow && overrideAbout(e, fire):
			return followWrong
		case proof && e.Kind == "commit_gate" && e.Stage == "precommit" && e.Verdict == "violated" && proofAbout(e, fire):
			caught = true
		case proof && e.Kind == "commit_gate" && e.Stage == "precommit" && e.Verdict == "red-proven" && proofAbout(e, fire):
			proved = true
		case e.Kind == "commit_gate_result" && e.Verdict == "blocked" && !proof,
			e.Kind == "ci" && e.Verdict == "red",
			e.Kind == "escape" && e.Verdict != "false-positive":
			caught = true
		case e.Kind == "merge" && e.Verdict == "ok":
			merged = true
		}
	}
	switch {
	case caught:
		return followCatch
	case merged || proved:
		return followPass
	}
	return followOpen
}

// proofAbout reports whether a commit proof's run is about the unit a red-green fire
// named. A proof is about a unit only when it names packages (go's ./pkg, ./pkg/...
// or ./...) and one of them holds the unit's package, compared from the unit's
// module root (the fire's unit_pkg, the package relative to its module, which is
// what a proof's own patterns are relative to). A proof that names none, whether a
// whole-project run or a stage's label such as postedit-ledger, is about no unit
// and decides nothing for it.
func proofAbout(proof, fire stamped) bool {
	unit := fire.Detail["unit"]
	if unit == "" {
		return true
	}
	// A project-root unit (Python, TypeScript, any language but Go) is the whole
	// project: the gate holds no per-test or per-symbol knowledge for it, so no
	// package pattern can say what a proof covered. The fire names its project's
	// root, and a proof is about the unit when it ran in that root.
	if root := fire.Detail["unit_root"]; root != "" {
		return sameProjectRoot(proof.Root, root)
	}
	target, ok := fire.Detail["unit_pkg"]
	if !ok {
		target = unit
	}
	for _, arg := range strings.Fields(proof.Cmd) {
		if arg != "." && !strings.HasPrefix(arg, "./") {
			continue
		}
		pat := strings.TrimPrefix(arg, "./")
		base, tree := strings.CutSuffix(pat, "/...")
		switch {
		case pat == "...", pat == target:
			return true
		case tree && (target == base || strings.HasPrefix(target, base+"/")):
			return true
		}
	}
	return false
}

// sameProjectRoot compares two project roots as the log spells them: either
// separator, no trailing one, and case-blind (a Windows box's paths are).
func sameProjectRoot(a, b string) bool {
	norm := func(p string) string { return strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/") }
	a, b = norm(a), norm(b)
	return a != "" && strings.EqualFold(a, b)
}

// isTrunkLane is a lane name that is a trunk's: the primary checkout's.
func isTrunkLane(lane string) bool {
	return lane == "main" || lane == "master" || lane == "@trunk"
}

// overrideWords are the words an override of a shadowed rule's wall carries in its
// name, beside the live rule's own name.
var overrideWords = map[string][]string{
	"primary-write": {"primary"},
	"discard-work":  {"discard"},
	"rerun-suite":   {"override-bash-"},
	"bypass-verb":   {"direct-pr", "pr-open"},
	"attribution":   {"undercover"},
	"red-green":     {"override-off"},
	"stop-red":      {"override-off"},
}

// overrideAbout reports whether the override waives the rule the fire was about:
// the aphrollo rule's own name or the shadowed rule's wall, never an unrelated
// escape on the same lane.
func overrideAbout(o, fire stamped) bool {
	name := o.Detail["override"]
	if name == "" {
		name = o.Verdict
	}
	needles := slices.Clone(overrideWords[fire.Detail["rule"]])
	if live := fire.Detail["live_rule"]; live != "" {
		needles = append(needles, live, strings.TrimPrefix(live, "ratchet:"))
	}
	return slices.ContainsFunc(needles, func(n string) bool { return n != "" && strings.Contains(name, n) })
}

func windowText(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return d.String()
}

// Text is the shadow section as the plain lines `aphrollo stats --shadow` prints.
func (s Shadow) Text() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	const cont = "                       " // under a row's first count
	p("%-22s%d (%s)", "shadow fires", s.Fires, s.Window)
	for _, n := range s.Notes {
		p("%-22s%s", "  note", n)
	}
	p("%-22s%s", "  wrong block", fmt.Sprintf("an override of the same rule within %.0f min of the fire on its lane", WrongBlockWindow.Minutes()))
	p("%-22s%s", "  catch / pass", fmt.Sprintf("a later commit gate refusal, red CI or escape / the lane's merge, within %d days", ShadowHorizonDays))
	for _, r := range s.Rules {
		p("%-22s %d fires  agree %d  would-be block %d  softer %d (%d held out)  mismatch %d  not comparable %d  unjudged %d  held out %d",
			r.Rule, r.Fires, r.Agree, r.Stricter, r.Softer, r.SoftHeldOut, r.Mismatch, r.NotComparable, r.Unjudged, r.HeldOut)
		if r.Stricter > 0 {
			p("%swould-be blocks: catches %d  wrong %d  passes %d  open %d", cont, r.Catches, r.Wrong, r.Passes, r.Open)
		}
		if r.Overruns > 0 {
			p("%sbudget overruns %d: records that did not finish inside the hook's budget, counted unjudged", cont, r.Overruns)
		}
		if rate := r.Rate(); rate != "" {
			p("%sagreement %s of %d fires", cont, rate, r.Fires)
		} else {
			p("%sunder %d fires: no rate", cont, MinShadowFires)
		}
	}
	return b.String()
}
