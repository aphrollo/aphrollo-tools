package report

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// price is a model's list price in USD per million tokens. The cost of a group
// is computed from the tokens when the report is read and is never stored: a
// price change is one line here.
type price struct{ Input, Output, CacheRead, CacheWrite float64 }

// modelPrices are list prices by model-id prefix, so a dated or [1m] id of a
// family resolves; the longest matching prefix wins. A notional cost, not an
// invoice: sessions may ride a flat plan.
var modelPrices = []struct {
	prefix string
	price  price
}{
	{"claude-fable-5", price{10, 50, 1, 12.5}},
	{"claude-opus-5", price{5, 25, 0.5, 6.25}},
	{"claude-opus-4-8", price{5, 25, 0.5, 6.25}},
	{"claude-sonnet-5", price{3, 15, 0.3, 3.75}},
	{"claude-sonnet-4-6", price{3, 15, 0.3, 3.75}},
	{"claude-haiku-4-5", price{1, 5, 0.1, 1.25}},
}

func priceFor(model string) (price, bool) {
	best, ok := price{}, false
	n := 0
	for _, m := range modelPrices {
		if strings.HasPrefix(model, m.prefix) && len(m.prefix) > n {
			best, ok, n = m.price, true, len(m.prefix)
		}
	}
	return best, ok
}

// costUSD prices tokens; ok is false for a model with no price (it costs 0 and
// its tokens are reported as unpriced).
func costUSD(model string, t UsageTokens) (float64, bool) {
	p, ok := priceFor(model)
	if !ok {
		return 0, false
	}
	return (float64(t.Fresh)*p.Input + float64(t.Output)*p.Output + float64(t.CacheRead)*p.CacheRead + float64(t.CacheWrite)*p.CacheWrite) / 1e6, true
}

// UsageGroup is the cells of one key summed. For an injection group Tokens is
// the estimated tokens injected and Count the injections that held them.
type UsageGroup struct {
	Key        string  `json:"key"`
	Turns      int64   `json:"turns"`
	Fresh      int64   `json:"fresh_input"`
	CacheWrite int64   `json:"cache_write"`
	CacheRead  int64   `json:"cache_read"`
	Output     int64   `json:"output"`
	Thinking   int64   `json:"thinking"`
	CostUSD    float64 `json:"cost_usd"`
	Tokens     int64   `json:"injected_tokens,omitempty"`
	Count      int64   `json:"injections,omitempty"`
}

// Submitted is the input of the group's turns, fresh and cached.
func (g UsageGroup) Submitted() int64 { return g.Fresh + g.CacheWrite + g.CacheRead }

func (g *UsageGroup) add(t UsageTokens, cost float64) {
	g.Turns += t.Turns
	g.Fresh += t.Fresh
	g.CacheWrite += t.CacheWrite
	g.CacheRead += t.CacheRead
	g.Output += t.Output
	g.Thinking += t.Thinking
	g.CostUSD += cost
}

// Injection is the aphrollo text the hooks added to sessions: its estimated
// tokens (the brief-length check's estimate) by hook event and by the kind of
// gate line, and its share of the input the models were submitted. A text is
// counted once, when it is injected; the cache then re-reads it on later turns.
type Injection struct {
	Tokens     int64        `json:"tokens"`
	InputShare string       `json:"share_of_input"`
	ByEvent    []UsageGroup `json:"by_hook_event"`
	ByKind     []UsageGroup `json:"by_kind"`
}

// UsagePeriod is a span of the window: its days, its usage and its injection.
type UsagePeriod struct {
	Days       int        `json:"days"`
	Group      UsageGroup `json:"usage"`
	Injected   int64      `json:"injected_tokens"`
	InputShare string     `json:"share_of_input"`
}

// UsageCompare is the usage before and after a date, for a change to be read
// against what it changed.
type UsageCompare struct {
	At     string      `json:"at"`
	Before UsagePeriod `json:"before"`
	After  UsagePeriod `json:"after"`
}

// Usage is the session usage section: aggregate numbers only, never a prompt,
// a tool input or output, or an injected text.
type Usage struct {
	Repo            string        `json:"repo"`
	Sessions        int           `json:"sessions"`
	Total           UsageGroup    `json:"total"`
	ByDay           []UsageGroup  `json:"by_day"`
	ByLane          []UsageGroup  `json:"by_lane"`
	ByRole          []UsageGroup  `json:"by_role"`
	ByModel         []UsageGroup  `json:"by_model"`
	TopSessions     []UsageGroup  `json:"top_sessions"`
	Injection       Injection     `json:"injection"`
	Compare         *UsageCompare `json:"compare,omitempty"`
	UnpricedTokens  int64         `json:"unpriced_tokens"`
	Unreadable      int           `json:"unreadable"`
	UnreadableFiles int           `json:"unreadable_files"`
}

// topSessionsN is how many sessions the section names.
const topSessionsN = 5

// BuildUsage regroups the facts' cells into the section; every table is one key
// function over the same cells. compareAt, when set and inside the window,
// adds the before and after.
func BuildUsage(f UsageFacts, now time.Time, window time.Duration, compareAt time.Time) Usage {
	u := Usage{Repo: f.Repo, Unreadable: f.Unreadable, UnreadableFiles: f.UnreadableFiles}
	groups := func(key func(UsageCell) string) []UsageGroup { return regroup(f, key) }
	u.Total = sumGroup("total", f)
	u.ByDay = groups(func(c UsageCell) string { return c.Day })
	sort.Slice(u.ByDay, func(i, j int) bool { return u.ByDay[i].Key < u.ByDay[j].Key })
	u.ByLane = groups(func(c UsageCell) string { return c.Lane })
	u.ByModel = groups(func(c UsageCell) string { return c.Model })
	u.ByRole = groups(func(c UsageCell) string {
		if c.Subagent {
			return "subagent"
		}
		return "coordinator"
	})
	sort.Slice(u.ByRole, func(i, j int) bool { return u.ByRole[i].Key > u.ByRole[j].Key })
	u.TopSessions = groups(func(c UsageCell) string { return c.Session })
	u.Sessions = len(u.TopSessions)
	if len(u.TopSessions) > topSessionsN {
		u.TopSessions = u.TopSessions[:topSessionsN]
	}
	u.UnpricedTokens = unpriced(f)
	u.Injection = injection(f, "", "", u.Total.Submitted())
	if cmp := compare(f, now, window, compareAt); cmp != nil {
		u.Compare = cmp
	}
	return u
}

// regroup sums the cells by key, the most costly group first, ties by key.
func regroup(f UsageFacts, key func(UsageCell) string) []UsageGroup {
	by := map[string]*UsageGroup{}
	for c, t := range f.Cells {
		k := key(c)
		g := by[k]
		if g == nil {
			g = &UsageGroup{Key: k}
			by[k] = g
		}
		cost, _ := costUSD(c.Model, t)
		g.add(t, cost)
	}
	out := make([]UsageGroup, 0, len(by))
	for _, g := range by {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostUSD != out[j].CostUSD {
			return out[i].CostUSD > out[j].CostUSD
		}
		if a, b := out[i].Submitted()+out[i].Output, out[j].Submitted()+out[j].Output; a != b {
			return a > b
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func sumGroup(key string, f UsageFacts) UsageGroup { return sumRange(key, f, "", "") }

// sumRange is the cells of the days from <= day < to (a bound "" is open).
func sumRange(key string, f UsageFacts, from, to string) UsageGroup {
	g := UsageGroup{Key: key}
	for c, t := range f.Cells {
		if !inDays(c.Day, from, to) {
			continue
		}
		cost, _ := costUSD(c.Model, t)
		g.add(t, cost)
	}
	return g
}

func inDays(day, from, to string) bool {
	return (from == "" || day >= from) && (to == "" || day < to)
}

func unpriced(f UsageFacts) int64 {
	var n int64
	for c, t := range f.Cells {
		if _, ok := priceFor(c.Model); !ok {
			n += t.Submitted() + t.Output
		}
	}
	return n
}

// injection is the injected tokens of the days from <= day < to by hook event
// and by kind, against the input submitted over the same days.
func injection(f UsageFacts, from, to string, submitted int64) Injection {
	events, kinds := map[string]*UsageGroup{}, map[string]*UsageGroup{}
	var in Injection
	bump := func(m map[string]*UsageGroup, key string, v InjectTokens) {
		g := m[key]
		if g == nil {
			g = &UsageGroup{Key: key}
			m[key] = g
		}
		g.Tokens += v.Tokens
		g.Count += v.Count
	}
	for k, v := range f.Inject {
		if !inDays(k.Day, from, to) {
			continue
		}
		in.Tokens += v.Tokens
		bump(events, k.Event, v)
		bump(kinds, k.Kind, v)
	}
	in.ByEvent, in.ByKind = injectGroups(events), injectGroups(kinds)
	in.InputShare = share(in.Tokens, submitted)
	return in
}

func injectGroups(m map[string]*UsageGroup) []UsageGroup {
	out := make([]UsageGroup, 0, len(m))
	for _, g := range m {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func share(part, whole int64) string {
	if whole <= 0 {
		return ""
	}
	return fmt.Sprintf("%.3f%%", float64(part)/float64(whole)*100)
}

const dayLayout = "2006-01-02"

// compare splits the window at the day of at. The days of a period are the
// calendar days it spans, the window's first and last day included.
func compare(f UsageFacts, now time.Time, window time.Duration, at time.Time) *UsageCompare {
	if at.IsZero() {
		return nil
	}
	end := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	start := end.Add(-24 * time.Hour)
	if window > 0 {
		start = now.UTC().Add(-window).Truncate(24 * time.Hour)
	} else {
		for c := range f.Cells {
			if d, err := time.Parse(dayLayout, c.Day); err == nil && d.Before(start) {
				start = d
			}
		}
	}
	cut := at.UTC().Truncate(24 * time.Hour)
	if !cut.After(start) || !cut.Before(end) {
		return nil
	}
	period := func(from, to time.Time) UsagePeriod {
		fd, td := from.Format(dayLayout), to.Format(dayLayout)
		g := sumRange("period", f, fd, td)
		in := injection(f, fd, td, g.Submitted())
		return UsagePeriod{Days: int(to.Sub(from) / (24 * time.Hour)), Group: g, Injected: in.Tokens, InputShare: in.InputShare}
	}
	return &UsageCompare{At: cut.Format(dayLayout), Before: period(start, cut), After: period(cut, end)}
}

// Text is the section as plain text: numbers, session ids, lane and model names.
func (u Usage) Text() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	line := func(g UsageGroup) {
		p("    %-30s %5d turns  input %s (fresh %s, cache write %s, cache read %s)  output %s  thinking %s  $%.2f",
			g.Key, g.Turns, tok(g.Submitted()), tok(g.Fresh), tok(g.CacheWrite), tok(g.CacheRead), tok(g.Output), tok(g.Thinking), g.CostUSD)
	}
	p("  from the harness's local transcripts of %s: %d sessions, %d turns (a reply counted once), unreadable %d lines, %d files", u.Repo, u.Sessions, u.Total.Turns, u.Unreadable, u.UnreadableFiles)
	line(UsageGroup{Key: "total", Turns: u.Total.Turns, Fresh: u.Total.Fresh, CacheWrite: u.Total.CacheWrite, CacheRead: u.Total.CacheRead, Output: u.Total.Output, Thinking: u.Total.Thinking, CostUSD: u.Total.CostUSD})
	p("  notional list-price cost, computed now from the price table in code; %s tokens of a model with no price are not costed", tok(u.UnpricedTokens))
	for _, t := range []struct {
		name string
		gs   []UsageGroup
		cap  int
	}{{"per role", u.ByRole, 0}, {"per day", u.ByDay, 0}, {"per lane", u.ByLane, textRows}, {"per model", u.ByModel, textRows}, {"top sessions", u.TopSessions, 0}} {
		p("  %s", t.name)
		for i, g := range t.gs {
			if t.cap > 0 && i == t.cap {
				p("    ... %d more (--json lists them all)", len(t.gs)-t.cap)
				break
			}
			line(g)
		}
	}
	p("  aphrollo's injected text: ~%s tokens, %s of the input submitted (counted once when injected; the cache re-reads it on later turns)", tok(u.Injection.Tokens), orNone(u.Injection.InputShare))
	for _, g := range u.Injection.ByEvent {
		p("    hook %-26s %8s tokens  %d injections", g.Key, tok(g.Tokens), g.Count)
	}
	for _, g := range u.Injection.ByKind {
		p("    kind %-26s %8s tokens", g.Key, tok(g.Tokens))
	}
	if c := u.Compare; c != nil {
		p("  before and after %s", c.At)
		for _, x := range []struct {
			name string
			per  UsagePeriod
		}{{"before", c.Before}, {"after", c.After}} {
			d := float64(max(x.per.Days, 1))
			p("    %-6s %d days  $%.2f a day  output %s a day  input %s a day  injected share %s",
				x.name, x.per.Days, x.per.Group.CostUSD/d, tok(int64(float64(x.per.Group.Output)/d)), tok(int64(float64(x.per.Group.Submitted())/d)), orNone(x.per.InputShare))
		}
	}
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "n/a"
	}
	return s
}

// tok is a token count in thousands and millions.
func tok(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}
