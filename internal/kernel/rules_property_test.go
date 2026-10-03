package kernel

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"pgregory.net/rapid"
)

var ruleKinds = []Kind{
	KindPreTool, KindPreCommit, KindPrePush, KindPreMerge, KindStop,
	KindEdit, KindRunResult, KindCommitGated, KindLaneMerged, KindCIVerdict, "garbage",
}

var cmdBits = []Cmd{
	CmdWrite, CmdBypassGate, CmdMoveOffTrunk, CmdPushTrunk, CmdMergePR,
	CmdDiscard, CmdOutward, CmdLongWait, CmdRerun, CmdNoisy,
}

func genRuleEvent() *rapid.Generator[Event] {
	return rapid.Custom(func(t *rapid.T) Event {
		var cmds Cmd
		for _, b := range cmdBits {
			if rapid.Bool().Draw(t, "cmd") {
				cmds |= b
			}
		}
		return Event{
			Kind:        rapid.SampledFrom(ruleKinds).Draw(t, "kind"),
			Lane:        rapid.SampledFrom([]string{"", TrunkLane, "fix", "arm-1", "arm-2", "arm-3"}).Draw(t, "lane"),
			Actor:       "s/a",
			At:          t0,
			Claude:      rapid.Bool().Draw(t, "claude"),
			Tool:        rapid.SampledFrom([]Tool{"", ToolWrite, ToolBash, "odd"}).Draw(t, "tool"),
			Target:      rapid.SampledFrom([]PathClass{"", PathLane, PathPrimary, PathOther, "odd"}).Draw(t, "target"),
			Cmds:        cmds,
			NonMerge:    rapid.Bool().Draw(t, "nonMerge"),
			LawHit:      rapid.SampledFrom([]string{"", "no-panic"}).Draw(t, "law"),
			LawDeny:     rapid.Bool().Draw(t, "lawDeny"),
			Secret:      rapid.Bool().Draw(t, "secret"),
			Attribution: rapid.Bool().Draw(t, "attribution"),
			Failed:      rapid.SampledFrom([]Check{"", CheckProof, CheckVet, CheckLint, CheckDocs, CheckSuppress, CheckBaseline, "odd"}).Draw(t, "failed"),
			Survivors:   rapid.IntRange(0, 3).Draw(t, "survivors"),
			GreenOS:     rapid.SliceOfN(rapid.SampledFrom([]string{"linux", "windows"}), 0, 2).Draw(t, "greenOS"),
			UnseenRed:   rapid.Bool().Draw(t, "unseenRed"),
			StopActive:  rapid.Bool().Draw(t, "stopActive"),
			Unit:        rapid.SampledFrom([]string{"", "pkg/a", "pkg/b"}).Draw(t, "unit"),
			Tree:        rapid.SampledFrom(unitTrees).Draw(t, "tree"),
			File:        rapid.SampledFrom([]FileClass{"", ClassCode, ClassTest, ClassOther}).Draw(t, "file"),
			Test:        rapid.SampledFrom(unitTests).Draw(t, "test"),
			Covered:     rapid.Bool().Draw(t, "covered"),
			AddsSymbol:  rapid.Bool().Draw(t, "addsSymbol"),
			Verdict:     rapid.SampledFrom([]Verdict{"", VerdictGreen, VerdictRed, VerdictRedBogus, VerdictNotTested}).Draw(t, "verdict"),
			Cause:       rapid.SampledFrom([]string{"", CauseTimeout, CauseDeferred}).Draw(t, "cause"),
			Conclusion:  rapid.SampledFrom([]Conclusion{"", CIGreen, CIRed}).Draw(t, "conclusion"),
		}
	})
}

var pinLevels = []Level{LevelOff, LevelWarn, LevelEnforce, "odd"}

func genConfig() *rapid.Generator[Config] {
	return rapid.Custom(func(t *rapid.T) Config {
		c := Config{
			TDD:         rapid.SampledFrom([]TDDMode{"", ModeWarn, ModeEnforce, ModeOff, "odd"}).Draw(t, "tdd"),
			NoIsolation: rapid.Bool().Draw(t, "noIsolation"),
			Undercover:  rapid.Bool().Draw(t, "undercover"),
			Mutation:    rapid.SampledFrom([]Level{"", LevelOff, LevelWarn, LevelEnforce}).Draw(t, "mutation"),
			CIOS:        rapid.SliceOfN(rapid.SampledFrom([]string{"linux", "windows"}), 0, 2).Draw(t, "ciOS"),
		}
		for _, id := range allRuleIDs() {
			if rapid.IntRange(0, 3).Draw(t, "pinned") == 0 {
				if c.Rules == nil {
					c.Rules = map[string]Level{}
				}
				c.Rules[id] = rapid.SampledFrom(pinLevels).Draw(t, "pin")
			}
		}
		return c
	})
}

func genRuleLane() *rapid.Generator[State] {
	return rapid.Custom(func(t *rapid.T) State {
		return State{
			Branch:     rapid.SampledFrom([]string{"", TrunkLane, "fix", "arm-1"}).Draw(t, "branch"),
			Life:       rapid.SampledFrom([]Life{"", LifeNone, LifeOpen, LifePR, LifeCIGreen, LifeMerged}).Draw(t, "life"),
			CIRequired: rapid.SliceOfN(rapid.SampledFrom([]string{"linux", "windows"}), 0, 2).Draw(t, "required"),
		}
	})
}

func genRuleUnits() *rapid.Generator[Units] {
	return rapid.Custom(func(t *rapid.T) Units {
		us := Units{}
		for _, name := range []string{"pkg/a", "pkg/b", "pkg/c"} {
			if rapid.Bool().Draw(t, "has") {
				us[name] = genUnit().Draw(t, name)
			}
		}
		return us
	})
}

type ruleInput struct {
	l  State
	us Units
	e  Event
	c  Config
}

func genRuleInput() *rapid.Generator[ruleInput] {
	return rapid.Custom(func(t *rapid.T) ruleInput {
		return ruleInput{genRuleLane().Draw(t, "lane"), genRuleUnits().Draw(t, "units"), genRuleEvent().Draw(t, "event"), genConfig().Draw(t, "config")}
	})
}

func (in ruleInput) decide() Decision { return Decide(in.l, in.us, in.e, in.c) }

func TestDecide_isTotalDeterministicAndOrderFree(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := genRuleInput().Draw(t, "input")
		first := in.decide()
		switch first.Outcome {
		case OutcomeAllow, OutcomeGuide, OutcomeDeny, OutcomeAsk:
		default:
			t.Fatalf("outcome %q is outside the set", first.Outcome)
		}
		// Go randomises map iteration per range: many calls over maps with several keys
		// would disagree if any decision read them in iteration order.
		for range 10 {
			if again := in.decide(); !reflect.DeepEqual(first, again) {
				t.Fatalf("two calls disagree:\n%+v\n%+v", first, again)
			}
		}
		rebuilt := in
		rebuilt.us = Units{}
		for _, k := range slices.Backward(slices.Sorted(maps.Keys(in.us))) {
			rebuilt.us[k] = in.us[k]
		}
		rebuilt.c.Rules = map[string]Level{}
		for _, k := range slices.Backward(slices.Sorted(maps.Keys(in.c.Rules))) {
			rebuilt.c.Rules[k] = in.c.Rules[k]
		}
		if again := rebuilt.decide(); !reflect.DeepEqual(first, again) {
			t.Fatalf("a map built in another order changed the decision:\n%+v\n%+v", first, again)
		}
	})
}

func TestDecide_aRulePinnedOffNeverFires(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := genRuleInput().Draw(t, "input")
		id := rapid.SampledFrom(allRuleIDs()).Draw(t, "rule")
		in.c.Rules = maps.Clone(in.c.Rules)
		if in.c.Rules == nil {
			in.c.Rules = map[string]Level{}
		}
		in.c.Rules[id] = LevelOff
		if d := in.decide(); d.Rule == id {
			t.Fatalf("rule %s at off fired: %+v", id, d)
		}
	})
}

func TestDecide_noRuleDeniesWhenEveryRuleIsPinnedToWarn(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := genRuleInput().Draw(t, "input")
		in.c.Rules = map[string]Level{}
		for _, id := range allRuleIDs() {
			in.c.Rules[id] = LevelWarn
		}
		if d := in.decide(); d.Outcome == OutcomeDeny {
			t.Fatalf("a deny under warn everywhere: %+v", d)
		}
	})
}

func TestDecide_aDenyIsCompleteLiveAndOnlyForWhoMayBeBlocked(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := genRuleInput().Draw(t, "input")
		d := in.decide()
		if d.Outcome != OutcomeDeny {
			if d.Outcome == OutcomeAllow && d.Rule != "" {
				t.Fatalf("an allow names rule %q", d.Rule)
			}
			return
		}
		r := ruleByID(d.Rule)
		switch {
		case d.Cause == "" || d.Next == "" || d.Override == "" || d.Section == "":
			t.Fatalf("a deny without its rule, cause, next step or override: %+v", d)
		case r.Do != OutcomeDeny || r.Class == RuleGuide:
			t.Fatalf("rule %s may not block but denied: %+v", d.Rule, d)
		case d.Level != LevelEnforce || d.HeldOut || d.WouldDeny:
			t.Fatalf("a deny at level %q (held out %v, would-deny %v)", d.Level, d.HeldOut, d.WouldDeny)
		case !in.e.Kind.Question():
			t.Fatalf("a deny on %q, an event that is a fact and cannot be refused", in.e.Kind)
		case !in.e.Claude && !r.AllAuthors:
			t.Fatalf("a deny of a human by %s", d.Rule)
		}
	})
}

func TestDecide_theHoldoutArmDependsOnlyOnTheLaneAndTheRule(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := genRuleInput().Draw(t, "input")
		d := in.decide()
		if d.HeldOut {
			if d.Outcome != OutcomeGuide || !d.WouldDeny || !heldOut(first(in.e.Lane, in.l.Branch), d.Rule) || ruleByID(d.Rule).Class != RuleEarned {
				t.Fatalf("an incoherent held-out decision: %+v at lane %q", d, in.e.Lane)
			}
			if _, pinned := in.c.pin(d.Rule); pinned {
				t.Fatalf("a pinned rule %s was shadowed", d.Rule)
			}
		}
		// another actor and another time, same lane and rule: the same arm
		other := in
		other.e.Actor, other.e.At = "s2/a2", t0.AddDate(0, 0, 3)
		if o := other.decide(); o.Rule == d.Rule && o.HeldOut != d.HeldOut {
			t.Fatalf("the arm of %s at lane %q moved with actor and time", d.Rule, in.e.Lane)
		}
	})
}

func TestDecide_aQuestionNeverAdvancesAMachine(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := genRuleInput().Draw(t, "input")
		in.e.Kind = rapid.SampledFrom([]Kind{KindPreTool, KindPreCommit, KindPrePush, KindPreMerge, KindStop}).Draw(t, "question")
		d := in.decide()
		if !reflect.DeepEqual(d.Lane, in.l) || !reflect.DeepEqual(d.Units, in.us) || len(d.Effects) != 0 {
			t.Fatalf("a %s question moved state or asked for effects: %+v", in.e.Kind, d)
		}
	})
}
