package measure

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

const notRecorded = "not recorded"

// Text is the answer as the plain lines `aphrollo why` prints: every field the
// log recorded for the event, then what the rest of the log says of it.
func (w Why) Text() string {
	var b strings.Builder
	line := func(label, format string, a ...any) { fmt.Fprintf(&b, "%-11s %s\n", label, fmt.Sprintf(format, a...)) }
	fmt.Fprintf(&b, "seq %d  %s  %s  lane %s\n", w.Seq, w.Kind, w.At, laneName(w.Lane))
	if d := w.Deny; d != nil {
		line("rule", "%s", d.Rule)
		line("cause", "%s", orNot(d.Cause))
		line("offered", "%s", orNot(d.OfferedOverride))
		w.logged(line)
		line("override", "%s", d.overrideText())
		line("outcome", "%s", d.Outcome)
		line("this rule", "%d denies, %d overrides, %d wrong blocks, %d complied",
			d.Counts.Denies, d.Counts.Overrides, d.Counts.WrongBlocks, d.Counts.Complied)
		d.kernelLines(line)
		line("shadow", "%s", w.Shadow)
		return b.String()
	}
	if r := w.Run; r != nil {
		line("verdict", "%s", r.Verdict)
		line("result", "%s", orNot(r.Result))
		if r.NotTested {
			line("not tested", "%s", orNot(r.Cause))
		} else {
			line("cause", "%s", orNot(r.Cause))
		}
		line("tree", "%s", orNot(r.Tree))
		line("edit", "%s", orNot(r.Edit))
		if r.LatencyMs == nil {
			line("latency", "%s", notRecorded)
		} else {
			line("latency", "%s ms from edit to verdict", strconv.FormatFloat(*r.LatencyMs, 'f', -1, 64))
		}
		w.detailLine(line)
		return b.String()
	}
	if f := w.ShadowFire; f != nil {
		line("rule", "%s", f.Rule)
		line("live rule", "%s", orNot(f.LiveRule))
		line("trellis", "%s", f.Trellis)
		line("aphrollo", "%s", f.Aphrollo)
		line("relation", "%s", f.Relation)
		line("held out", "%s", map[bool]string{true: "yes", false: "no"}[f.HeldOut])
		line("key", "%s", orNot(f.Key))
		line("outcome", "%s", f.Outcome)
		line("this rule", "%s", f.Counts.summary())
		w.detailLine(line)
		return b.String()
	}
	w.logged(line)
	line("note", "why replays a deny, a run result or a shadow fire; this is a %s event", w.Kind)
	return b.String()
}

// logged prints the record's own fields that the kind-specific lines do not.
func (w Why) logged(line func(label, format string, a ...any)) {
	line("stage", "%s", orNot(w.Stage))
	line("verdict", "%s", orNot(w.Verdict))
	w.detailLine(line)
}

// detailLine prints the record's detail, every key, sorted.
func (w Why) detailLine(line func(label, format string, a ...any)) {
	var kv []string
	for _, k := range slices.Sorted(maps.Keys(w.Detail)) {
		kv = append(kv, k+"="+w.Detail[k])
	}
	line("detail", "%s", orNone(strings.Join(kv, " ")))
}

func (d *DenyWhy) overrideText() string {
	window := fmt.Sprintf("%.0f min", WrongBlockWindow.Minutes())
	if d.Override == nil {
		return "none within " + window
	}
	verdict := "not within " + window
	if d.WrongBlock {
		verdict = "a wrong block, within " + window
	}
	after := time.Duration(d.Override.AfterSecs * float64(time.Second)).Round(time.Millisecond)
	return fmt.Sprintf("%s (seq %d) %s after: %s", orNot(d.Override.Name), d.Override.Seq, after, verdict)
}

func (d *DenyWhy) kernelLines(line func(label, format string, a ...any)) {
	k := d.Kernel
	if k == nil {
		line("kernel", "no row for this rule in the rule table")
		return
	}
	line("kernel", "%s: level %s, class %s, %s", d.Rule, k.Level, k.Class, k.Section)
	switch {
	case !k.Shadowable:
		line("holdout", "never shadowed (class %s)", k.Class)
	case k.InHoldout:
		line("holdout", "this lane is in the shadow arm")
	default:
		line("holdout", "this lane is not in the shadow arm")
	}
}

func orNot(s string) string {
	if s == "" {
		return notRecorded
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
