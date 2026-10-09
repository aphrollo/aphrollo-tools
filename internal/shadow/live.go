package shadow

import (
	"context"
	"path/filepath"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// LiveBudget is the least a PreToolUse hook waits for the kernel's answer to the
// red→green question before it says nothing (docs/trellis-architecture.md §4:
// 50 ms, with no law check). A repo whose decisions take longer on this box waits
// longer, up to BudgetCap (SizedBudget). Past it the question is dropped and told so
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
	// Spent is what the question had cost when it was answered or dropped, and Budget the
	// wait it was given.
	Spent, Budget time.Duration
	// Waived is set by the caller when the session had waived the deny the answer holds:
	// the hook allowed the write, and the record says so rather than a block.
	Waived bool
}

// RedGreenLive asks the kernel, under cfg, the red→green question of each code file a
// PreToolUse call is about to write, inside the wait SizedBudget gives (LiveBudget at
// least). It reads the lane's record
// and the edit ledger and spawns nothing. A question that does not finish inside the
// budget has no answer: the result says it overran and carries none.
func (w World) RedGreenLive(p Payload, files []string, cfg kernel.Config) LiveResult {
	clk := clock
	dir := w.stateDirOf(files)
	budget := SizedBudget(LiveBudget, readDecisions(dir))
	start := clk.Now()
	deadline := start.Add(budget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	expired, release := clk.Timer(budget)
	defer release()
	done := make(chan []askedFile, 1)
	go func() {
		var asked []askedFile
		defer func() { _ = recover(); done <- asked }() // a panic is no answer
		asked = w.askCodeFiles(ctx, p, files, cfg)
	}()
	var asked []askedFile
	answered := false
	select {
	case asked = <-done:
		answered = true
	case <-expired:
	}
	now := clk.Now()
	spent := now.Sub(start)
	noteDecision(dir, spent) // a dropped one took at least the budget
	res := LiveResult{Spent: spent, Budget: budget}
	if !answered || !now.Before(deadline) {
		res.Overran = true
		return res
	}
	res.Asked = liveOf(asked)
	return res
}

// firstCodeFile is the first code file of a call's files, the one a dropped decision names.
func firstCodeFile(files []string) string {
	for _, f := range files {
		if FileClassOf(f) == kernel.ClassCode {
			return f
		}
	}
	return ""
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
		if a := l.Action(); a != Allow && (!live.Waived || a != Block) {
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
				r := unjudged(HookPre, RuleRedGreen, "", CauseBudget)
				r.File, r.Spent, r.Budget = firstCodeFile(files), live.Spent, live.Budget
				return []core.Event{r.event(s, wd.Lane(s.Root))}
			}
			recs := wd.RedGreenAs(ctx, p, files, actual)
			evs := make([]core.Event, 0, len(recs))
			for _, r := range recs {
				rs := s
				if r.Root != "" {
					rs.Root = r.Root
				}
				r.Waived = live.Waived && r.Trellis == "block"
				evs = append(evs, r.event(rs, wd.Lane(rs.Root)))
			}
			return evs
		},
		skip: unjudgedFor,
	}}
}
