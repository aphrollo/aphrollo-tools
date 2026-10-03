package measure

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

var base = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

// ev builds one event sec seconds after base.
func ev(sec float64, lane, kind string, f func(*tdd.Event)) tdd.Event {
	e := tdd.Event{V: 1, At: base.Add(time.Duration(sec * float64(time.Second))).Format("2006-01-02T15:04:05.000Z07:00"),
		Lane: lane, Kind: kind}
	if f != nil {
		f(&e)
	}
	return e
}

func detail(kv ...string) func(*tdd.Event) {
	return func(e *tdd.Event) {
		e.Detail = map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			e.Detail[kv[i]] = kv[i+1]
		}
	}
}

func verdictDetail(verdict string, kv ...string) func(*tdd.Event) {
	d := detail(kv...)
	return func(e *tdd.Event) { e.Verdict = verdict; d(e) }
}

func compute(events []tdd.Event, o Options) Report {
	return Compute(events, base.Add(24*time.Hour), o)
}

func TestPercentile_IsNearestRank(t *testing.T) {
	cases := []struct {
		in   []float64
		p    float64
		want float64
	}{
		{[]float64{10, 20, 30, 40}, 0.5, 20},
		{[]float64{10, 20, 30, 40}, 0.9, 40},
		{[]float64{10, 20, 30, 40, 50}, 0.5, 30},
		{[]float64{5}, 0.95, 5},
		{nil, 0.5, 0},
	}
	for _, c := range cases {
		if got := percentile(c.in, c.p); got != c.want {
			t.Errorf("percentile(%v, %v) = %v, want %v", c.in, c.p, got, c.want)
		}
	}
}

func TestSpeed_LaneFirstEventToItsOwnMerge(t *testing.T) {
	events := []tdd.Event{
		ev(0, "lane/a", "edit", nil),
		ev(3600, "lane/a", "merge", verdictDetail("ok", "pr", "1")),
		ev(10, "lane/b", "hook.timing", nil),
		ev(7210, "lane/b", "merge", verdictDetail("ok", "pr", "2")),
		// A merge made outside the verb has no lane that opened it.
		ev(100, "main", "merge", verdictDetail("ok", "by", "outside")),
		// A merge of a lane with no earlier event says nothing.
		ev(500, "lane/c", "merge", verdictDetail("ok")),
	}
	got := compute(events, Options{}).Speed
	want := Dist{N: 2, P50: 3600, P90: 7200, P95: 7200}
	if got != want {
		t.Fatalf("speed = %+v, want %+v", got, want)
	}
}

func TestSpeed_ALaneNameReusedAfterItsMergeStartsAgain(t *testing.T) {
	events := []tdd.Event{
		ev(0, "lane/a", "edit", nil),
		ev(100, "lane/a", "merge", verdictDetail("ok")),
		ev(1000, "lane/a", "edit", nil),
		ev(1500, "lane/a", "merge", verdictDetail("ok")),
	}
	got := compute(events, Options{}).Speed
	if got.N != 2 || got.P50 != 100 || got.P90 != 500 {
		t.Fatalf("speed = %+v, want two lanes of 100s and 500s", got)
	}
}

func TestCI_OnlyTheFirstResultOfALaneCountsByCauseAndOS(t *testing.T) {
	events := []tdd.Event{
		ev(1, "lane/a", "ci", verdictDetail("red", "cause", "test", "os", "windows")),
		ev(2, "lane/a", "ci", verdictDetail("green", "os", "windows")),
		ev(3, "lane/b", "ci", verdictDetail("green", "os", "linux")),
		ev(4, "lane/c", "ci", verdictDetail("red", "cause", "mutation", "os", "linux")),
		ev(5, "lane/d", "ci", verdictDetail("red")),
	}
	got := compute(events, Options{}).CI
	want := CI{
		Lanes: 4, Green: 1, Rate: 25,
		RedByCause: []Count{{"mutation", 1}, {"other", 1}, {"test", 1}},
		ByOS:       []OSRate{{"linux", 2, 1}, {"unknown", 1, 0}, {"windows", 1, 0}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ci = %+v, want %+v", got, want)
	}
}

func TestGate_SumsStageAndHookTimingPerLane(t *testing.T) {
	secs := func(s float64) func(*tdd.Event) { return func(e *tdd.Event) { e.Secs = s } }
	events := []tdd.Event{
		ev(1, "lane/a", "stage.timing", secs(2.5)),
		ev(2, "lane/a", "hook.timing", secs(1.5)),
		ev(3, "lane/a", "commit_gate", secs(99)),
		ev(4, "lane/b", "hook.timing", secs(1)),
	}
	got := compute(events, Options{}).Gate
	want := Gate{TotalSecs: 5, Lanes: []LaneSecs{{"lane/a", 4}, {"lane/b", 1}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gate = %+v, want %+v", got, want)
	}
}

func TestRuns_NotTestedShareByCauseAndEditToVerdictLatency(t *testing.T) {
	events := []tdd.Event{
		ev(1, "l", "run.result", detail("result", "green", "latency_ms", "100")),
		ev(2, "l", "run.result", detail("result", "green", "latency_ms", "200")),
		ev(3, "l", "run.result", detail("result", "not-tested", "cause", "timeout")),
		ev(4, "l", "run.result", detail("result", "not-tested", "cause", "timeout")),
		ev(5, "l", "run.result", detail("result", "not-tested", "cause", "skipped")),
	}
	got := compute(events, Options{}).Runs
	want := Runs{
		Total: 5, NotTested: 3, NotTestedShare: 0.6,
		Causes:  []Count{{"timeout", 2}, {"skipped", 1}},
		Latency: Dist{N: 2, P50: 100, P90: 200, P95: 200},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runs = %+v, want %+v", got, want)
	}
}

func TestEdits_CountedBetweenUserPromptSubmitBoundariesOfASession(t *testing.T) {
	actor := func(a string) func(*tdd.Event) { return func(e *tdd.Event) { e.Actor = a } }
	boundary := func(a string) func(*tdd.Event) {
		return func(e *tdd.Event) { e.Actor = a; e.Detail = map[string]string{"hook": "userpromptsubmit"} }
	}
	events := []tdd.Event{
		ev(0, "l", "edit", actor("s1")), // before the session's first message: no message owns it
		ev(1, "l", "hook.timing", boundary("s1")),
		ev(2, "l", "edit", actor("s1")),
		ev(3, "l", "edit", actor("s1/agent7")), // a subagent edit belongs to its session's message
		ev(4, "l", "hook.timing", boundary("s1")),
		ev(5, "l", "edit", actor("s1")),
		ev(6, "l", "hook.timing", boundary("s2")),
		ev(7, "l", "hook.timing", func(e *tdd.Event) { e.Actor = "s1"; e.Detail = map[string]string{"hook": "posttooluse"} }),
	}
	got := compute(events, Options{}).Edits
	want := Edits{Messages: 3, Edits: 3, Mean: 1, Max: 2}
	if got != want {
		t.Fatalf("edits = %+v, want %+v", got, want)
	}
}

func TestDenies_ByRuleOverridesAndWrongBlocksWithinTenMinutes(t *testing.T) {
	events := []tdd.Event{
		ev(0, "l", "deny", detail("rule", "primary-write", "cause", "trunk")),
		ev(60, "l", "override", detail("override", "override-primary")),   // 1 min after a deny: wrong block
		ev(1200, "l", "override", detail("override", "override-primary")), // 20 min after: not
		ev(30, "m", "deny", detail("rule", "primary-write")),
		ev(5000, "m", "override", detail("override", "smell-escape:x")), // far later: not
		ev(40, "n", "override", detail("override", "override-primary")), // no deny on lane n: not
		ev(50, "l", "deny", detail("rule", "red-green")),
	}
	got := compute(events, Options{}).Denies
	want := Denies{
		Denies:      3,
		ByRule:      []Count{{"primary-write", 2}, {"red-green", 1}},
		ByCause:     []Count{{"trunk", 1}},
		Overrides:   4,
		ByOverride:  []Count{{"override-primary", 3}, {"smell-escape:x", 1}},
		WrongBlocks: 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("denies = %+v, want %+v", got, want)
	}
}

func TestEscapes_ByClassKeepsFalsePositivesApart(t *testing.T) {
	events := []tdd.Event{
		ev(1, "l", "escape", verdictDetail("escape", "class", "product")),
		ev(2, "l", "escape", verdictDetail("escape", "class", "product")),
		ev(3, "l", "escape", verdictDetail("escape", "class", "canary")),
		ev(4, "main", "escape", verdictDetail("outside-merge", "by", "outside")),
		ev(5, "l", "escape", verdictDetail("false-positive")),
	}
	got := compute(events, Options{}).Escapes
	want := Escapes{ByClass: []Count{{"product", 2}, {"canary", 1}, {"outside-merge", 1}}, FalsePositives: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("escapes = %+v, want %+v", got, want)
	}
}

func TestCompute_WindowAndLaneNarrowTheReport(t *testing.T) {
	old := ev(-3*24*3600, "lane/a", "deny", detail("rule", "old"))
	recent := ev(23*3600, "lane/a", "deny", detail("rule", "new"))
	other := ev(23*3600, "lane/b", "deny", detail("rule", "elsewhere"))
	events := []tdd.Event{old, recent, other}

	if got := compute(events, Options{Window: 7 * 24 * time.Hour}).Denies.Denies; got != 3 {
		t.Errorf("a week keeps every deny, got %d", got)
	}
	win := compute(events, Options{Window: 2 * time.Hour}).Denies
	if win.Denies != 2 || len(win.ByRule) != 2 {
		t.Errorf("a 2h window keeps the two recent denies, got %+v", win)
	}
	laneOnly := compute(events, Options{Lane: "lane/a"}).Denies
	if laneOnly.Denies != 2 {
		t.Errorf("lane filter keeps lane/a's two denies, got %+v", laneOnly)
	}
}

func TestCompute_SameEventsGiveTheSameBytes(t *testing.T) {
	events := []tdd.Event{
		ev(1, "lane/b", "deny", detail("rule", "r2")),
		ev(2, "lane/a", "deny", detail("rule", "r1")),
		ev(3, "lane/a", "ci", verdictDetail("green")),
	}
	reversed := []tdd.Event{events[2], events[1], events[0]}
	a := compute(events, Options{}).Text()
	b := compute(reversed, Options{}).Text()
	if a != b {
		t.Fatalf("event order changed the report:\n%s\n---\n%s", a, b)
	}
}

func TestTokens_IsBytesOverFourRoundedUp(t *testing.T) {
	cases := map[int]int{0: 0, 1: 1, 4: 1, 5: 2, 1600: 400, 1601: 401}
	for bytes, want := range cases {
		if got := Tokens(bytes); got != want {
			t.Errorf("Tokens(%d) = %d, want %d", bytes, got, want)
		}
	}
}

func TestCheckBriefs_MarksOnlyTheItemsOverTheirOwnCap(t *testing.T) {
	got := CheckBriefs([]Brief{
		{Name: "block", Bytes: 1600},                    // 400: at the brief cap, not over
		{Name: "skill", Bytes: 1601},                    // 401: over
		{Name: "builder", Subagent: true, Bytes: 1000},  // 250: at the subagent cap
		{Name: "reviewer", Subagent: true, Bytes: 1001}, // 251: over
	})
	want := []BriefLine{
		{Name: "block", Bytes: 1600, Tokens: 400, Cap: BriefCap, Over: false},
		{Name: "skill", Bytes: 1601, Tokens: 401, Cap: BriefCap, Over: true},
		{Name: "builder", Bytes: 1000, Tokens: 250, Cap: SubagentBriefCap, Over: false},
		{Name: "reviewer", Bytes: 1001, Tokens: 251, Cap: SubagentBriefCap, Over: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %+v, want %+v", got, want)
	}
}

func TestBriefsText_NamesEveryItemAndMarksTheOverOnes(t *testing.T) {
	text := BriefsText(CheckBriefs([]Brief{{Name: "block", Bytes: 1600}, {Name: "skill", Bytes: 1601}}))
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 2 || strings.Contains(lines[0], "OVER") || !strings.Contains(lines[1], "OVER") {
		t.Fatalf("want one line each, OVER only on the skill:\n%s", text)
	}
}

func TestDenies_AnEventWithNoRuleDetailNamesItsRuleFromItsVerdict(t *testing.T) {
	events := []tdd.Event{
		ev(1, "l", "deny", func(e *tdd.Event) { e.Verdict = "pretooluse-denied:discard-bash" }),
		ev(2, "l", "deny", func(e *tdd.Event) { e.Verdict = "commit-refused" }),
		ev(3, "l", "deny", nil),
	}
	got := compute(events, Options{}).Denies.ByRule
	want := []Count{{"commit-refused", 1}, {"discard-bash", 1}, {"unknown", 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("by rule = %+v, want %+v", got, want)
	}
}

func TestGate_AnImplausibleDurationIsCountedAndLeftOutOfTheSum(t *testing.T) {
	secs := func(s float64) func(*tdd.Event) { return func(e *tdd.Event) { e.Secs = s } }
	events := []tdd.Event{
		ev(1, "lane/a", "stage.timing", secs(2)),
		ev(2, "lane/a", "stage.timing", secs(9223372036.854776)), // a duration that overflowed
		ev(3, "lane/a", "hook.timing", secs(-1)),
	}
	got := compute(events, Options{}).Gate
	want := Gate{TotalSecs: 2, Lanes: []LaneSecs{{"lane/a", 2}}, Implausible: 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gate = %+v, want %+v", got, want)
	}
}

func TestText_ListsTheSlowestLanesAndSaysHowManyItLeftOut(t *testing.T) {
	// Lane i spends 100-i seconds, so lane/a is the slowest.
	var events []tdd.Event
	for i := 0; i < 12; i++ {
		lane := "lane/" + string(rune('a'+i))
		events = append(events, ev(float64(i), lane, "hook.timing", func(e *tdd.Event) { e.Secs = float64(100 - i) }))
	}
	text := compute(events, Options{}).Text()
	if !strings.Contains(text, "lane/a ") || strings.Contains(text, "lane/l ") {
		t.Errorf("want the slowest 10 lanes listed (lane/a..lane/j) and lane/l not:\n%s", text)
	}
	if !strings.Contains(text, "2 more lanes") {
		t.Errorf("want the two unlisted lanes counted:\n%s", text)
	}
}

// A merge the verb only queued has not landed: the lane closes at the ok event
// written once the queue has merged it, so the queue's wait is part of the speed.
func TestSpeed_AQueuedMergeDoesNotCloseTheLaneUntilTheQueueMergedIt(t *testing.T) {
	events := []tdd.Event{
		ev(0, "lane/a", "edit", nil),
		ev(60, "lane/a", "merge", verdictDetail("queued", "pr", "1")),
		ev(900, "lane/a", "merge", verdictDetail("ok", "pr", "1")),
	}
	got := compute(events, Options{}).Speed
	if got.N != 1 || got.P50 != 900 {
		t.Fatalf("speed = %+v, want one lane of 900s (opened to the queue's merge, not to the enqueue)", got)
	}
}
