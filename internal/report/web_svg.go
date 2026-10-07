package report

import (
	"fmt"
	"html"
	"html/template"
	"strings"
)

// The charts are made here: no script, no library, nothing fetched. A bar is a
// page element whose width is its share, so its label and value are page text
// at the page's own size on any screen; only the line chart is inline SVG, and
// it carries no text of its own. Colours are CSS classes the page's stylesheet
// defines for light and dark, so a chart follows the reader's colour scheme.

// barRow is one bar: what it is called, how long it is and the text beside it.
type barRow struct {
	Label string
	Value float64
	Text  string
}

// barChart is a list of bars, the full bar at top, or at the longest value
// when top is 0. A label is never cut: it wraps.
func barChart(title string, rows []barRow, top float64) template.HTML {
	if len(rows) == 0 {
		return ""
	}
	if top <= 0 {
		for _, r := range rows {
			top = max(top, r.Value)
		}
	}
	var b strings.Builder
	t := html.EscapeString(title)
	fmt.Fprintf(&b, `<figure class="bars" aria-label="%s"><figcaption>%s</figcaption>`, t, t)
	for _, r := range rows {
		fmt.Fprintf(&b, `<div class="row"><span class="lbl">%s</span><span class="track"><span class="bar" style="width:%.1f%%"></span></span><span class="val">%s</span></div>`,
			html.EscapeString(r.Label), min(safeDiv(r.Value, top), 1)*100, html.EscapeString(r.Text))
	}
	b.WriteString(`</figure>`)
	return template.HTML(b.String())
}

// splitPart is one part of a whole: its name, its colour class and its amount.
type splitPart struct {
	Name, Class string
	Value       float64
}

// splitChart is one bar cut into the parts of a whole, each with its share.
func splitChart(title string, parts []splitPart) template.HTML {
	total := 0.0
	for _, p := range parts {
		total += p.Value
	}
	if total <= 0 {
		return ""
	}
	var bar, legend strings.Builder
	for _, p := range parts {
		share := p.Value / total * 100
		label := html.EscapeString(fmt.Sprintf("%s %.0f%%", p.Name, share))
		fmt.Fprintf(&bar, `<span class="seg %s" style="width:%.1f%%" title="%s"></span>`, html.EscapeString(p.Class), share, label)
		fmt.Fprintf(&legend, `<li><span class="key %s"></span>%s <span class="muted">%s</span></li>`, html.EscapeString(p.Class), label, html.EscapeString(usd(p.Value)))
	}
	t := html.EscapeString(title)
	return template.HTML(fmt.Sprintf(`<figure class="split" aria-label="%s"><figcaption>%s</figcaption><div class="stack">%s</div><ul class="legend">%s</ul></figure>`,
		t, t, bar.String(), legend.String()))
}

// cut shortens s to n runes for the eye, with an ellipsis.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// lineChart is one series over its x labels, drawn to its own peak so a small
// series is never flattened by a big one. The SVG stretches to the page's width
// and holds no text: the title, the peak and the first and last label are page
// text around it. With fewer than two points there is no line, and no chart.
func lineChart(title string, xs []string, values []float64, class string) template.HTML {
	if len(xs) < 2 || len(values) != len(xs) {
		return ""
	}
	const w, h, pad = 640, 120, 6
	peak := 0.0
	for _, v := range values {
		peak = max(peak, v)
	}
	pts := make([]string, len(values))
	for i, v := range values {
		x := float64(w) * float64(i) / float64(len(xs)-1)
		y := float64(h-pad) - float64(h-2*pad)*safeDiv(v, peak)
		pts[i] = fmt.Sprintf("%.1f,%.1f", x, y)
	}
	t := html.EscapeString(title)
	c := html.EscapeString(class)
	var b strings.Builder
	fmt.Fprintf(&b, `<figure class="trend" aria-label="%s"><figcaption>%s <span class="muted">peak %s</span></figcaption>`, t, t, html.EscapeString(tok(int64(peak))))
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" preserveAspectRatio="none" role="img" aria-label="%s">`, w, h, t)
	fmt.Fprintf(&b, `<polygon class="area %s" points="0,%d %s %d,%d"/>`, c, h-pad, strings.Join(pts, " "), w, h-pad)
	fmt.Fprintf(&b, `<polyline class="line %s" points="%s"/>`, c, strings.Join(pts, " "))
	fmt.Fprintf(&b, `<line class="axis" x1="0" y1="%d" x2="%d" y2="%d"/></svg>`, h-pad, w, h-pad)
	fmt.Fprintf(&b, `<div class="xs"><span>%s</span><span>%s</span></div></figure>`, html.EscapeString(xs[0]), html.EscapeString(xs[len(xs)-1]))
	return template.HTML(b.String())
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}
