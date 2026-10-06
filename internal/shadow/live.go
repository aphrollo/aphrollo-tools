package shadow

import (
	"context"
	"path/filepath"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// LiveBudget is how long a PreToolUse hook waits for the kernel's answer to the
// red→green question before it says nothing (docs/trellis-architecture.md §4:
// 50 ms, with no law check). Past it the question is dropped and told so
// (LiveResult.Overran), never guessed.
var LiveBudget = 50 * time.Millisecond

// Live is the kernel's answer, under the mode a lane runs in, to the question one
// code file about to be written puts to the red→green rule.
type Live struct {
	File, Root                    string
	Unit, UnitPkg, UnitRoot, Lang string
	Decision                      kernel.Decision
}

// Fires reports whether the red→green rule fired: a guide or a deny, whoever it
// was shadowed for. Another rule the same event reads (an escape hold) is not this
// one's answer.
func (l Live) Fires() bool {
	return l.Decision.Rule == RuleRedGreen && (l.Decision.Outcome == kernel.OutcomeGuide || l.Decision.Outcome == kernel.OutcomeDeny)
}

// Action is what the live hook does about the file: block on a deny, warn on a guide,
// else allow.
func (l Live) Action() Action {
	switch {
	case !l.Fires():
		return Allow
	case l.Decision.Outcome == kernel.OutcomeDeny:
		return Block
	}
	return Warn
}

// LiveResult is the answers of one call, one per code file of a followed lane (a unit
// asked twice in a call is asked once), or an overrun.
type LiveResult struct {
	Asked   []Live
	Overran bool
}

// RedGreenLive asks the kernel, under cfg, the red→green question of each code file a
// PreToolUse call is about to write, inside LiveBudget. It reads the lane's record
// and the edit ledger and spawns nothing. A question that does not finish inside the
// budget has no answer: the result says it overran and carries none.
func (w World) RedGreenLive(p Payload, files []string, cfg kernel.Config) LiveResult {
	clk := clock
	deadline := clk.Now().Add(LiveBudget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	expired, release := clk.Timer(LiveBudget)
	defer release()
	done := make(chan []askedFile, 1)
	go func() {
		var asked []askedFile
		defer func() { _ = recover(); done <- asked }() // a panic is no answer
		asked = w.askCodeFiles(ctx, p, files, cfg)
	}()
	select {
	case asked := <-done:
		if !clk.Now().Before(deadline) {
			return LiveResult{Overran: true}
		}
		return LiveResult{Asked: liveOf(asked)}
	case <-expired:
		return LiveResult{Overran: true}
	}
}

func liveOf(asked []askedFile) []Live {
	var out []Live
	for _, a := range asked {
		if a.Unjudged != nil {
			continue // the shadow record says why it could not be asked
		}
		l := Live{File: a.File, Root: a.Root, Unit: a.Unit.ID, UnitPkg: a.Unit.Pkg, Lang: LangOfUnit(a.Unit.ID), Decision: a.Decision}
		if a.Unit.Kind == unitProjectRoot {
			l.UnitRoot = filepath.ToSlash(a.ProjectRoot)
		}
		out = append(out, l)
	}
	return out
}

// RedGreenSteps is the step of a PreToolUse call that shadows red→green over the
// files it is about to write, for a call the live hook did not act on. It makes none
// for a call that writes no code or test file.
func RedGreenSteps(wd World, s Source, p Payload, files []string) []Step {
	return RedGreenStepsLive(wd, s, p, files, LiveResult{})
}

// RedGreenStepsLive is RedGreenSteps for a call the live hook answered: each record
// compares the kernel's enforce-level decision with what the hook did about its file
// (warned, blocked, or nothing), and carries the lane's arm. A call whose live answer
// outran its budget is one unjudged record for the budget, and the kernel is not
// asked a second time.
func RedGreenStepsLive(wd World, s Source, p Payload, files []string, live LiveResult) []Step {
	if !hasWrites(files) {
		return nil
	}
	actual := map[string]Action{}
	for _, l := range live.Asked {
		if a := l.Action(); a != Allow {
			actual[l.File] = a
		}
	}
	unjudgedFor := func(cause string) core.Event {
		return unjudged(HookPre, RuleRedGreen, "", cause).event(s, wd.Lane(s.Root))
	}
	return []Step{{
		run: func(ctx context.Context) []core.Event {
			if live.Overran {
				Overruns.Add(1)
				return []core.Event{unjudgedFor(CauseBudget)}
			}
			recs := wd.RedGreenAs(ctx, p, files, actual)
			evs := make([]core.Event, 0, len(recs))
			for _, r := range recs {
				rs := s
				if r.Root != "" {
					rs.Root = r.Root
				}
				evs = append(evs, r.event(rs, wd.Lane(rs.Root)))
			}
			return evs
		},
		skip: unjudgedFor,
	}}
}
