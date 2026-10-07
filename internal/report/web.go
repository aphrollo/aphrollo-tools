package report

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"sort"
	"strconv"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/measure"
)

// RenderHTML is the report as one self-contained HTML page: inline CSS, charts
// made here as page elements and inline SVG, no script and nothing fetched, so
// it opens offline and carries nothing but the report's own aggregate numbers.
// The same model gives the same bytes.
func RenderHTML(r Report) ([]byte, error) {
	var out bytes.Buffer
	if err := pageTmpl.Execute(&out, newPage(r)); err != nil {
		return nil, fmt.Errorf("rendering the report page: %w", err)
	}
	return out.Bytes(), nil
}

var pageTmpl = template.Must(template.New("page").Funcs(template.FuncMap{
	"refs": refsHTML, "dur": dur, "tok": tok, "usd": usd, "num": numAny,
	"delta": deltaCell, "short": shortKey, "frictionRow": frictionRowOf,
}).Parse(pageTemplate))

// pageRows is how many rows a long table shows before the rest is folded.
const pageRows = textRows

// refsHTML is the evidence of a row as text to copy: the replay command of the
// first seq, then the others folded behind how many there are. It is text,
// never a link, so it stays a thing to run in the repo.
func refsHTML(r Refs) template.HTML {
	if len(r.Seqs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<span class="refs"><code>aphrollo why ` + strconv.FormatInt(r.Seqs[0], 10) + `</code>`)
	if rest := len(r.Seqs) - 1 + r.More; rest > 0 {
		fmt.Fprintf(&b, `<details><summary>+%d more</summary>`, rest)
		var seqs []string
		for _, s := range r.Seqs[1:] {
			seqs = append(seqs, strconv.FormatInt(s, 10))
		}
		b.WriteString(strings.Join(seqs, " "))
		if r.More > 0 {
			if len(seqs) > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "(+%d in the JSON)", r.More)
		}
		b.WriteString(`</details>`)
	}
	b.WriteString(`</span>`)
	return template.HTML(b.String())
}

// num is a count with thousands separators.
func num(n int64) string {
	s := strconv.FormatInt(max(n, -n), 10)
	var b strings.Builder
	if n < 0 {
		b.WriteString("−")
	}
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// usd is a dollar amount with thousands separators and cents.
func usd(v float64) string {
	cents := int64(v*100 + 0.5)
	return fmt.Sprintf("$%s.%02d", num(cents/100), cents%100)
}

// signed is a change with its sign, the minus a real one.
func signed(d int) string {
	switch {
	case d > 0:
		return "+" + num(int64(d))
	case d < 0:
		return num(int64(d))
	}
	return "±0"
}

// deltaCell is a rule's change against the window before as a table cell:
// more friction reads as worse, less as better.
func deltaCell(now, prev int) template.HTML {
	class := "n"
	switch {
	case now > prev:
		class += " up"
	case now < prev:
		class += " down"
	}
	return template.HTML(fmt.Sprintf(`<td class="%s" title="%s the window before">%s</td>`, class, html.EscapeString(num(int64(prev))), html.EscapeString(signed(now-prev))))
}

// shortKey is a long id cut to what tells it apart; the page carries it whole in a title.
func shortKey(s string) string { return cut(s, 13) }

// comparePeriod is one side of the before and after, with its per-day figures
// computed here from the model: a derived number is never stored in it.
type comparePeriod struct {
	Name         string
	Days         int
	CostPerDay   float64
	OutputPerDay int64
	Share        string
}

// fact is one line of the summary: this window's figure and, when the report
// has a window before, the change against it.
type fact struct {
	Name, Value, Change string
	Worse               bool
}

// proposalGroup is the proposals that share one change.
type proposalGroup struct {
	Change    string
	Proposals []Proposal
}

type webCharts struct {
	Friction, Wrong, Escapes, AB, Briefs  template.HTML
	Days, Split, Lanes, Models, Injection template.HTML
	Compare                               template.HTML
}

// webPage is what the template reads: the model, its tables split into what
// shows and what is folded, the summary and the charts.
type webPage struct {
	R              Report
	Lede           string
	Facts          []fact
	Groups         []proposalGroup
	Friction       []Friction
	FrictionMore   []Friction
	Waived         []WrongBlock
	Lanes          []UsageGroup
	LanesMore      []UsageGroup
	ABInWindow     bool
	Charts         webCharts
	ComparePeriods []comparePeriod
}

func newPage(r Report) webPage {
	p := webPage{R: r}
	p.Friction, p.FrictionMore = split(r.Friction, pageRows)
	for _, w := range r.WrongBlocks {
		if w.Waived > 0 {
			p.Waived = append(p.Waived, w)
		}
	}
	for _, a := range r.AB.Arms {
		p.ABInWindow = p.ABInWindow || a.Lanes > 0
	}
	p.Groups = groupProposals(r.Proposals)
	p.Lede = lede(r, len(p.Groups))
	p.Facts = facts(r)
	if u := r.Usage; u != nil {
		p.Lanes, p.LanesMore = split(u.ByLane, pageRows)
		if u.Compare != nil {
			for _, x := range []struct {
				name string
				per  UsagePeriod
			}{{"before", u.Compare.Before}, {"after", u.Compare.After}} {
				d := float64(max(x.per.Days, 1))
				p.ComparePeriods = append(p.ComparePeriods, comparePeriod{x.name, x.per.Days, x.per.Group.CostUSD / d,
					int64(float64(x.per.Group.Output) / d), orNone(x.per.FreshShare)})
			}
		}
	}
	p.Charts = charts(r)
	return p
}

// split is the first n rows and the rest.
func split[T any](rows []T, n int) ([]T, []T) {
	if len(rows) <= n {
		return rows, nil
	}
	return rows[:n], rows[n:]
}

// groupProposals is the proposals by the change they make, the group with the
// most rules first, ties by the change's text; inside a group the model's order.
func groupProposals(ps []Proposal) []proposalGroup {
	var gs []proposalGroup
	at := map[string]int{}
	for _, p := range ps {
		i, ok := at[p.Change]
		if !ok {
			i = len(gs)
			at[p.Change] = i
			gs = append(gs, proposalGroup{Change: p.Change})
		}
		gs[i].Proposals = append(gs[i].Proposals, p)
	}
	sort.SliceStable(gs, func(i, j int) bool {
		if len(gs[i].Proposals) != len(gs[j].Proposals) {
			return len(gs[i].Proposals) > len(gs[j].Proposals)
		}
		return gs[i].Change < gs[j].Change
	})
	return gs
}

// lede is the page's first sentence: where the A/B stands and how much there
// is to look at.
func lede(r Report, groups int) string {
	var arms []string
	for _, a := range r.ABTotal.Arms {
		arms = append(arms, fmt.Sprintf("%s %d of %d lanes", a.Arm, a.Lanes, measure.MinABLanes))
	}
	ab := "The A/B has no lanes yet."
	if len(arms) > 0 {
		state := "not decidable yet"
		if r.ABTotal.Decidable {
			state = "both arms can decide"
		}
		ab = fmt.Sprintf("The A/B is %s: %s.", state, strings.Join(arms, ", "))
	}
	switch n := len(r.Proposals); n {
	case 0:
		return ab + " No rule is over a proposal threshold."
	case 1:
		return ab + " One proposal."
	default:
		return fmt.Sprintf("%s %d proposals in %d groups.", ab, n, groups)
	}
}

// facts is the summary: the window's friction, escapes and cost, each against
// the window before when there is one. For every figure here, more is worse.
func facts(r Report) []fact {
	var now Previous
	for _, f := range r.Friction {
		now.Denies += f.Denies
		now.Overrides += f.Overrides
		now.Refusals += f.Refusals
		now.NotTested += f.NotTested
		now.SecsLost += f.SecsLost
	}
	for _, e := range r.Escapes.Rows {
		now.Escapes += e.N
	}
	count := func(name string, v int, prev func(Previous) int) fact {
		f := fact{Name: name, Value: num(int64(v))}
		if r.Previous != nil {
			d := v - prev(*r.Previous)
			f.Change, f.Worse = signed(d), d > 0
		}
		return f
	}
	out := []fact{
		count("Not tested", now.NotTested, func(p Previous) int { return p.NotTested }),
		count("Denies", now.Denies, func(p Previous) int { return p.Denies }),
		count("Overrides", now.Overrides, func(p Previous) int { return p.Overrides }),
		count("Refusals", now.Refusals, func(p Previous) int { return p.Refusals }),
		count("Escapes", now.Escapes, func(p Previous) int { return p.Escapes }),
	}
	waited := fact{Name: "Waited on the gate", Value: dur(now.SecsLost)}
	if r.Previous != nil {
		d := now.SecsLost - r.Previous.SecsLost
		waited.Change, waited.Worse = "+"+dur(d), d > 0
		if d < 0 {
			waited.Change = "−" + dur(-d)
		}
	}
	out = append(out, waited)
	if u := r.Usage; u != nil {
		out = append(out, fact{Name: "Session cost", Value: usd(u.Total.CostUSD)})
	}
	return out
}

// chartRows bounds a bar chart to what a phone shows.
const chartRows = 10

func charts(r Report) webCharts {
	var c webCharts
	var rows []barRow
	for _, f := range r.Friction {
		if len(rows) < chartRows && f.total() > 0 {
			rows = append(rows, barRow{f.Rule, float64(f.total()), num(int64(f.total()))})
		}
	}
	c.Friction = barChart("friction per rule", rows, 0)
	rows = nil
	for _, w := range r.WrongBlocks {
		if w.Waived > 0 && len(rows) < chartRows {
			rows = append(rows, barRow{w.Rule, float64(w.Waived) / float64(max(w.Denies, 1)) * 100, w.Rate})
		}
	}
	c.Wrong = barChart("share of denies waived per rule", rows, 100)
	rows = nil
	for _, e := range r.Escapes.Rows[:min(len(r.Escapes.Rows), chartRows)] {
		rows = append(rows, barRow{e.Class + ": " + e.Caught, float64(e.N), num(int64(e.N))})
	}
	c.Escapes = barChart("escapes by class", rows, 0)
	rows = nil
	for _, a := range r.ABTotal.Arms {
		rows = append(rows, barRow{a.Arm, float64(min(a.Lanes, measure.MinABLanes)), fmt.Sprintf("%d of %d", a.Lanes, measure.MinABLanes)})
	}
	c.AB = barChart(fmt.Sprintf("lanes per A/B arm in the whole log, %d needed", measure.MinABLanes), rows, measure.MinABLanes)
	rows = nil
	for _, b := range r.Tokens.Briefs {
		rows = append(rows, barRow{b.Name, safeDiv(float64(b.Tokens), float64(b.Cap)) * 100, fmt.Sprintf("%s / %s", num(int64(b.Tokens)), num(int64(b.Cap)))})
	}
	c.Briefs = barChart("injected text tokens against its cap", rows, 100)
	if u := r.Usage; u != nil {
		usageCharts(u, &c)
	}
	return c
}

func usageCharts(u *Usage, c *webCharts) {
	var days []string
	var in, out []float64
	for _, d := range u.ByDay {
		days = append(days, d.Key)
		in = append(in, float64(d.Submitted()))
		out = append(out, float64(d.Output))
	}
	c.Days = template.HTML(string(lineChart("input submitted per day", days, in, "l1")) + string(lineChart("output per day", days, out, "l2")))
	var lanes []UsageGroup
	var onLanes, coord, unattr float64
	for _, g := range u.ByLane {
		switch g.Key {
		case laneCoordination:
			coord += g.CostUSD
		case laneUnattributed:
			unattr += g.CostUSD
		default:
			onLanes += g.CostUSD
			lanes = append(lanes, g)
		}
	}
	c.Split = splitChart("where the session cost went", []splitPart{{"lanes", "s1", onLanes}, {"coordination", "s2", coord}, {"unattributed", "s3", unattr}})
	c.Lanes = barChart("cost per lane", costRows(lanes), 0)
	c.Models = barChart("cost per model", costRows(u.ByModel), 0)
	var rows []barRow
	for _, g := range u.Injection.ByKind[:min(len(u.Injection.ByKind), chartRows)] {
		rows = append(rows, barRow{g.Key, float64(g.Tokens), tok(g.Tokens)})
	}
	c.Injection = barChart("injected tokens by gate-line kind", rows, 0)
	if cmp := u.Compare; cmp != nil {
		per := func(p UsagePeriod) float64 { return p.Group.CostUSD / float64(max(p.Days, 1)) }
		c.Compare = barChart("cost a day before and after", []barRow{
			{"before " + cmp.At, per(cmp.Before), usd(per(cmp.Before))},
			{"after " + cmp.At, per(cmp.After), usd(per(cmp.After))},
		}, 0)
	}
}

func costRows(gs []UsageGroup) []barRow {
	var rows []barRow
	for _, g := range gs[:min(len(gs), chartRows)] {
		rows = append(rows, barRow{g.Key, g.CostUSD, usd(g.CostUSD)})
	}
	return rows
}

// numAny is num for the template, which holds both int and int64 counts.
func numAny(v any) string {
	switch n := v.(type) {
	case int:
		return num(int64(n))
	case int64:
		return num(n)
	}
	return fmt.Sprint(v)
}

// frictionLine is one friction row as the template prints it: Prev says the
// report has a window before, so the row carries its change.
type frictionLine struct {
	F     Friction
	Total int
	Prev  bool
}

func frictionRowOf(p webPage, f Friction) frictionLine {
	return frictionLine{F: f, Total: f.total(), Prev: p.R.Previous != nil}
}
