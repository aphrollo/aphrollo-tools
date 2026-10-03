package render

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Kind is the kind of a rendered line.
type Kind string

const (
	KindDeny      Kind = "deny"
	KindGuide     Kind = "guide"
	KindGreen     Kind = "green"
	KindRed       Kind = "red"
	KindNotTested Kind = "not-tested"
	KindDeferred  Kind = "deferred"
	KindStale     Kind = "stale"
)

// The caps of §5 "Tokens" and §4, in tokens (bytes ÷ 4). The doc names none for
// not-tested, pending and stale: they take the guidance cap.
const (
	CapDeny          = 120
	CapGuide         = 60
	CapGreen         = 60
	CapRed           = 400
	CapBrief         = 400
	CapSubagentBrief = 250

	// PlatformContextBytes is the platform's cap on one additionalContext
	// (§12 "Platform facts": 10,000 characters), for hooks with no cap of §5.
	PlatformContextBytes = 10000
)

// Cap is the cap of a kind in tokens. A kind with no cap of its own has the
// guidance cap, the smallest.
func Cap(k Kind) int {
	switch k {
	case KindDeny:
		return CapDeny
	case KindGreen:
		return CapGreen
	case KindRed:
		return CapRed
	}
	return CapGuide
}

// Tokens is the estimate of n bytes: bytes ÷ 4, rounded up so a text just past
// its cap is never read as under it (the rule measure.Tokens counts by).
func Tokens(n int) int { return (n + 3) / 4 }

// Bounds on what a line keeps whole. A field past its bound is cut and named.
const (
	ruleCeil     = 48
	overrideCeil = 160
	unitCeil     = 48
	jobCeil      = 24
	refCeil      = 24
)

var ansi = regexp.MustCompile("\x1b(?:\\[[0-?]*[ -/]*[@-~]|\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|.)")

// plain flattens text for a line: escape sequences go, every run of white
// space or control characters becomes one space, invalid UTF-8 becomes U+FFFD.
func plain(s string) string {
	s = strings.ToValidUTF8(ansi.ReplaceAllString(s, ""), "�")
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
}

// prefix is s cut to at most n bytes at a character boundary: the cut drops
// the bytes of a character it split.
func prefix(s string, n int) string {
	return strings.ToValidUTF8(s[:min(n, len(s))], "")
}

// clip is s cut to at most n bytes, ending in "…" when it was cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < len("…") {
		return ""
	}
	return strings.TrimRight(prefix(s, n-len("…")), " ") + "…"
}

// part is one piece of a line. A part with a name is a field: it is dropped
// whole, with its lead and trail, when its text is empty, it is cut to ceil
// when it has one, and when flex it is squeezed to fit the cap. A part with no
// name is fixed text that is never touched.
type part struct {
	name, lead, text, trail string
	ceil                    int
	flex                    bool
}

func fixed(s string) part { return part{lead: s} }

func keep(name, lead, text, trail string, ceil int) part {
	return part{name: name, lead: lead, text: text, trail: trail, ceil: ceil}
}

func flex(name, lead, text, trail string) part {
	return part{name: name, lead: lead, text: text, trail: trail, flex: true}
}

func (p part) size() int { return len(p.lead) + len(p.text) + len(p.trail) }

func marker(names []string) string { return " · cut: " + strings.Join(names, ", ") }

// compose joins the parts into a line of at most limit bytes and names what it
// cut. Over the limit, flex fields share what the fixed text and the cut marker
// leave, shortest first; if even that is not enough the whole line is clipped
// and says so, a last resort the tests show no kind reaches.
func compose(limit int, parts ...part) (string, []string) {
	var cut []string
	mark := func(name string) {
		if !slices.Contains(cut, name) {
			cut = append(cut, name)
		}
	}
	var live []part
	for _, p := range parts {
		if p.name != "" && p.text == "" {
			continue
		}
		if p.ceil > 0 && len(p.text) > p.ceil {
			p.text = clip(p.text, p.ceil)
			mark(p.name)
		}
		live = append(live, p)
	}
	join := func() string {
		var b strings.Builder
		for _, p := range live {
			b.WriteString(p.lead + p.text + p.trail)
		}
		return b.String()
	}
	out := join()
	if len(cut) == 0 && len(out) <= limit {
		return out, nil
	}
	if len(out)+len(marker(cut)) <= limit {
		return out + marker(cut), cut
	}
	names, room, flexed := slices.Clone(cut), limit, []int(nil)
	for i, p := range live {
		if !p.flex {
			room -= p.size()
			continue
		}
		room -= len(p.lead) + len(p.trail)
		flexed = append(flexed, i)
		if !slices.Contains(names, p.name) {
			names = append(names, p.name)
		}
	}
	room = max(room-len(marker(names)), 0)
	slices.SortStableFunc(flexed, func(a, b int) int { return len(live[a].text) - len(live[b].text) })
	for k, i := range flexed {
		if share := room / (len(flexed) - k); len(live[i].text) > share {
			live[i].text = clip(live[i].text, share)
			mark(live[i].name)
		}
		room -= len(live[i].text)
	}
	live = slices.DeleteFunc(live, func(p part) bool { return p.name != "" && p.text == "" })
	out = join()
	if len(out)+len(marker(cut)) <= limit {
		return out + marker(cut), cut
	}
	mark("line")
	tail := marker(cut)
	return clip(out, limit-len(tail)) + tail, cut
}
