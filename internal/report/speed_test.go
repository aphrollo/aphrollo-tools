package report

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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
	if row := speedRow(t, r, "commit gate: go test"); row.N != 1 || row.P50 != 30 {
		t.Errorf("commit gate stage = %+v", row)
	}
	if row := speedRow(t, r, "commit gate: lint"); row.N != 1 || row.P50 != 8 {
		t.Errorf("a refused stage must count: %+v", row)
	}
	if row := speedRow(t, r, "merge gate: go test"); row.P50 != 100 {
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
	for _, want := range []string{"mutation at commit"} {
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

func TestSpeed_OnlyTheStageLinesOfATimedKindCount(t *testing.T) {
	evs := []tdd.Event{
		timed(evAt(1, 50, "gate", "l", "green"), "postedit", "", 5),
		timed(evAt(2, 49, "queue", "l", "queue-waiting"), "postedit", "", 7),
		timed(evAt(3, 48, "ci", "l", "green"), "ci", "", 0),
	}
	evs[2].Detail = map[string]string{"secs": "600"}
	r := build(evs)
	if len(r.Speed.Rows) != 1 || r.Speed.Rows[0].Stage != "CI pipeline" || r.Speed.Rows[0].P50 != 600 {
		t.Errorf("rows = %+v, want only the CI pipeline of 600s (a gate or queue line is no suite run)", r.Speed.Rows)
	}
}

func TestSecsText_AndSignedSecsReadLiteralValues(t *testing.T) {
	for _, c := range []struct {
		got, want string
	}{
		{secsText(9.5), "9.5s"}, {secsText(10), "10s"}, {secsText(90), "1.5m"},
		{signedSecs(0), "0s"}, {signedSecs(3), "+3s"}, {signedSecs(-3), "-3s"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestSpeedDeltaCell_NoChangeIsNeitherBetterNorWorse(t *testing.T) {
	zero := 0.0
	if got, want := string(speedDeltaCell(SpeedRow{Change: &zero, Clear: true, PrevP50: 4})), `<td class="n muted" title="4s the window before">~</td>`; got != want {
		t.Errorf("cell = %s, want %s", got, want)
	}
}

func TestSecsText_RoundsToATenthBelowTenSecondsAndTrimsTheZero(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{signedSecs(4.1 - 3.6), "+0.5s"},
		{secsText(0.5000000000000004), "0.5s"},
		{secsText(3), "3s"},
		{secsText(9.96), "10s"},
		{signedSecs(-(4.1 - 3.6)), "-0.5s"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestSpeed_APairWhoseEndsAreEqualOrBackwardsIsNoDuration(t *testing.T) {
	evs := []tdd.Event{
		evAt(1, 100, "pr_opened", "l", "ok", "pr", "1"),
		evAt(2, 100, "merge", "l", "ok", "pr", "1", "method", "squash"),
		evAt(3, 90, "pr_opened", "l", "ok", "pr", "2"),
		evAt(4, 95, "merge", "l", "ok", "pr", "2", "method", "squash"),
		evAt(5, 80, "merge", "l", "queued", "pr", "3", "method", "merge queue"),
		evAt(6, 80, "merge", "l", "ok", "pr", "3", "method", "merge queue"),
	}
	if rows := build(evs).Speed.Rows; len(rows) != 0 {
		t.Errorf("rows = %+v, want none: a zero or negative span is a skewed clock, not a run", rows)
	}
}

func TestSpeed_MergeQueueCountsFromTheLastEnqueueBeforeTheMerge(t *testing.T) {
	evs := []tdd.Event{
		evAt(1, 90, "merge", "l", "queued", "pr", "5", "method", "merge queue"),
		evAt(2, 30, "merge", "l", "queued", "pr", "5", "method", "merge queue"),
		evAt(3, 10, "merge", "l", "ok", "pr", "5", "method", "merge queue"),
	}
	if row := speedRow(t, build(evs), "merge queue"); row.P50 != 1200 {
		t.Errorf("merge queue = %+v, want 1200s from the re-queue", row)
	}
}

func TestSpeed_AClearChangeOfExactlyZeroReadsAsNoChangeEverywhere(t *testing.T) {
	zero := 0.0
	row := SpeedRow{Stage: "edit suite", N: 5, P50: 4, PrevN: 5, PrevP50: 4, Change: &zero, Clear: true, P: 0.01}
	if got := row.changeText(); got != "change ~" {
		t.Errorf("text = %q, want change ~", got)
	}
	r := Report{Window: "last 7d", Previous: &Previous{Window: "last 7d"}, Speed: Speed{Rows: []SpeedRow{row}}}
	if got := r.Changes(); len(got) != 1 || !strings.HasPrefix(got[0], "no clear change") {
		t.Errorf("changes = %q, want the no-change line", got)
	}
}

// ratchet: test_removed TestSpeed_OnlyMutationAtCommitIsStillNotDerivable: replaced by TestSpeed_AnEmptyClassSaysWhatWouldFillIt, which also names CI and the queue

func TestSpeed_AGateStageIsItsToolNotItsWholeCommandLine(t *testing.T) {
	evs := []tdd.Event{
		timed(evAt(1, 50, "commit_gate", "l", "ratchet-clean"), "precommit", "ratchet check", 8),
		timed(evAt(2, 49, "commit_gate", "l", "ran"), "precommit", "go test ./internal/a ./internal/b", 30),
		timed(evAt(3, 48, "commit_gate", "l", "ran"), "precommit", "go test -count=1 ./internal/c -run ^TestX$", 20),
		timed(evAt(4, 47, "commit_gate", "l", "red-proven"), "precommit", "go test ./internal/a -run ^(TestA)$", 2),
		timed(evAt(5, 46, "commit_gate", "l", "green-proven"), "precommit", "go test ./internal/a -run ^(TestA)$", 3),
		timed(evAt(6, 45, "commit_gate", "l", "tddsplit-blocked"), "precommit", "go test -count=1 ./tools/tddsplit -run ^TestDrift$", 4),
		timed(evAt(7, 44, "commit_gate", "l", "mutants-passed:tested=1"), "precommit", "mutants", 40),
		timed(evAt(8, 43, "commit_gate", "l", "lint-blocked"), "precommit", "", 5),
	}
	var got []string
	for _, row := range build(evs).Speed.Rows {
		got = append(got, fmt.Sprintf("%s n=%d", row.Stage, row.N))
	}
	want := []string{
		"commit gate: fail-first n=2", "commit gate: go test n=2", "commit gate: lint n=1",
		"commit gate: ratchet check n=1", "commit gate: tddsplit n=1", "mutation (commit) n=1",
	} // expectation-changed: the gate's mutation stage is the mutation (commit) row, not a second row for the same run
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

// The commit gate's mutation stage is the mutation run at commit: one row that
// carries it, never a gate row beside a "not derivable" line for the same run.
func TestSpeed_TheCommitGateMutationStageIsTheMutationRow(t *testing.T) {
	r := build([]tdd.Event{
		timed(evAt(1, 50, "commit_gate", "l", "mutants-passed:tested=3"), "precommit", "mutants", 90),
	})
	if row := speedRow(t, r, "mutation (commit)"); row.N != 1 || row.Max != 90 {
		t.Errorf("mutation (commit) = %+v, want the one 90s run", row)
	}
	for _, row := range r.Speed.Rows {
		if row.Stage == "commit gate: mutants" {
			t.Errorf("the run is also a gate row: %+v", row)
		}
	}
	if gaps := strings.Join(r.Speed.Gaps, "\n"); strings.Contains(gaps, "mutation at commit") {
		t.Errorf("gaps = %q: a window with a mutation run at commit must not call it not derivable", gaps)
	}
}

func TestSpeed_AnImpossibleDurationIsDroppedAndCounted(t *testing.T) {
	evs := []tdd.Event{
		timed(evAt(1, 50, "run.result", "l", "deferred-abandoned"), "postedit", "go test", 9223372036.854776),
		timed(evAt(2, 49, "stage.timing", "l", "green"), "postedit", "go test", 4),
	}
	r := build(evs)
	if row := speedRow(t, r, "edit suite"); row.N != 1 || row.Max != 4 {
		t.Errorf("edit suite = %+v, want the one real run", row)
	}
	if r.Speed.Dropped != 1 || !strings.Contains(strings.Join(r.Speed.Gaps, "\n"), "1 duration longer than 30 days") {
		t.Errorf("dropped = %d, gaps %q, want the impossible sample counted and named", r.Speed.Dropped, r.Speed.Gaps)
	}
}

func TestSpeed_AnEmptyClassSaysWhatWouldFillIt(t *testing.T) {
	gaps := strings.Join(build(nil).Speed.Gaps, "\n")
	for _, want := range []string{"mutation at commit", "CI pipeline", "merge queue"} {
		if !strings.Contains(gaps, want) {
			t.Errorf("gaps = %q, want a line for %s", gaps, want)
		}
	}
	if strings.Contains(gaps, "AppendGateLog") {
		t.Errorf("gaps = %q: the mutation duration is recorded now; the line must not ask for the fix", gaps)
	}
}

func TestGateStage_ABareCommandOrNoneStillNamesARow(t *testing.T) {
	cases := map[[2]string]string{
		{"ran", ""}:              "(unnamed)",
		{"ran", "go"}:            "go",
		{"ran", "go vet ./..."}:  "go vet",
		{"green", "go test ./a"}: "go test",
	}
	for in, want := range cases {
		if got := gateStage(in[0], in[1]); got != want {
			t.Errorf("gateStage(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestSpeed_ThirtyDaysExactlyIsKeptAndNothingDroppedSaysNothing(t *testing.T) {
	evs := []tdd.Event{timed(evAt(1, 50, "stage.timing", "l", "green"), "postedit", "go test", maxSpeedSecs)}
	r := build(evs)
	if row := speedRow(t, r, "edit suite"); row.N != 1 || row.Max != maxSpeedSecs {
		t.Errorf("edit suite = %+v, want the 30-day sample kept", row)
	}
	if r.Speed.Dropped != 0 || strings.Contains(strings.Join(r.Speed.Gaps, "\n"), "longer than 30 days") {
		t.Errorf("dropped = %d, gaps %q, want nothing dropped and no line about it", r.Speed.Dropped, r.Speed.Gaps)
	}
}

func TestSpeed_MergeQueueCountsFromTheEnqueueTheMergeRecordCarries(t *testing.T) {
	merged := evAt(2, 30, "merge", "l", "ok", "pr", "5", "method", "merge queue")
	at, _ := time.Parse(time.RFC3339, merged.At)
	merged.Detail["enqueued_at"] = at.Add(-30 * time.Minute).Format(time.RFC3339)
	if row := speedRow(t, build([]tdd.Event{merged}), "merge queue"); row.N != 1 || row.P50 != 1800 {
		t.Errorf("merge queue = %+v, want 1800s from the enqueue the merge record names", row)
	}
}

// ratchet: test_removed TestSpeed_MergeQueueAlsoCountsFromAnEnqueueTheVerbWaitedOn: the merge record now carries enqueued_at instead of a separate enqueued event; TestSpeed_MergeQueueCountsFromTheEnqueueTheMergeRecordCarries pins it

// A merge that skipped a declared command by reuse is counted with the seconds
// the lane's green took, summed: the saving is the point of the reuse.
func TestSpeed_DeclaredReuseCountsEachSkipAndSumsTheSavedSeconds(t *testing.T) {
	evs := []tdd.Event{
		evAt(1, 50, "merge_gate", "l", "declared-reuse", "saved_secs", "90"),
		evAt(2, 49, "merge_gate", "l", "declared-reuse", "saved_secs", "30.5"),
		evAt(3, 48, "merge_gate", "l", "declared-reuse", "saved_secs", "bogus"),
	}
	row := speedRow(t, build(evs), "declared reuse saved")
	if row.N != 2 || row.Sum != 120.5 {
		t.Errorf("declared reuse saved = n %d sum %v, want n 2 sum 120.5 (an unreadable saved_secs counts nothing)", row.N, row.Sum)
	}
}

// A parallel block of declared commands is counted by what it saved: the sum
// of its parts less its wall time. It is a class of its own, not a row of the
// gate it ran in, and a block that saved nothing counts nothing.
func TestSpeed_DeclaredParallelSumsWhatEachBlockSaved(t *testing.T) {
	evs := []tdd.Event{
		evAt(1, 50, "commit_gate", "l", "declared-parallel", "commands", "2", "sum_secs", "150", "wall_secs", "90"),
		evAt(2, 49, "merge_gate", "l", "declared-parallel", "commands", "3", "sum_secs", "100", "wall_secs", "40"),
		evAt(3, 48, "merge_gate", "l", "declared-parallel", "commands", "2", "sum_secs", "10", "wall_secs", "30"),
		evAt(4, 47, "merge_gate", "l", "declared-parallel", "commands", "2", "sum_secs", "bogus", "wall_secs", "30"),
	}
	r := build(evs)
	row := speedRow(t, r, "declared parallel saved")
	if row.N != 2 || row.Sum != 120 {
		t.Errorf("declared parallel saved = n %d sum %v, want n 2 sum 120", row.N, row.Sum)
	}
	for _, other := range r.Speed.Rows {
		if strings.Contains(other.Stage, "parallel") && other.Stage != "declared parallel saved" {
			t.Errorf("row %q: a parallel block made a row of its gate", other.Stage)
		}
	}
}
