package kernel

import (
	"maps"
	"slices"
)

// TrunkLane is the key of the primary checkout on trunk (§3 "Lane").
const TrunkLane = "@trunk"

// Level is a rule's level (§3 Rule, §5 "Level", §7 `[rules]`). The adapter that
// reads the config maps the document's words onto these: `block` is enforce,
// `warn` and `guide` are both warn here, because neither ever denies.
type Level string

const (
	LevelOff     Level = "off"
	LevelWarn    Level = "warn"
	LevelEnforce Level = "enforce"
)

func (l Level) valid() bool { return l == LevelOff || l == LevelWarn || l == LevelEnforce }

// Outcome is what the table decides. OutcomeAsk is reserved: no line of §3 to
// §5 asks, so no row does.
type Outcome string

const (
	OutcomeAllow Outcome = "allow"
	OutcomeGuide Outcome = "guide"
	OutcomeAsk   Outcome = "ask"
	OutcomeDeny  Outcome = "deny"
)

// rank orders outcomes for the precedence: the strongest wins.
func (o Outcome) rank() int {
	return slices.Index([]Outcome{OutcomeAllow, OutcomeGuide, OutcomeAsk, OutcomeDeny}, o)
}

// RuleClass is the row of the §5 level table a rule sits in; the order of the
// classes is the order of the table.
type RuleClass string

const (
	RuleWall   RuleClass = "wall"   // block always: real damage, never demoted, never shadowed
	RuleEarned RuleClass = "earned" // block, earned: shadowed 10% of the time, demotable
	RulePinned RuleClass = "pinned" // blocks only where a repo pins block: never shadowed
	RuleGuide  RuleClass = "guide"  // additionalContext, never a deny
)

// Tool is the kind of tool a tool.pre asks about.
type Tool string

const (
	ToolWrite Tool = "write" // Edit, Write, MultiEdit, NotebookEdit
	ToolBash  Tool = "bash"  // Bash, PowerShell, and the git and cargo shims
)

// PathClass is where a write or command lands, as the adapter resolved it. The
// zero value is unknown, which is never the primary checkout.
type PathClass string

const (
	PathLane    PathClass = "lane"
	PathPrimary PathClass = "primary" // the main checkout
	PathOther   PathClass = "other"
)

// Cmd is a set of what a Bash or PowerShell command does, parsed by the adapter.
type Cmd uint16

const (
	CmdWrite        Cmd = 1 << iota // writes a path
	CmdBypassGate                   // --no-verify, -c core.hooksPath
	CmdMoveOffTrunk                 // checkout -b, switch -c, a move off trunk
	CmdPushTrunk                    // pushes to trunk
	CmdMergePR                      // gh pr merge
	CmdDiscard                      // discards uncommitted work
	CmdOutward                      // an outward call a verb covers
	CmdLongWait                     // a long foreground wait
	CmdRerun                        // re-runs a suite the run already covered
	CmdNoisy                        // noisy output
)

func (c Cmd) has(set Cmd) bool { return c&set != 0 }

// Check is the commit-gate stage that failed (§4 pre-commit).
type Check string

const (
	CheckProof    Check = "proof"
	CheckVet      Check = "vet"
	CheckLint     Check = "lint"
	CheckDocs     Check = "docs"
	CheckSuppress Check = "suppress"
	CheckBaseline Check = "baseline"
)

// Config is what the rule table reads of the config (§7). Its zero value is
// the document's defaults: tdd warn, isolation on, not undercover, mutation
// off, ci.os ["linux"], no pins.
type Config struct {
	TDD         TDDMode
	NoIsolation bool     // isolation = false
	Undercover  bool     // undercover = true
	Mutation    Level    // `mutation`: off, warn (guide) or enforce (block)
	CIOS        []string // ci.os
	Rules       map[string]Level
}

// pin is the level `[rules]` pins a rule to. A value that is no level is no
// pin at all: it falls back to the default, never to off (§7 "No silent misreads").
func (c Config) pin(id string) (Level, bool) {
	lv, ok := c.Rules[id]
	return lv, ok && lv.valid()
}

// tddMode is the mode the TDD machine runs under: the `tdd` key, unless the
// red-green rule is pinned. An unknown mode reads as warn.
func (c Config) tddMode() TDDMode {
	if lv, ok := c.pin("red-green"); ok {
		return map[Level]TDDMode{LevelEnforce: ModeEnforce, LevelWarn: ModeWarn, LevelOff: ModeOff}[lv]
	}
	if c.TDD == ModeEnforce || c.TDD == ModeOff {
		return c.TDD
	}
	return ModeWarn
}

// level is a rule's level, and whether a pin set it. Without a pin a row's own
// reading of the config decides, else the class default: a deny rule enforces,
// a guide rule warns.
func (c Config) level(r Rule) (Level, bool) {
	if lv, ok := c.pin(r.ID); ok {
		return lv, true
	}
	switch {
	case r.Level != nil:
		return r.Level(c), false
	case r.Class == RuleGuide:
		return LevelWarn, false
	}
	return LevelEnforce, false
}

// Facts is what a rule reads: the lane and units as they stood, the event, the
// config, and the codes of the guides the TDD machine gave for the event.
type Facts struct {
	Lane   State
	Units  Units
	Event  Event
	Config Config
	Guides []string
}

func (f Facts) guided(codes ...string) bool {
	return slices.ContainsFunc(f.Guides, func(g string) bool { return slices.Contains(codes, g) })
}

// laneID is the lane the event is about: the one it names, else the lane's own.
func (f Facts) laneID() string { return first(f.Event.Lane, f.Lane.Branch) }

// Rule is one row of the rule table (§5): the facts it reads, what it decides,
// the level it holds, the override it offers and the document section it
// comes from. Reads returns whether the row fires and a detail naming what it
// found; Level, when set, is the row's reading of the config (a key such as
// `isolation` or `tdd` that moves it), under any pin.
type Rule struct {
	ID, Section string
	Class       RuleClass
	Do          Outcome // OutcomeDeny where the document lets the rule block, else OutcomeGuide
	Cause, Next string  // the deny line's cause and next step
	Override    string  // what a deny offers; "" for a guide
	AllAuthors  bool    // blocks a human too (§5: a secret blocks for every author)
	Level       func(Config) Level
	Reads       func(Facts) (detail string, hit bool)
}

// Decision is the rule table's answer for one event, with what the machines
// made of it. For a question the machines are only consulted: Lane and Units
// are the inputs, unchanged, and no effects come out; the engine steps them
// with the fact that follows.
type Decision struct {
	Outcome  Outcome
	Rule     string // "" for an allow
	Section  string
	Cause    string
	Next     string
	Override string
	Detail   string
	Level    Level
	// WouldDeny marks a guide the rule would have made a deny: its level was
	// warn, the actor is a human, the event cannot be refused, or the holdout
	// shadowed it. HeldOut marks the last, so measure can compare the arms.
	WouldDeny bool
	HeldOut   bool

	Lane    State
	Units   Units
	Effects []Effect
}

// Decide is the whole kernel in one pure call: the lane machine, the TDD
// machine and the rule table (§2). The precedence is fixed:
//
//  1. Rows are read in table order, which is the §4 PreToolUse order: walls,
//     earned blocks, pinned blocks, guides.
//  2. A row at level off does not fire. At warn, or for a human, or for an
//     event that is a fact, a deny row only guides and says WouldDeny.
//  3. The strongest outcome wins: deny over ask over guide over allow; among
//     equals the earlier row wins, so one line comes out.
//  4. An earned block pinned to no level is shadowed in 10% of lanes (heldOut):
//     its deny becomes a guide marked HeldOut, which loses to any later deny.
func Decide(l State, us Units, e Event, c Config) Decision {
	e.Mode = c.tddMode()
	machines := e
	if e.Kind == KindPreTool && e.Tool == ToolWrite {
		machines.Kind = KindEdit // the edit about to happen, to read what the TDD machine says of it
	}
	nextLane, laneFx := Step(l, machines)
	nextUnits, unitFx := StepUnits(us, machines)
	f := Facts{Lane: l, Units: us, Event: e, Config: c}
	for _, fx := range unitFx {
		if fx.Kind == EffectGuide {
			f.Guides = append(f.Guides, fx.Detail)
		}
	}
	best := Decision{Outcome: OutcomeAllow}
	for _, r := range ruleTable {
		if d, ok := judge(r, f); ok && d.Outcome.rank() > best.Outcome.rank() {
			best = d
		}
	}
	if e.Kind.question() {
		best.Lane, best.Units = l, us
		return best
	}
	best.Lane, best.Units, best.Effects = nextLane, nextUnits, slices.Concat(laneFx, unitFx)
	return best
}

// judge fires one row against the facts: ok is false for a row that does not
// read its facts or is off.
func judge(r Rule, f Facts) (Decision, bool) {
	detail, hit := r.Reads(f)
	if !hit {
		return Decision{}, false
	}
	lv, pinned := f.Config.level(r)
	if lv == LevelOff {
		return Decision{}, false
	}
	d := Decision{Outcome: OutcomeGuide, Rule: r.ID, Section: r.Section, Cause: r.Cause, Next: r.Next,
		Override: r.Override, Detail: detail, Level: lv}
	if r.Do != OutcomeDeny {
		return d, true
	}
	switch {
	case lv != LevelEnforce || !f.Event.Kind.question() || (!f.Event.Claude && !r.AllAuthors):
		d.WouldDeny = true
	case r.Class == RuleEarned && !pinned && heldOut(f.laneID(), r.ID):
		d.WouldDeny, d.HeldOut = true, true
	default:
		d.Outcome = OutcomeDeny
	}
	return d, true
}

// heldOut is the 10% holdout arm (§5 "The holdout"): FNV-1a over the lane id,
// a NUL and the rule id, one hash in ten. The same lane always lands in the
// same arm for a rule, and the arm reads nothing else.
func heldOut(lane, rule string) bool {
	h := uint32(2166136261)
	for _, b := range []byte(lane + "\x00" + rule) {
		h = (h ^ uint32(b)) * 16777619
	}
	return h%10 == 0
}

// firstOpen is the first unit, by name, with a real red outstanding.
func firstOpen(us Units) (string, bool) {
	for _, name := range slices.Sorted(maps.Keys(us)) {
		if us[name].Phase == PhaseOpen {
			return name, true
		}
	}
	return "", false
}

// missingOS is the declared OSes (the lane's, else ci.os, else linux) that have
// no green verdict for the merged tree, sorted and joined by commas.
func missingOS(f Facts) string {
	declared := slices.Compact(slices.Sorted(slices.Values(
		cmpOr(f.Lane.CIRequired, f.Config.CIOS, []string{"linux"}))))
	out := ""
	for _, os := range declared {
		if slices.Contains(f.Event.GreenOS, os) {
			continue
		}
		if out != "" {
			out += ","
		}
		out += os
	}
	return out
}

// cmpOr is the first non-empty list.
func cmpOr(lists ...[]string) []string {
	for _, l := range lists {
		if len(l) > 0 {
			return l
		}
	}
	return nil
}
