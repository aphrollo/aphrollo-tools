package report

import (
	"sort"
	"strings"
)

// A report is shown whole to the person who runs it, and published to an issue
// only in the form below: nothing in it names the tool that wrote the code.

// modelSizes name a model by the size of its family, so a published report says
// "large model" and not an id that names its maker.
var modelSizes = []struct{ family, label string }{
	{"fable", "large model"}, {"opus", "large model"}, {"sonnet", "medium model"}, {"haiku", "small model"},
}

// modelLabel is the published name of a model id.
func modelLabel(id string) string {
	l := strings.ToLower(id)
	for _, m := range modelSizes {
		if strings.Contains(l, m.family) {
			return m.label
		}
	}
	return "other model"
}

// Published is the report as it may be published: the per-model table keyed by
// model size, summed. The receiver is unchanged.
func (r Report) Published() Report {
	if r.Usage == nil {
		return r
	}
	u := *r.Usage
	by := map[string]*UsageGroup{}
	for _, g := range u.ByModel {
		k := modelLabel(g.Key)
		p := by[k]
		if p == nil {
			p = &UsageGroup{Key: k}
			by[k] = p
		}
		p.Turns += g.Turns
		p.Fresh += g.Fresh
		p.CacheWrite += g.CacheWrite
		p.CacheWrite1h += g.CacheWrite1h
		p.CacheRead += g.CacheRead
		p.Output += g.Output
		p.Thinking += g.Thinking
		p.CostUSD += g.CostUSD
	}
	u.ByModel = nil
	for _, g := range by {
		u.ByModel = append(u.ByModel, *g)
	}
	sort.Slice(u.ByModel, func(i, j int) bool {
		if u.ByModel[i].CostUSD != u.ByModel[j].CostUSD {
			return u.ByModel[i].CostUSD > u.ByModel[j].CostUSD
		}
		return u.ByModel[i].Key < u.ByModel[j].Key
	})
	r.Usage = &u
	return r
}

// WithoutUsage is the report with its session usage left out: what is published
// when the undercover check refuses the usage section.
func (r Report) WithoutUsage() Report {
	r.Usage = nil
	r.withheld = true
	return r
}
