package report

import "time"

// Previous is the window before the report's, of the same length, counted the
// same way: what the change since last week is read against.
type Previous struct {
	Window    string  `json:"window"`
	Events    int     `json:"events"`
	Denies    int     `json:"denies"`
	Overrides int     `json:"overrides"`
	Refusals  int     `json:"refusals"`
	NotTested int     `json:"not_tested"`
	SecsLost  float64 `json:"secs_lost"`
	Escapes   int     `json:"escapes"`
	// Gone are the rules with friction the window before and none in this one.
	Gone []RuleCount `json:"gone"`
}

// RuleCount is a rule and its friction total.
type RuleCount struct {
	Rule string `json:"rule"`
	N    int    `json:"n"`
}

// previous folds the window before since into the totals of Previous and each
// rule's total, and gives the friction rows their Prev. A rule seen only the
// window before is named in Gone, so a drop to zero shows. A window before
// with no events is no comparison: nil.
func previous(evs []stamped, repo, window string, from, since time.Time, rows []Friction) ([]Friction, *Previous) {
	in := func(e stamped) bool { return !e.at.Before(from) && e.at.Before(since) }
	f := newFold()
	f.repo = repo
	p := &Previous{Window: window}
	for _, e := range evs {
		if in(e) {
			p.Events++
		}
		f.see(e, in(e))
	}
	if p.Events == 0 {
		// Before the log began: every number would read as all new.
		return rows, nil
	}
	before, _, _, esc := f.results()
	for _, x := range esc.Rows {
		p.Escapes += x.N
	}
	idx := map[string]int{}
	for i, r := range rows {
		idx[r.Rule] = i
	}
	for _, b := range before {
		p.Denies += b.Denies
		p.Overrides += b.Overrides
		p.Refusals += b.Refusals
		p.NotTested += b.NotTested
		p.SecsLost += b.SecsLost
		if i, ok := idx[b.Rule]; ok {
			rows[i].Prev = b.total()
			continue
		}
		p.Gone = append(p.Gone, RuleCount{b.Rule, b.total()})
	}
	return rows, p
}
