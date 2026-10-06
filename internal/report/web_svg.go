package report

import (
	"fmt"
	"html"
	"html/template"
	"strings"
)

// The charts are inline SVG made here: no script, no library, nothing fetched.
// Colours are CSS classes the page's stylesheet defines for light and dark, so
// a chart follows the reader's colour scheme.

// barRow is one bar: what it is called, how long it is and the text beside it.
type barRow struct {
	Label string
	Value float64
	Text  string
}

const (
	chartWidth = 640
	labelWidth = 230
	rowHeight  = 24
	valueRoom  = 110
)

// barChart is a horizontal bar chart, longest value the full bar. A label is
// escaped and carried whole in a title, so a long rule name is readable by hover
// and by assistive tech even where it is cut for the eye.
func barChart(title string, rows []barRow) template.HTML {
	if len(rows) == 0 {
		return ""
	}
	top := 0.0
	for _, r := range rows {
		top = max(top, r.Value)
	}
	var b strings.Builder
	h := len(rows)*rowHeight + 8
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %d %d" role="img" aria-label="%s">`, chartWidth, h, html.EscapeString(title))
	room := float64(chartWidth - labelWidth - valueRoom)
	for i, r := range rows {
		y := 4 + i*rowHeight
		w := 0.0
		if top > 0 {
			w = r.Value / top * room
		}
		fmt.Fprintf(&b, `<g><title>%s: %s</title>`, html.EscapeString(r.Label), html.EscapeString(r.Text))
		fmt.Fprintf(&b, `<text class="lbl" x="%d" y="%d" text-anchor="end">%s</text>`, labelWidth-8, y+15, html.EscapeString(cut(r.Label, 34)))
		fmt.Fprintf(&b, `<rect class="bar" x="%d" y="%d" width="%.1f" height="%d" rx="2"/>`, labelWidth, y+3, w, rowHeight-8)
		fmt.Fprintf(&b, `<text class="val" x="%.1f" y="%d">%s</text></g>`, float64(labelWidth)+w+6, y+15, html.EscapeString(r.Text))
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// cut shortens s to n runes for the eye, with an ellipsis.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// lineSeries is one line of a line chart: a value per x label.
type lineSeries struct {
	Name   string
	Class  string
	Values []float64
}

// lineChart is a line per series over the same x labels, drawn to the largest
// value of any. With fewer than two points there is no line to draw, and the
// chart says so rather than drawing nothing.
func lineChart(title string, xs []string, series []lineSeries) template.HTML {
	if len(xs) < 2 {
		return ""
	}
	const left, right, top, bottom = 8, 8, 12, 34
	const h = 190
	top0 := 0.0
	for _, s := range series {
		for _, v := range s.Values {
			top0 = max(top0, v)
		}
	}
	plotW, plotH := float64(chartWidth-left-right), float64(h-top-bottom)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %d %d" role="img" aria-label="%s">`, chartWidth, h, html.EscapeString(title))
	fmt.Fprintf(&b, `<line class="axis" x1="%d" y1="%d" x2="%d" y2="%d"/>`, left, h-bottom, chartWidth-right, h-bottom)
	for _, s := range series {
		pts := make([]string, len(s.Values))
		for i, v := range s.Values {
			x := float64(left) + plotW*float64(i)/float64(len(xs)-1)
			y := float64(h-bottom) - plotH*safeDiv(v, top0)
			pts[i] = fmt.Sprintf("%.1f,%.1f", x, y)
		}
		fmt.Fprintf(&b, `<polyline class="line %s" fill="none" points="%s"><title>%s</title></polyline>`, html.EscapeString(s.Class), strings.Join(pts, " "), html.EscapeString(s.Name))
	}
	fmt.Fprintf(&b, `<text class="lbl" x="%d" y="%d">%s</text>`, left, h-bottom+16, html.EscapeString(xs[0]))
	fmt.Fprintf(&b, `<text class="lbl" x="%d" y="%d" text-anchor="end">%s</text>`, chartWidth-right, h-bottom+16, html.EscapeString(xs[len(xs)-1]))
	for i, s := range series {
		fmt.Fprintf(&b, `<text class="lbl key %s" x="%d" y="%d">%s</text>`, html.EscapeString(s.Class), left+i*190, h-6, html.EscapeString("— "+s.Name))
	}
	fmt.Fprintf(&b, `<text class="lbl" x="%d" y="%d" text-anchor="end">peak %s</text>`, chartWidth-right, top, html.EscapeString(fmt.Sprintf("%.0f", top0)))
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}
