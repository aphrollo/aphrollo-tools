package report

import (
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/measure"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// The report joins the lanes' agent cost from the session usage, and the escapes the
// tracker blames on a lane, into the on/off comparison.
func TestBuild_TheOnOffSectionTakesLaneCostFromUsageAndBlamedEscapesFromTheAttribution(t *testing.T) {
	evs := []tdd.Event{
		evAt(1, 600, "lane-arm", "lane/on-1", "", "arm", "enforce", "why", "assigned", "mode", "enforce"),
		evAt(2, 500, "lane-arm", "lane/off-1", "", "arm", "off", "why", "assigned", "mode", "off"),
	}
	cell := func(lane string) UsageCell {
		return UsageCell{Day: "2026-10-07", Lane: lane, Session: "s", Model: "claude-sonnet-4-6"}
	}
	usage := &UsageFacts{Repo: "aphrollo-tools", Cells: map[UsageCell]UsageTokens{
		cell("lane/on-1"):  {Fresh: 1_000_000, Turns: 1},
		cell("lane/off-1"): {Fresh: 2_000_000, Turns: 1},
	}}
	r := Build(Input{Events: evs, Now: now, Window: week, Repo: "aphrollo-tools", Usage: usage,
		Attribution:      []measure.Attribution{{Lane: "lane/on-1", Source: "issue", Key: "pr:1"}},
		AttributionNotes: []string{"runs on main not read: boom"}})
	oo := r.ABTotal.OnOff
	if !oo.Active || oo.On.Lanes != 1 || oo.Off.Lanes != 1 || oo.On.Escapes != 1 {
		t.Fatalf("on/off = %+v, want active with one lane a side and one blamed escape on the on side", oo)
	}
	var cost *measure.OnOffMetric
	for i := range oo.Metrics {
		if strings.HasPrefix(oo.Metrics[i].Name, "agent cost") {
			cost = &oo.Metrics[i]
		}
	}
	if cost == nil || cost.On != 3 || cost.Off != 6 {
		t.Errorf("agent cost = %+v, want on $3 and off $6", cost)
	}
	if txt := r.Text(); !strings.Contains(txt, "on vs off") || !strings.Contains(txt, "runs on main not read: boom") {
		t.Errorf("report text lacks the on vs off section or the note about what was not read:\n%s", txt)
	}
}
