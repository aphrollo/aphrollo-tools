package engine

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"pgregory.net/rapid"
)

var (
	factKinds = []kernel.Kind{
		kernel.KindLaneOpened, kernel.KindLaneEntered, kernel.KindLaneLeft, kernel.KindEdit,
		kernel.KindRunRequested, kernel.KindRunResult, kernel.KindCommitGated, kernel.KindPROpened,
		kernel.KindCIVerdict, kernel.KindPush, kernel.KindLaneMerged, kernel.KindLaneRemoved,
		kernel.KindLaneTick, kernel.KindEscape, "garbage",
	}
	questionKinds = []kernel.Kind{kernel.KindPreTool, kernel.KindPreCommit, kernel.KindPrePush, kernel.KindPreMerge, kernel.KindStop}
	propLanes     = []string{"fix", "other"}
	propTrees     = []string{"", "t1", "t2", "t3"}
	propUnits     = []string{"", "pkg/a", "pkg/b"}
)

func genFact() *rapid.Generator[kernel.Event] {
	return rapid.Custom(func(t *rapid.T) kernel.Event {
		return kernel.Event{
			Kind:       rapid.SampledFrom(factKinds).Draw(t, "kind"),
			Lane:       rapid.SampledFrom(propLanes).Draw(t, "lane"),
			Actor:      rapid.SampledFrom([]string{"", "s1/a", "s2/a"}).Draw(t, "actor"),
			Worktree:   "wt",
			Base:       "main",
			Head:       rapid.SampledFrom([]string{"", "h1", "h2"}).Draw(t, "head"),
			Source:     rapid.SampledFrom([]string{"", kernel.SourceAPI, kernel.SourceLocal}).Draw(t, "source"),
			OS:         rapid.SampledFrom([]string{"linux", "windows"}).Draw(t, "os"),
			Conclusion: rapid.SampledFrom([]kernel.Conclusion{"", kernel.CIStarted, kernel.CIGreen, kernel.CIRed}).Draw(t, "conclusion"),
			Required:   rapid.SampledFrom([][]string{nil, {"linux"}, {"linux", "windows"}}).Draw(t, "required"),
			TreeClean:  rapid.Bool().Draw(t, "clean"),
			Locked:     rapid.Bool().Draw(t, "locked"),
			Unit:       rapid.SampledFrom(propUnits).Draw(t, "unit"),
			Tree:       rapid.SampledFrom(propTrees).Draw(t, "tree"),
			Job:        rapid.SampledFrom([]string{"", "j1", "j2"}).Draw(t, "job"),
			File:       rapid.SampledFrom([]kernel.FileClass{"", kernel.ClassCode, kernel.ClassTest, kernel.ClassOther}).Draw(t, "file"),
			Test:       rapid.SampledFrom([]string{"", "TestA", "TestB"}).Draw(t, "test"),
			Removed:    rapid.Bool().Draw(t, "removed"),
			Covered:    rapid.Bool().Draw(t, "covered"),
			AddsSymbol: rapid.Bool().Draw(t, "addsSymbol"),
			Verdict:    rapid.SampledFrom([]kernel.Verdict{"", kernel.VerdictGreen, kernel.VerdictRed, kernel.VerdictRedBogus, kernel.VerdictNotTested}).Draw(t, "verdict"),
			Cause:      rapid.SampledFrom([]string{"", kernel.CauseTimeout, kernel.CauseDeferred}).Draw(t, "cause"),
			Gated:      rapid.Bool().Draw(t, "gated"),
			Failure:    rapid.SampledFrom([]string{"", kernel.FailureTest, kernel.FailureFlaky}).Draw(t, "failure"),
			Stage:      rapid.SampledFrom([]string{"", kernel.StageTrunk}).Draw(t, "stage"),
			EscapeID:   rapid.SampledFrom([]string{"", "esc-1"}).Draw(t, "escape"),
			Closes:     rapid.SampledFrom([][]string{nil, {"esc-1"}}).Draw(t, "closes"),
		}
	})
}

func genQuestion() *rapid.Generator[kernel.Event] {
	return rapid.Custom(func(t *rapid.T) kernel.Event {
		var cmds kernel.Cmd
		for _, b := range []kernel.Cmd{kernel.CmdWrite, kernel.CmdBypassGate, kernel.CmdDiscard, kernel.CmdNoisy} {
			if rapid.Bool().Draw(t, "cmd") {
				cmds |= b
			}
		}
		return kernel.Event{
			Kind:       rapid.SampledFrom(questionKinds).Draw(t, "kind"),
			Lane:       rapid.SampledFrom(propLanes).Draw(t, "lane"),
			Actor:      "s1/a",
			Claude:     rapid.Bool().Draw(t, "claude"),
			Tool:       rapid.SampledFrom([]kernel.Tool{"", kernel.ToolWrite, kernel.ToolBash}).Draw(t, "tool"),
			Target:     rapid.SampledFrom([]kernel.PathClass{"", kernel.PathLane, kernel.PathPrimary}).Draw(t, "target"),
			Cmds:       cmds,
			Unit:       rapid.SampledFrom(propUnits).Draw(t, "unit"),
			Tree:       rapid.SampledFrom(propTrees).Draw(t, "tree"),
			File:       rapid.SampledFrom([]kernel.FileClass{"", kernel.ClassCode, kernel.ClassTest}).Draw(t, "file"),
			Test:       rapid.SampledFrom([]string{"", "TestA"}).Draw(t, "test"),
			Covered:    rapid.Bool().Draw(t, "covered"),
			AddsSymbol: rapid.Bool().Draw(t, "addsSymbol"),
			UnseenRed:  rapid.Bool().Draw(t, "unseenRed"),
		}
	})
}

// refMode is the mode the kernel runs the TDD machine under for a config with
// no pins: enforce and off as set, anything else warn.
func refMode(c kernel.Config) kernel.TDDMode {
	if c.TDD == kernel.ModeEnforce || c.TDD == kernel.ModeOff {
		return c.TDD
	}
	return kernel.ModeWarn
}

type pair struct {
	lane  kernel.State
	units kernel.Units
}

func genConfig() *rapid.Generator[kernel.Config] {
	return rapid.Custom(func(t *rapid.T) kernel.Config {
		return kernel.Config{TDD: rapid.SampledFrom([]kernel.TDDMode{"", kernel.ModeWarn, kernel.ModeEnforce, kernel.ModeOff}).Draw(t, "tdd")}
	})
}

// TestHandle_equalsFoldingKernelStepsDirectly holds the engine to the kernel:
// over any sequence of facts, across lanes, what Handle returns and what the
// store ends up holding is what folding Step and StepUnits by hand gives.
func TestHandle_equalsFoldingKernelStepsDirectly(t *testing.T) {
	ctx := bounded(t)
	rapid.Check(t, func(rt *rapid.T) {
		cfg := genConfig().Draw(rt, "config")
		eng, store := newEngine(t, cfg)
		events := rapid.SliceOfN(genFact(), 1, 30).Draw(rt, "events")
		ref := map[string]*pair{}
		for i, e := range events {
			e.At = t0.Add(time.Duration(i) * time.Minute)
			r := ref[e.Lane]
			if r == nil {
				r = &pair{}
				ref[e.Lane] = r
			}
			machines := e
			machines.Mode = refMode(cfg)
			wantLane, laneFx := kernel.Step(r.lane, machines)
			wantUnits, unitFx := kernel.StepUnits(r.units, machines)
			r.lane, r.units = wantLane, wantUnits

			d, err := eng.Handle(ctx, e)
			if err != nil {
				rt.Fatalf("event %d (%s): %v", i, e.Kind, err)
			}
			if !reflect.DeepEqual(d.Lane, wantLane) || !reflect.DeepEqual(d.Units, wantUnits) {
				rt.Fatalf("event %d (%s): decision state differs from the direct fold\n got %+v / %+v\nwant %+v / %+v", i, e.Kind, d.Lane, d.Units, wantLane, wantUnits)
			}
			if want := slices.Concat(laneFx, unitFx); !reflect.DeepEqual(d.Effects, want) {
				rt.Fatalf("event %d (%s): effects = %+v, want %+v", i, e.Kind, d.Effects, want)
			}
		}
		for name, r := range ref {
			got, _ := load(t, store, name)
			if !reflect.DeepEqual(got.Lane, r.lane) || !reflect.DeepEqual(got.Units, r.units) {
				rt.Fatalf("lane %q: stored record differs from the direct fold\n got %+v / %+v\nwant %+v / %+v", name, got.Lane, got.Units, r.lane, r.units)
			}
		}
	})
}

// quiet is the units with the guided-once flags cleared, the one thing a question
// may change, and the units that held nothing else dropped: a question can leave
// a record that only carries a flag.
func quiet(us kernel.Units) kernel.Units {
	out := kernel.Units{}
	for name, u := range us {
		u.GuidedUntested, u.GuidedHeld = false, false
		if u != (kernel.Unit{}) {
			out[name] = u
		}
	}
	return out
}

// TestHandle_questionsChangeNothingButTheGuidedFlags holds that a question
// moves no lane and no unit: with questions mixed into any run of facts, the
// lane is the facts-only fold and each unit differs from it in at most its
// guided-once flags.
func TestHandle_questionsChangeNothingButTheGuidedFlags(t *testing.T) {
	ctx := bounded(t)
	rapid.Check(t, func(rt *rapid.T) {
		cfg := genConfig().Draw(rt, "config")
		eng, store := newEngine(t, cfg)
		events := rapid.SliceOfN(rapid.OneOf(genFact(), genQuestion()), 1, 30).Draw(rt, "events")
		ref := map[string]*pair{}
		for i, e := range events {
			e.At = t0.Add(time.Duration(i) * time.Minute)
			r := ref[e.Lane]
			if r == nil {
				r = &pair{}
				ref[e.Lane] = r
			}
			before, _ := load(t, store, e.Lane)
			if _, err := eng.Handle(ctx, e); err != nil {
				rt.Fatalf("event %d (%s): %v", i, e.Kind, err)
			}
			if e.Kind.Question() {
				after, _ := load(t, store, e.Lane)
				if !reflect.DeepEqual(before.Lane, after.Lane) {
					rt.Fatalf("event %d (%s) moved the lane:\n before %+v\n after  %+v", i, e.Kind, before.Lane, after.Lane)
				}
				if len(after.Units) != len(before.Units) {
					rt.Fatalf("event %d (%s) changed the units known to the facts:\n before %+v\n after  %+v", i, e.Kind, before.Units, after.Units)
				}
				continue
			}
			machines := e
			machines.Mode = refMode(cfg)
			r.lane, _ = kernel.Step(r.lane, machines)
			r.units, _ = kernel.StepUnits(r.units, machines)
		}
		for name, r := range ref {
			got, _ := load(t, store, name)
			if !reflect.DeepEqual(got.Lane, r.lane) {
				rt.Fatalf("lane %q: %+v, want the facts-only fold %+v", name, got.Lane, r.lane)
			}
			if !reflect.DeepEqual(quiet(got.Units), quiet(r.units)) {
				rt.Fatalf("lane %q: units %+v differ from the facts-only fold %+v beyond the guided flags", name, got.Units, r.units)
			}
		}
	})
}
