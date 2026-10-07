package report

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// timed is e with the seconds its stage line recorded.
func timed(e tdd.Event, stage, cmd string, secs float64) tdd.Event {
	e.Stage, e.Cmd, e.Secs = stage, cmd, secs
	return e
}

func speedRow(t *testing.T, r Report, stage string) SpeedRow {
	t.Helper()
	for _, row := range r.Speed.Rows {
		if row.Stage == stage {
			return row
		}
	}
	t.Fatalf("no speed row %q in %+v", stage, r.Speed.Rows)
	return SpeedRow{}
}

func TestPercentile_IsNearestRankOverTheSamples(t *testing.T) {
	cases := []struct {
		name     string
		in       []float64
		p50, p90 float64
	}{
		{"single", []float64{7}, 7, 7},
		{"odd", []float64{3, 1, 2}, 2, 3},
		{"even takes the lower middle", []float64{4, 1, 3, 2}, 2, 4},
		{"ten", []float64{10, 9, 8, 7, 6, 5, 4, 3, 2, 1}, 5, 9},
	}
	for _, c := range cases {
		s := slices.Clone(c.in)
		slices.Sort(s)
		if got := percentile(s, 50); got != c.p50 {
			t.Errorf("%s: p50 = %v, want %v", c.name, got, c.p50)
		}
		if got := percentile(s, 90); got != c.p90 {
			t.Errorf("%s: p90 = %v, want %v", c.name, got, c.p90)
		}
	}
}

func TestSpeed_ClassifiesEveryTimedRunOfAStageGreenOrNot(t *testing.T) {
	evs := []tdd.Event{
		timed(evAt(1, 50, "stage.timing", "l", "green"), "postedit", "go test", 4),
		timed(evAt(2, 49, "stage.timing", "l", "TIMEOUT"), "postedit", "go test", 90),
		timed(evAt(3, 48, "stage.timing", "l", "green"), "preedit", "x", 3),
		timed(evAt(4, 47, "commit_gate", "l", "green"), "precommit", "go test ./...", 30),
		timed(evAt(5, 46, "commit_gate", "l", "lint-blocked"), "precommit", "golangci-lint", 8),
		timed(evAt(6, 45, "commit_gate", "l", "green"), "precommit", "go vet", 0),
		timed(evAt(7, 44, "merge_gate", "l", "green"), "premergecommit", "go test ./...", 100),
		timed(evAt(8, 43, "commit_gate_result", "l", "blocked"), "", "", 41),
		evAt(9, 42, "run.result", "l", "green", "result", "green", "latency_ms", "2500"),
		timed(evAt(10, 41, "mutants", "l", "mutants-ci-busy"), "mutants", "mutants", 60),
	}
	r := build(evs)
	if row := speedRow(t, r, "edit suite"); row.N != 2 || row.Max != 90 || row.P50 != 4 {
		t.Errorf("edit suite = %+v, want n 2 p50 4 max 90 (a timed-out run counts, preedit does not)", row)
	}
	if row := speedRow(t, r, "commit gate: go test ./..."); row.N != 1 || row.P50 != 30 {
		t.Errorf("commit gate stage = %+v", row)
	}
	if row := speedRow(t, r, "commit gate: golangci-lint"); row.N != 1 || row.P50 != 8 {
		t.Errorf("a refused stage must count: %+v", row)
	}
	if row := speedRow(t, r, "merge gate: go test ./..."); row.P50 != 100 {
		t.Errorf("merge gate stage = %+v", row)
	}
	if row := speedRow(t, r, "commit gate total"); row.P50 != 41 {
		t.Errorf("commit gate total = %+v", row)
	}
	if row := speedRow(t, r, "edit to verdict"); row.P50 != 2.5 {
		t.Errorf("edit to verdict = %+v, want 2.5s from latency_ms", row)
	}
	for _, row := range r.Speed.Rows {
		if strings.HasPrefix(row.Stage, "mutation") || strings.Contains(row.Stage, "go vet") {
			t.Errorf("row %q: a wait on CI or an untimed line is no run duration", row.Stage)
		}
	}
	if len(r.Speed.Rows) != 6 {
		t.Errorf("rows = %d, want 6: %+v", len(r.Speed.Rows), r.Speed.Rows)
	}
}

func TestSpeed_PRLeadTimeCountsOnlyAnOpenedPRThatMerged(t *testing.T) {
	evs := []tdd.Event{
		evAt(1, 200, "pr_opened", "l", "ok", "pr", "1"),
		evAt(2, 140, "merge", "l", "ok", "pr", "1", "method", "squash"),
		evAt(3, 150, "pr_opened", "l", "ok", "pr", "2"),
		evAt(4, 100, "merge", "l", "ok", "pr", "3", "method", "squash"),
		evAt(5, 90, "pr_opened", "l", "ok", "pr", "4"),
		evAt(6, 80, "merge", "l", "queued", "pr", "4", "method", "merge queue"),
	}
	row := speedRow(t, build(evs), "PR lead time")
	if row.N != 1 || row.P50 != 3600 {
		t.Errorf("PR lead time = %+v, want one PR of 3600s (open without merge and merge without open are not counted, a queued verdict is not merged)", row)
	}
}

func TestSpeed_MergeQueueIsEnqueueToTheMergeOfTheSamePR(t *testing.T) {
	evs := []tdd.Event{
		evAt(1, 60, "merge", "l", "queued", "pr", "5", "method", "merge queue"),
		evAt(2, 30, "merge", "l", "ok", "pr", "5", "method", "merge queue"),
		evAt(3, 20, "merge", "l", "ok", "pr", "6", "method", "squash"),
	}
	r := build(evs)
	if row := speedRow(t, r, "merge queue"); row.N != 1 || row.P50 != 1800 {
		t.Errorf("merge queue = %+v, want 1800s", row)
	}
}

func TestSpeed_ChangeIsTheP50AgainstTheWindowBefore(t *testing.T) {
	before := 8.0 * 24 * 60
	evs := []tdd.Event{
		timed(evAt(1, before, "stage.timing", "l", "green"), "postedit", "", 10),
		timed(evAt(2, 50, "stage.timing", "l", "green"), "postedit", "", 4),
		timed(evAt(3, before, "commit_gate", "l", "green"), "precommit", "go test", 20),
		timed(evAt(4, 40, "commit_gate", "l", "green"), "precommit", "go test", 25),
		timed(evAt(5, 30, "commit_gate", "l", "green"), "precommit", "go vet", 5),
	}
	r := build(evs)
	edit := speedRow(t, r, "edit suite")
	if edit.PrevN != 1 || edit.PrevP50 != 10 || edit.Change == nil || *edit.Change != -6 {
		t.Errorf("edit suite vs before = %+v, want prev 10 and change -6 (faster)", edit)
	}
	if c := speedRow(t, r, "commit gate: go test"); c.Change == nil || *c.Change != 5 {
		t.Errorf("a slower p50 must read as +5: %+v", c)
	}
	if c := speedRow(t, r, "commit gate: go vet"); c.Change != nil {
		t.Errorf("no run before means no change: %+v", c)
	}
}

func TestSpeed_NoTimedRunSaysNoRunsAndNamesWhatCannotBeDerived(t *testing.T) {
	r := build([]tdd.Event{evAt(1, 10, "deny", "l", "pretooluse-denied:x", "rule", "x")})
	if len(r.Speed.Rows) != 0 {
		t.Fatalf("rows = %+v, want none", r.Speed.Rows)
	}
	text := r.Text()
	if !strings.Contains(text, "no runs") {
		t.Errorf("text lacks \"no runs\":\n%s", text)
	}
	joined := strings.Join(r.Speed.Gaps, "\n")
	for _, want := range []string{"commit gate total", "CI pipeline", "mutation"} {
		if !strings.Contains(joined, want) {
			t.Errorf("gaps lack %q: %q", want, joined)
		}
	}
}

func TestSpeed_OrderAndNumbersDoNotDependOnTheInputOrder(t *testing.T) {
	evs := []tdd.Event{
		timed(evAt(1, 50, "stage.timing", "l", "green"), "postedit", "", 4),
		timed(evAt(2, 49, "commit_gate", "l", "green"), "precommit", "b", 6),
		timed(evAt(3, 48, "commit_gate", "l", "green"), "precommit", "a", 7),
		timed(evAt(4, 47, "merge_gate", "l", "green"), "premergecommit", "c", 9),
	}
	want := build(evs).Speed
	rev := slices.Clone(evs)
	slices.Reverse(rev)
	if got := build(rev).Speed; !reflect.DeepEqual(got, want) {
		t.Errorf("reversed input changed the section:\n%+v\n%+v", got, want)
	}
	var names []string
	for _, row := range want.Rows {
		names = append(names, row.Stage)
	}
	if w := []string{"edit suite", "commit gate: a", "commit gate: b", "merge gate: c"}; !slices.Equal(names, w) {
		t.Errorf("order = %q, want %q", names, w)
	}
}

func TestSpeed_JSONKeyIsSpeedAndPublishedTextCarriesTheBlock(t *testing.T) {
	r := build([]tdd.Event{timed(evAt(1, 50, "stage.timing", "l", "green"), "postedit", "", 4)})
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["speed"]; !ok {
		t.Errorf("no speed key in %s", b)
	}
	if !strings.Contains(r.Published().Text(), "edit suite") {
		t.Error("the issue text lacks the speed block")
	}
}
