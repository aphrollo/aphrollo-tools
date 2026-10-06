// Package report folds the v1 event log into the weekly continuous-improvement
// report: where the gates cost the agent time, which blocks look wrong, which
// escapes got through and which stage should have caught them, the A/B and the
// shadow per arm and language, what the injected texts cost, and proposals.
// Every function is pure: events and the clock come in, a Report goes out. The
// report only proposes; nothing here changes a rule.
package report

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/measure"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// maxRefs caps the event seqs listed behind one row; the rest is a count, never
// dropped without saying so.
const maxRefs = 8

// Input is what a report is built from.
type Input struct {
	Events []tdd.Event
	Now    time.Time
	// Window is the span before Now the report covers; the whole log when 0.
	Window time.Duration
	// Repo names the repository in the title lines.
	Repo string
	// Briefs are the injected texts measured against their token caps.
	Briefs []measure.BriefLine
	// Usage is the session usage scanned from the harness's transcripts, nil to leave the section out; CompareAt, when set, adds its before and after.
	Usage     *UsageFacts
	CompareAt time.Time
}

// Refs are the event seqs behind a number, so `aphrollo why <seq>` can replay
// them: at most maxRefs, ascending, and how many more there are.
type Refs struct {
	Seqs []int64 `json:"seqs"`
	More int     `json:"more"`
}

func makeRefs(all []int64) Refs {
	s := slices.Clone(all)
	slices.Sort(s)
	s = slices.Compact(s)
	r := Refs{Seqs: s}
	if len(s) > maxRefs {
		r.Seqs, r.More = s[:maxRefs], len(s)-maxRefs
	}
	return r
}

// Friction is what one rule cost the week: its denies, the overrides that
// waived them, the refusals of the commit and merge gates, the runs that
// proved nothing, and the seconds the refused and untested stages took.
type Friction struct {
	Rule      string `json:"rule"`
	Denies    int    `json:"denies"`
	Overrides int    `json:"overrides"`
	Refusals  int    `json:"refusals"`
	NotTested int    `json:"not_tested"`
	// SecsLost is the gate time the agent waited on; SecsBackground the time the
	// gate ran while the agent kept working (deferred and queued runs).
	SecsLost       float64 `json:"secs_lost"`
	SecsBackground float64 `json:"secs_background"`
	Refs           Refs    `json:"refs"`
}

func (f Friction) total() int { return f.Denies + f.Overrides + f.Refusals + f.NotTested }

// WrongBlock is a rule's denies and how many were waived by an override within
// measure.WrongBlockWindow on the same lane: the override rate, which is the
// candidate for a block that is wrong.
type WrongBlock struct {
	Rule   string `json:"rule"`
	Denies int    `json:"denies"`
	Waived int    `json:"waived"`
	Rate   string `json:"rate"`
	Refs   Refs   `json:"refs"`
}

// ShadowWrong is a rule the kernel would have blocked where aphrollo did not,
// and how the log judged those blocks afterwards.
type ShadowWrong struct {
	Rule     string `json:"rule"`
	Fires    int    `json:"fires"`
	Stricter int    `json:"would_be_blocks"`
	Wrong    int    `json:"would_be_wrong"`
	Catches  int    `json:"would_be_catches"`
	Open     int    `json:"would_be_open"`
}

// Standdown is a rule that stood down, by the matcher it could not run.
type Standdown struct {
	Matcher string `json:"matcher"`
	N       int    `json:"n"`
	Refs    Refs   `json:"refs"`
}

// EscapeRow is the escapes of one class and the stage that should have caught them.
type EscapeRow struct {
	Class  string `json:"class"`
	Caught string `json:"should_have_been_caught_by"`
	N      int    `json:"n"`
	Refs   Refs   `json:"refs"`
}

// Escapes is the week's escapes; a false positive is a wrong deny, counted apart.
type Escapes struct {
	Rows           []EscapeRow `json:"rows"`
	FalsePositives int         `json:"false_positives"`
}

// GateLine is the recorded text of one gate line, by stage and verdict head.
type GateLine struct {
	Name   string `json:"name"`
	N      int    `json:"n"`
	Bytes  int    `json:"bytes"`
	Tokens int    `json:"tokens"`
	Refs   Refs   `json:"refs"`
}

// Tokens is the token cost of what the harness injects and of the gate lines.
type Tokens struct {
	Briefs  []measure.BriefLine `json:"briefs"`
	Biggest []GateLine          `json:"biggest_gate_lines"`
}

// Proposal is one change the evidence suggests. The report never applies it.
type Proposal struct {
	Rule    string `json:"rule"`
	Numbers string `json:"numbers"`
	Change  string `json:"change"`
	Refs    Refs   `json:"refs"`
}

// Report is the weekly report.
type Report struct {
	Title    string     `json:"title"`
	Repo     string     `json:"repo"`
	Window   string     `json:"window"`
	Until    string     `json:"until"`
	Events   int        `json:"events"`
	Friction []Friction `json:"friction"`
	// WrongBlocks are the rules with a deny, by override rate; ShadowWrong the shadow's would-be blocks.
	WrongBlocks []WrongBlock  `json:"wrong_block_candidates"`
	ShadowWrong []ShadowWrong `json:"shadow_would_be_blocks"`
	Standdowns  []Standdown   `json:"standdowns"`
	Escapes     Escapes       `json:"escapes"`
	// AB and Shadow are the window's; ABTotal is the whole log's, which is what
	// the 30-lanes-per-arm status reads.
	AB        measure.AB     `json:"ab"`
	ABTotal   measure.AB     `json:"ab_total"`
	Shadow    measure.Shadow `json:"shadow"`
	Tokens    Tokens         `json:"tokens"`
	Proposals []Proposal     `json:"proposals"`
	// Usage is the session usage section; nil when no transcripts were read.
	Usage *Usage `json:"usage,omitempty"`
}

type stamped struct {
	tdd.Event
	at time.Time
}

// Build folds the events of the window into a report. The result depends only
// on the events and Now, never on the events' order.
func Build(in Input) Report {
	evs := sortEvents(in.Events)
	var since time.Time
	if in.Window > 0 {
		since = in.Now.Add(-in.Window)
	}
	inWindow := func(e stamped) bool { return since.IsZero() || !e.at.Before(since) }

	f := newFold()
	f.repo = in.Repo
	for _, e := range evs {
		f.see(e, inWindow(e))
	}
	r := Report{Title: Title(in.Now), Repo: in.Repo, Window: windowText(in.Window), Until: in.Now.UTC().Format(time.RFC3339)}
	for _, e := range evs {
		if inWindow(e) {
			r.Events++
		}
	}
	r.Friction, r.WrongBlocks, r.Standdowns, r.Escapes = f.results()

	o := measure.Options{Window: in.Window}
	raw := make([]tdd.Event, len(evs))
	for i, e := range evs {
		raw[i] = e.Event
	}
	r.AB = measure.ComputeAB(raw, in.Now, o)
	r.ABTotal = measure.ComputeAB(raw, in.Now, measure.Options{})
	r.Shadow = measure.ComputeShadow(raw, in.Now, o)
	r.Shadow.Notes = nil
	for _, s := range r.Shadow.Rules {
		if s.Stricter > 0 {
			r.ShadowWrong = append(r.ShadowWrong, ShadowWrong{Rule: s.Rule, Fires: s.Fires, Stricter: s.Stricter, Wrong: s.Wrong, Catches: s.Catches, Open: s.Open})
		}
	}
	r.Tokens = Tokens{Briefs: in.Briefs, Biggest: f.biggestLines()}
	r.Proposals = propose(r)
	if in.Usage != nil {
		u := BuildUsage(*in.Usage, in.Now, in.Window, in.CompareAt)
		r.Usage = &u
	}
	return r
}

// sortEvents is the events with a time, in time order, ties by seq: the one
// order every fold reads, so the input's order cannot change the output.
func sortEvents(events []tdd.Event) []stamped {
	out := make([]stamped, 0, len(events))
	for _, e := range events {
		at, err := time.Parse(time.RFC3339, e.At)
		if err != nil {
			continue
		}
		out = append(out, stamped{e, at})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].at.Equal(out[j].at) {
			return out[i].at.Before(out[j].at)
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}

// Title is the report issue's title for the ISO week of t.
func Title(t time.Time) string {
	y, w := t.UTC().ISOWeek()
	return fmt.Sprintf("Report %d-W%02d", y, w)
}

func windowText(d time.Duration) string {
	switch {
	case d <= 0:
		return "whole log"
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("last %dd", d/(24*time.Hour))
	}
	return "last " + d.String()
}
