package measure

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// onOffLane is a lane in the on arm (enforce) or the off arm, merged lead seconds after its
// first event, with escapes escape records.
func onOffLane(i int, arm string, lead float64, escapes int) []tdd.Event {
	lane := fmt.Sprintf("lane/%s-%d", arm, i)
	sec := float64(i) * 10000
	out := []tdd.Event{ev(sec, lane, "lane-arm", detail("arm", arm, "why", "assigned", "mode", arm))}
	for k := range escapes {
		out = append(out, ev(sec+lead+1+float64(k), lane, "escape", verdictDetail("escape", "id", fmt.Sprintf("%s-%d", lane, k))))
	}
	return append(out, ev(sec+lead, lane, "merge", verdictDetail("ok", "pr", fmt.Sprint(i))))
}

func onOffMetric(t *testing.T, ab AB, name string) OnOffMetric {
	t.Helper()
	for _, m := range ab.OnOff.Metrics {
		if strings.HasPrefix(m.Name, name) {
			return m
		}
	}
	t.Fatalf("no on/off metric %q in %+v", name, ab.OnOff.Metrics)
	return OnOffMetric{}
}

func TestOnOff_ThePrimaryMetricIsEscapesPerLaneAndTheVerdictNamesTheArm(t *testing.T) {
	var evs []tdd.Event
	for i := range 12 {
		evs = append(evs, onOffLane(i, "enforce", 600, 1)...) // the on lanes each let an escape through
		evs = append(evs, onOffLane(i, "off", 300, 0)...)
	}
	ab := computeAB(evs, Options{})
	if !ab.OnOff.Active || ab.OnOff.On.Lanes != 12 || ab.OnOff.Off.Lanes != 12 {
		t.Fatalf("on/off = %+v, want active with 12 lanes each", ab.OnOff)
	}
	m := onOffMetric(t, ab, "escapes per lane")
	if m.Role != "primary" || m.On != 1 || m.Off != 0 || m.NOn != 12 || m.NOff != 12 || m.Diff != 1 {
		t.Errorf("escapes per lane = %+v, want on 1, off 0, n 12 each, diff 1", m)
	}
	if m.Verdict != "decided: off better" || ab.OnOff.Verdict != "decided: off better" || !ab.OnOff.Decidable {
		t.Errorf("verdict = %q / %q, want decided: off better", m.Verdict, ab.OnOff.Verdict)
	}
	lead := onOffMetric(t, ab, "lead time")
	if lead.On != 600 || lead.Off != 300 || lead.Role != "guardrail" {
		t.Errorf("lead time = %+v, want on 600s, off 300s", lead)
	}
}

func TestOnOff_FrictionIsZeroByDefinitionForOffLanesAndSaysSo(t *testing.T) {
	var evs []tdd.Event
	for i := range 3 {
		evs = append(evs, onOffLane(i, "enforce", 600, 0)...)
		evs = append(evs, ev(float64(i)*10000+5, fmt.Sprintf("lane/enforce-%d", i), "commit_gate_result",
			func(e *tdd.Event) { e.Verdict = "blocked"; e.Secs = 30 }))
		evs = append(evs, onOffLane(i, "off", 600, 0)...)
	}
	ab := computeAB(evs, Options{})
	fr := onOffMetric(t, ab, "friction: blocks")
	if fr.On != 1 || fr.Off != 0 || fr.NOff != 3 {
		t.Errorf("blocks per lane = %+v, want on 1, off 0 over 3 off lanes", fr)
	}
	wait := onOffMetric(t, ab, "friction: gate wait")
	if wait.On != 30 || wait.Off != 0 {
		t.Errorf("gate wait = %+v, want on 30s, off 0", wait)
	}
	if !strings.Contains(ab.OnOff.Text(), "off lanes count 0 by definition") {
		t.Errorf("text does not label the friction rows:\n%s", ab.OnOff.Text())
	}
}

func TestOnOff_AgentCostComesFromTheLaneCostTheCallerJoined(t *testing.T) {
	var evs []tdd.Event
	cost := map[string]float64{}
	for i := range 3 {
		evs = append(evs, onOffLane(i, "enforce", 600, 0)...)
		evs = append(evs, onOffLane(i, "off", 600, 0)...)
		cost[fmt.Sprintf("lane/enforce-%d", i)] = 4
		cost[fmt.Sprintf("lane/off-%d", i)] = 3
	}
	ab := computeAB(evs, Options{LaneCost: cost})
	c := onOffMetric(t, ab, "agent cost")
	if c.On != 4 || c.Off != 3 || c.NOn != 3 {
		t.Errorf("agent cost = %+v, want on $4, off $3", c)
	}
}

func TestOnOff_ALogWithNoOffLaneHasNoSection(t *testing.T) {
	ab := computeAB(abCat(abLane(0, "lane/a", "enforce", "go"), abLane(100, "lane/b", "warn", "go")), Options{})
	if ab.OnOff.Active || strings.Contains(ab.Text(), "on vs off") {
		t.Errorf("on/off = %+v, want no section without an off lane", ab.OnOff)
	}
}

// Off lanes are in neither arm of the enforce/warn A/B, and a pinned lane is in neither side.
func TestOnOff_OffLanesDoNotEnterTheEnforceWarnArms(t *testing.T) {
	evs := abCat(abLane(0, "lane/a", "enforce", "go"), onOffLane(1, "off", 60, 0),
		[]tdd.Event{ev(50, "lane/pin", "lane-arm", detail("why", "pinned", "mode", "enforce"))})
	ab := computeAB(evs, Options{})
	if e, w := abRow(t, ab, "enforce"), abRow(t, ab, "warn"); e.Lanes != 1 || w.Lanes != 0 || ab.Pinned != 1 {
		t.Errorf("enforce %d, warn %d lanes, pinned %d; want 1, 0, 1", e.Lanes, w.Lanes, ab.Pinned)
	}
	if ab.OnOff.On.Lanes != 1 || ab.OnOff.Off.Lanes != 1 {
		t.Errorf("on/off lanes = %d / %d, want 1 / 1", ab.OnOff.On.Lanes, ab.OnOff.Off.Lanes)
	}
}

func TestOnOff_TheTextNamesTheSectionTheArmsAndTheStopRule(t *testing.T) {
	var evs []tdd.Event
	for i := range 2 {
		evs = append(evs, onOffLane(i, "enforce", 600, 0)...)
		evs = append(evs, onOffLane(i, "off", 600, 0)...)
	}
	txt := computeAB(evs, Options{}).Text()
	for _, want := range []string{"on vs off", "on  ", "off ", "escapes per lane (primary): on 0.00 (n 2), off 0.00 (n 2)", "verdict (escapes per lane): deciding"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text lacks %q:\n%s", want, txt)
		}
	}
}
