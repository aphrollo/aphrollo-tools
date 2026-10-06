package report

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"strconv"
	"strings"
)

// RenderHTML is the report as one self-contained HTML page: inline CSS, charts
// as inline SVG made here, no script and nothing fetched, so it opens offline
// and carries nothing but the report's own aggregate numbers. The same model
// gives the same bytes.
func RenderHTML(r Report) ([]byte, error) {
	var out bytes.Buffer
	if err := pageTmpl.Execute(&out, newPage(r)); err != nil {
		return nil, fmt.Errorf("rendering the report page: %w", err)
	}
	return out.Bytes(), nil
}

var pageTmpl = template.Must(template.New("page").Funcs(template.FuncMap{
	"refs": refsHTML, "dur": dur, "tok": tok,
	"usd": func(v float64) string { return fmt.Sprintf("$%.2f", v) },
}).Parse(pageTemplate))

// refsHTML is the evidence of a row as text to copy: the replay command of the
// first seq, then the others and how many more there are. It is text, never a
// link, so it stays a thing to run in the repo.
func refsHTML(r Refs) template.HTML {
	if len(r.Seqs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<span class="refs"><code>aphrollo why ` + strconv.FormatInt(r.Seqs[0], 10) + `</code>`)
	for _, s := range r.Seqs[1:] {
		b.WriteString(" " + strconv.FormatInt(s, 10))
	}
	if r.More > 0 {
		b.WriteString(html.EscapeString(fmt.Sprintf(" (+%d more)", r.More)))
	}
	b.WriteString(`</span>`)
	return template.HTML(b.String())
}

// comparePeriod is one side of the before and after, with its per-day figures
// computed here from the model: a derived number is never stored in it.
type comparePeriod struct {
	Name         string
	Days         int
	CostPerDay   float64
	OutputPerDay int64
	Share        string
}

type webCharts struct {
	Friction, Wrong, Escapes, AB, Briefs    template.HTML
	Days, Lanes, Models, Injection, Compare template.HTML
}

// webPage is what the template reads: the model, its tables cut to what a page
// holds, and the charts.
type webPage struct {
	R              Report
	Friction       []Friction
	FrictionMore   int
	Waived         []WrongBlock
	Charts         webCharts
	ComparePeriods []comparePeriod
}

func newPage(r Report) webPage {
	p := webPage{R: r, Friction: r.Friction}
	if len(p.Friction) > textRows {
		p.FrictionMore, p.Friction = len(p.Friction)-textRows, p.Friction[:textRows]
	}
	for _, w := range r.WrongBlocks {
		if w.Waived > 0 {
			p.Waived = append(p.Waived, w)
		}
	}
	p.Charts = charts(r, p)
	if u := r.Usage; u != nil && u.Compare != nil {
		for _, x := range []struct {
			name string
			per  UsagePeriod
		}{{"before", u.Compare.Before}, {"after", u.Compare.After}} {
			d := float64(max(x.per.Days, 1))
			p.ComparePeriods = append(p.ComparePeriods, comparePeriod{x.name, x.per.Days, x.per.Group.CostUSD / d,
				int64(float64(x.per.Group.Output) / d), orNone(x.per.InputShare)})
		}
	}
	return p
}

// chartRows bounds a bar chart to what a phone shows.
const chartRows = 10

func charts(r Report, p webPage) webCharts {
	var c webCharts
	var rows []barRow
	for _, f := range p.Friction[:min(len(p.Friction), chartRows)] {
		rows = append(rows, barRow{f.Rule, float64(f.total()), strconv.Itoa(f.total())})
	}
	c.Friction = barChart("friction per rule", rows)
	rows = nil
	for _, w := range p.Waived[:min(len(p.Waived), chartRows)] {
		rows = append(rows, barRow{w.Rule, float64(w.Waived) / float64(max(w.Denies, 1)) * 100, w.Rate})
	}
	c.Wrong = barChart("share of denies waived per rule", rows)
	rows = nil
	for _, e := range r.Escapes.Rows[:min(len(r.Escapes.Rows), chartRows)] {
		rows = append(rows, barRow{e.Class + ": " + e.Caught, float64(e.N), strconv.Itoa(e.N)})
	}
	c.Escapes = barChart("escapes by class", rows)
	rows = nil
	for _, a := range r.ABTotal.Arms {
		rows = append(rows, barRow{a.Arm + " lanes (whole log, 30 needed)", float64(a.Lanes), fmt.Sprintf("%d of 30", a.Lanes)})
	}
	c.AB = barChart("lanes per A/B arm", rows)
	rows = nil
	for _, b := range r.Tokens.Briefs {
		rows = append(rows, barRow{b.Name, float64(b.Tokens), fmt.Sprintf("%d / %d", b.Tokens, b.Cap)})
	}
	c.Briefs = barChart("injected text tokens against its cap", rows)
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
	c.Days = lineChart("tokens per day", days, []lineSeries{{"input submitted", "l1", in}, {"output", "l2", out}})
	c.Lanes = barChart("cost per lane", costRows(u.ByLane))
	c.Models = barChart("cost per model", costRows(u.ByModel))
	var rows []barRow
	for _, g := range u.Injection.ByKind[:min(len(u.Injection.ByKind), chartRows)] {
		rows = append(rows, barRow{g.Key, float64(g.Tokens), tok(g.Tokens)})
	}
	c.Injection = barChart("injected tokens by gate-line kind", rows)
	if cmp := u.Compare; cmp != nil {
		per := func(p UsagePeriod) float64 { return p.Group.CostUSD / float64(max(p.Days, 1)) }
		c.Compare = barChart("cost a day before and after", []barRow{
			{"before " + cmp.At, per(cmp.Before), fmt.Sprintf("$%.2f", per(cmp.Before))},
			{"after " + cmp.At, per(cmp.After), fmt.Sprintf("$%.2f", per(cmp.After))},
		})
	}
}

func costRows(gs []UsageGroup) []barRow {
	var rows []barRow
	for _, g := range gs[:min(len(gs), chartRows)] {
		rows = append(rows, barRow{g.Key, g.CostUSD, fmt.Sprintf("$%.2f", g.CostUSD)})
	}
	return rows
}
