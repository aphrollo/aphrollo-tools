package report

import (
	"fmt"
	"sort"
	"strings"
)

// Changes is what changed against the window before, the lines a reader needs
// first: the speed changes that pass the significance test (slower ones first),
// the rules that are new and the rules that are gone, and the counts of
// not-tested runs and waived denies (wrong blocks) that went up. When nothing
// passes it is the one line saying so.
func (r Report) Changes() []string {
	if r.Previous == nil {
		var lines []string
		lines = append(lines, versionChanges(r.Speed)...)
		if len(lines) == 0 {
			return []string{"no window before to compare against"}
		}
		return lines
	}
	type change struct {
		line string
		by   float64
	}
	var speed []change
	for _, row := range r.Speed.Rows {
		if row.Clear && row.Change != nil && *row.Change != 0 {
			speed = append(speed, change{fmt.Sprintf("%s: %s p50 %s -> %s (%s, p %.2g, %d runs against %d)",
				word(*row.Change), row.Stage, secsText(row.PrevP50), secsText(row.P50), signedSecs(*row.Change), row.P, row.N, row.PrevN), *row.Change})
		}
	}
	sort.SliceStable(speed, func(i, j int) bool { return speed[i].by > speed[j].by })
	var lines []string
	for _, c := range speed {
		lines = append(lines, c.line)
	}
	lines = append(lines, versionChanges(r.Speed)...)
	for _, f := range r.Friction {
		if f.Prev == 0 && f.total() > 0 {
			lines = append(lines, fmt.Sprintf("new rule: %s (%d)", f.Rule, f.total()))
		}
	}
	for _, g := range r.Previous.Gone {
		lines = append(lines, fmt.Sprintf("gone: %s (was %d)", g.Rule, g.N))
	}
	notTested, waived := 0, 0
	for _, f := range r.Friction {
		notTested += f.NotTested
	}
	for _, w := range r.WrongBlocks {
		waived += w.Waived
	}
	if notTested > r.Previous.NotTested {
		lines = append(lines, fmt.Sprintf("not-tested runs up: %d -> %d", r.Previous.NotTested, notTested))
	}
	if waived > r.Previous.Waived {
		lines = append(lines, fmt.Sprintf("wrong blocks up: %d -> %d", r.Previous.Waived, waived))
	}
	if len(lines) == 0 {
		return []string{"no clear change against the previous " + strings.TrimPrefix(r.Previous.Window, "last ")}
	}
	return lines
}

func word(change float64) string {
	if change > 0 {
		return "slower"
	}
	return "faster"
}

// versionChanges are the clear changes between a stage's adjacent binary versions.
func versionChanges(s Speed) []string {
	var out []string
	for _, row := range s.Rows {
		for _, v := range row.Versions {
			if v.Clear && v.Change != nil && *v.Change != 0 {
				out = append(out, fmt.Sprintf("%s %s: %s p50 %s -> %s (%s, p %.2g)",
					word(*v.Change), v.Label, row.Stage, secsText(v.PrevP50), secsText(v.P50), signedSecs(*v.Change), v.P))
			}
		}
	}
	return out
}
