package report

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// now is Wednesday of ISO week 41, 2026.
var now = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

const week = 7 * 24 * time.Hour

// evAt is an event ageMin minutes before now.
func evAt(seq int64, ageMin float64, kind, lane, verdict string, kv ...string) tdd.Event {
	d := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		d[kv[i]] = kv[i+1]
	}
	at := now.Add(-time.Duration(ageMin * float64(time.Minute)))
	return tdd.Event{V: 1, Seq: seq, At: at.Format(time.RFC3339), Lane: lane, Kind: kind, Verdict: verdict, Detail: d}
}

func build(evs []tdd.Event) Report {
	return Build(Input{Events: evs, Now: now, Window: week, Repo: "aphrollo-tools"})
}

func frictionRow(t *testing.T, r Report, rule string) Friction {
	t.Helper()
	for _, f := range r.Friction {
		if f.Rule == rule {
			return f
		}
	}
	t.Fatalf("no friction row for %q in %+v", rule, r.Friction)
	return Friction{}
}

func TestBuild_FrictionCountsDeniesOverridesRefusalsAndNotTestedPerRule(t *testing.T) {
	gate := evAt(30, 40, "commit_gate", "lane/a", "lint-blocked")
	gate.Secs = 12
	timeout := evAt(31, 41, "stage.timing", "lane/a", "TIMEOUT")
	timeout.Secs = 90
	evs := []tdd.Event{
		evAt(10, 100, "deny", "lane/a", "pretooluse-denied:ratchet:module_size", "rule", "ratchet:module_size", "cause", "law"),
		evAt(11, 90, "deny", "lane/a", "pretooluse-denied:ratchet:module_size", "rule", "ratchet:module_size", "cause", "law"),
		evAt(12, 95, "override", "lane/a", "override-law-escape-comment", "override", "override-law-escape-comment"),
		gate, timeout,
		evAt(32, 42, "run.result", "lane/a", "not-tested", "result", "not-tested", "cause", "deferred"),
		evAt(33, 43, "run.result", "lane/a", "green", "result", "green"),
	}
	r := build(evs)
	ms := frictionRow(t, r, "ratchet:module_size")
	if ms.Denies != 2 || ms.Overrides != 1 {
		t.Errorf("module_size denies/overrides = %d/%d, want 2/1", ms.Denies, ms.Overrides)
	}
	if !slices.Equal(ms.Refs.Seqs, []int64{10, 11, 12}) {
		t.Errorf("module_size seqs = %v, want [10 11 12]", ms.Refs.Seqs)
	}
	lint := frictionRow(t, r, "gate:lint-blocked")
	if lint.Refusals != 1 || lint.SecsLost != 12 {
		t.Errorf("lint refusals/secs = %d/%v, want 1/12", lint.Refusals, lint.SecsLost)
	}
	to := frictionRow(t, r, "gate:TIMEOUT")
	if to.NotTested != 1 || to.SecsLost != 90 {
		t.Errorf("TIMEOUT not-tested/secs = %d/%v, want 1/90", to.NotTested, to.SecsLost)
	}
	if got := frictionRow(t, r, "run:deferred"); got.NotTested != 1 {
		t.Errorf("run:deferred not-tested = %d, want 1 (a green run is not counted)", got.NotTested)
	}
}

func TestBuild_ARefusalVerdictOfTheCommitGateCountsAsARefusalNotAsNotTested(t *testing.T) {
	r := build([]tdd.Event{evAt(1, 5, "commit_gate", "lane/a", "ratchet-rejected"), evAt(2, 5, "commit_gate", "lane/a", "ratchet-clean")})
	if len(r.Friction) != 1 || r.Friction[0].Refusals != 1 || r.Friction[0].NotTested != 0 {
		t.Errorf("friction = %+v, want one refusal row (a clean verdict is no friction)", r.Friction)
	}
}

func TestBuild_NotTestedGateVerdictsAreFoundWhateverTheirSpelling(t *testing.T) {
	var evs []tdd.Event
	for i, v := range []string{"SKIPPED", "QUEUED-SKIPPED", "NOT RUN", "NOT MEASURED", "mutants-unmeasured:commit-budget", "suites-not-run", "mutants-skipped:nothing-to-measure"} {
		evs = append(evs, evAt(int64(i+1), 5, "commit_gate", "lane/a", v))
	}
	r := build(evs)
	total := 0
	for _, f := range r.Friction {
		total += f.NotTested
	}
	if total != 6 {
		t.Errorf("not-tested total = %d, want 6 (nothing-to-measure is a skip with nothing to test)", total)
	}
}

func TestBuild_AnOverrideSoonAfterADenyIsAWrongBlockCandidateOfThatRule(t *testing.T) {
	evs := []tdd.Event{
		evAt(1, 60, "deny", "lane/a", "", "rule", "disabled-test"),
		evAt(2, 58, "override", "lane/a", "", "override", "override-x"),
		evAt(3, 30, "deny", "lane/a", "", "rule", "disabled-test"),
		evAt(4, 20, "deny", "lane/b", "", "rule", "test-sleep"),
		evAt(5, 19, "override", "lane/b", "", "override", "override-y"),
		evAt(6, 1, "override", "lane/b", "", "override", "override-late"),
	}
	r := build(evs)
	byRule := map[string]WrongBlock{}
	for _, w := range r.WrongBlocks {
		byRule[w.Rule] = w
	}
	if w := byRule["disabled-test"]; w.Denies != 2 || w.Waived != 1 || w.Rate != "50%" {
		t.Errorf("disabled-test = %+v, want 2 denies, 1 waived, 50%%", w)
	}
	if w := byRule["test-sleep"]; w.Waived != 1 || w.Denies != 1 {
		t.Errorf("test-sleep = %+v, want its one deny waived once (the second override is 19 minutes late)", w)
	}
}

func TestBuild_StandDownsAreCountedPerMatcherKind(t *testing.T) {
	r := build([]tdd.Event{
		evAt(1, 5, "commit_gate", "lane/a", "standdown-unknown-matcher-kind:tautology"),
		evAt(2, 4, "merge_gate", "lane/a", "standdown-unknown-matcher-kind:tautology"),
		evAt(3, 3, "commit_gate", "lane/a", "standdown-unknown-matcher-kind:test_sleep"),
	})
	if len(r.Standdowns) != 2 || r.Standdowns[0].Matcher != "unknown-matcher-kind:tautology" || r.Standdowns[0].N != 2 {
		t.Errorf("standdowns = %+v, want tautology x2 first, then test_sleep", r.Standdowns)
	}
	if len(r.Friction) != 0 {
		t.Errorf("a stand-down is no friction row: %+v", r.Friction)
	}
}

func TestBuild_EscapesAreGroupedByClassWithTheStageThatShouldHaveCaughtThem(t *testing.T) {
	r := build([]tdd.Event{
		evAt(1, 5, "escape", "main", "escape", "class", "product", "from_ci", "test"),
		evAt(2, 5, "escape", "main", "escape", "class", "product"),
		evAt(3, 5, "escape", "main", "escape", "class", "canary", "check", "gitworld:x"),
		evAt(4, 5, "escape", "main", "escape", "class", "disagreement", "check", "merge:y"),
		evAt(5, 5, "escape", "main", "false-positive"),
	})
	got := map[string]bool{}
	for _, e := range r.Escapes.Rows {
		got[e.Class+"/"+e.Caught] = true
	}
	for _, want := range []string{
		"product/local test gate (CI job test caught it)",
		"product/not recorded (pass --check to `aphrollo gate escape record`)",
		"canary/test isolation (gitworld)",
		"disagreement/commit gate (an earlier stage passed what the merge gate refused)",
	} {
		if !got[want] {
			t.Errorf("no escape row %q in %v", want, got)
		}
	}
	if r.Escapes.FalsePositives != 1 {
		t.Errorf("false positives = %d, want 1", r.Escapes.FalsePositives)
	}
}

func TestBuild_OnlyTheWindowIsCounted(t *testing.T) {
	old := evAt(1, 8*24*60, "deny", "lane/a", "", "rule", "old-rule")
	r := build([]tdd.Event{old, evAt(2, 5, "deny", "lane/a", "", "rule", "new-rule")})
	if len(r.Friction) != 1 || r.Friction[0].Rule != "new-rule" {
		t.Errorf("friction = %+v, want only new-rule (the other is 8 days old)", r.Friction)
	}
}

func TestBuild_RefsAreCappedAndSaySoNotSilently(t *testing.T) {
	var evs []tdd.Event
	for i := 1; i <= 12; i++ {
		evs = append(evs, evAt(int64(i), 5, "deny", "lane/a", "", "rule", "r"))
	}
	f := build(evs).Friction[0]
	if len(f.Refs.Seqs) != maxRefs || f.Refs.More != 12-maxRefs {
		t.Errorf("refs = %d seqs + %d more, want %d + %d", len(f.Refs.Seqs), f.Refs.More, maxRefs, 12-maxRefs)
	}
}

func TestTitle_IsTheISOWeekOfTheDate(t *testing.T) {
	for date, want := range map[time.Time]string{
		now: "Report 2026-W41",
		time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC):   "Report 2026-W53",
		time.Date(2024, 12, 30, 0, 0, 0, 0, time.UTC): "Report 2025-W01",
	} {
		if got := Title(date); got != want {
			t.Errorf("Title(%v) = %q, want %q", date, got, want)
		}
	}
}

func TestText_NamesEverySectionAndHowToReplayASeq(t *testing.T) {
	text := build([]tdd.Event{evAt(7, 5, "deny", "lane/a", "", "rule", "r")}).Text()
	for _, want := range []string{"1. Friction per rule", "2. Wrong-block candidates", "3. Escapes", "4. A/B and shadow", "5. Token cost", "6. Proposals", "aphrollo why <seq>"} {
		if !strings.Contains(text, want) {
			t.Errorf("report text lacks %q:\n%s", want, text)
		}
	}
}

func TestBuild_GateTimeIsSplitIntoWaitedAndRanWhileTheAgentWorked(t *testing.T) {
	waited := evAt(1, 10, "stage.timing", "lane/a", "TIMEOUT")
	waited.Secs = 100
	bg := evAt(2, 9, "stage.timing", "lane/b", "deferred-timeout")
	bg.Secs = 40
	f := frictionRow(t, build([]tdd.Event{waited, bg}), "gate:TIMEOUT")
	if f.SecsLost != 100 {
		t.Errorf("time the agent waited = %v, want 100", f.SecsLost)
	}
	g := frictionRow(t, build([]tdd.Event{waited, bg}), "gate:deferred-timeout")
	if g.SecsLost != 0 || g.SecsBackground != 40 {
		t.Errorf("deferred run waited/background = %v/%v, want 0/40: the agent kept working", g.SecsLost, g.SecsBackground)
	}
}

func TestBuild_ARuleOnTwoLanesAndDaysIsOneRowSummedFromItsCells(t *testing.T) {
	r := build([]tdd.Event{
		evAt(1, 5, "deny", "lane/a", "", "rule", "r", "file", "x.go"),
		evAt(2, 30*60, "deny", "lane/b", "", "rule", "r", "file", "y.py"),
	})
	if len(r.Friction) != 1 || r.Friction[0].Denies != 2 {
		t.Errorf("friction = %+v, want one row of 2 denies from its two cells' sum", r.Friction)
	}
}

func TestBuild_ADetailThatMentionsUnmeasuredDoesNotMakeAPassedVerdictNotTested(t *testing.T) {
	r := build([]tdd.Event{
		evAt(1, 5, "commit_gate", "lane/a", "mutants-passed:tested=30,caught=30,unmeasured=0"),
		evAt(2, 5, "stage.timing", "lane/a", "bash-edit:internal/timeout/skipped_test.go"),
		evAt(3, 5, "commit_gate", "lane/a", "mutants-unmeasured:commit-budget"),
	})
	if len(r.Friction) != 1 || r.Friction[0].Rule != "gate:mutants-unmeasured" {
		t.Errorf("friction = %+v, want only mutants-unmeasured (a detail or a path is not the verdict)", r.Friction)
	}
}

func TestBuild_AnEventWithNoSeqIsCountedButNamesNoReplayCommand(t *testing.T) {
	r := build([]tdd.Event{evAt(0, 5, "deny", "lane/a", "", "rule", "r"), evAt(9, 5, "deny", "lane/a", "", "rule", "r")})
	f := r.Friction[0]
	if f.Denies != 2 || !slices.Equal(f.Refs.Seqs, []int64{9}) {
		t.Errorf("denies %d, seqs %v, want 2 denies and only the replayable seq 9", f.Denies, f.Refs.Seqs)
	}
}

func versioned(ver string, e tdd.Event) tdd.Event {
	e.BinVer = ver
	return e
}

// An update moves the binary under the log, so the report says which versions
// its window spans, and with ByVersion reads each one on its own.
func TestBuild_NamesTheVersionsTheWindowSpansAndSplitsThemOnRequest(t *testing.T) {
	evs := []tdd.Event{
		versioned("1.0.0", evAt(1, 300, "deny", "lane/a", "pretooluse-denied:r", "rule", "r")),
		versioned("1.1.0", evAt(2, 200, "deny", "lane/b", "pretooluse-denied:r", "rule", "r")),
		versioned("1.1.0", evAt(3, 100, "deny", "lane/b", "pretooluse-denied:r", "rule", "r")),
	}
	r := build(evs)
	if len(r.Versions) != 2 || r.Versions[0].Version != "1.0.0" || r.Versions[1].Events != 2 {
		t.Fatalf("Versions = %+v, want 1.0.0 (1) and 1.1.0 (2)", r.Versions)
	}
	if want := "versions in this window: 1.0.0 (1 event), 1.1.0 (2 events)"; !strings.Contains(r.Text(), want) {
		t.Fatalf("the text lacks %q:\n%s", want, r.Text())
	}
	if len(r.ByVersion) != 0 {
		t.Fatalf("ByVersion = %+v without being asked", r.ByVersion)
	}

	r = Build(Input{Events: evs, Now: now, Window: week, Repo: "aphrollo-tools", ByVersion: true})
	if len(r.ByVersion) != 2 || r.ByVersion[0].Version != "1.0.0" || r.ByVersion[0].Denies != 1 || r.ByVersion[1].Denies != 2 {
		t.Fatalf("ByVersion = %+v, want denies 1 for 1.0.0 and 2 for 1.1.0", r.ByVersion)
	}
	if !strings.Contains(r.Text(), "8. By version") {
		t.Fatalf("the text has no by-version section:\n%s", r.Text())
	}
}

// A log of events written before they carried a version names none, so the
// report reads exactly as it did.
func TestBuild_ALogWithNoVersionsSaysNothingOfVersions(t *testing.T) {
	r := build([]tdd.Event{evAt(1, 100, "deny", "lane/a", "pretooluse-denied:r", "rule", "r")})
	if strings.Contains(r.Text(), "versions in this window") {
		t.Fatalf("the text names versions for a log that carries none:\n%s", r.Text())
	}
}

func TestBuild_ThePreviousWindowIsCountedBesideThisOneForTheChangeSinceLastWeek(t *testing.T) {
	const day = 24 * 60
	prevGate := evAt(40, 8*day, "commit_gate", "lane/a", "lint-blocked")
	prevGate.Secs = 30
	evs := []tdd.Event{
		evAt(10, 100, "run.result", "lane/a", "not-tested", "result", "not-tested", "cause", "deferred"),
		evAt(11, 8*day, "run.result", "lane/a", "not-tested", "result", "not-tested", "cause", "deferred"),
		evAt(12, 9*day, "run.result", "lane/a", "not-tested", "result", "not-tested", "cause", "deferred"),
		evAt(13, 15*day, "run.result", "lane/a", "not-tested", "result", "not-tested", "cause", "deferred"), // two windows back: in neither
		prevGate,
		evAt(41, 9*day, "escape", "lane/a", "escape", "class", "product"),
	}
	r := build(evs)
	if got := frictionRow(t, r, "run:deferred").Prev; got != 2 {
		t.Errorf("run:deferred prev = %d, want 2 (the two runs of the week before, not the one two weeks back)", got)
	}
	p := r.Previous
	if p == nil {
		t.Fatal("no previous window on a windowed report")
	}
	if p.NotTested != 2 || p.Refusals != 1 || p.SecsLost != 30 || p.Escapes != 1 || p.Events != 4 {
		t.Errorf("previous = %+v, want 2 not tested, 1 refusal, 30s waited, 1 escape, 4 events", *p)
	}
	if len(p.Gone) != 1 || p.Gone[0] != (RuleCount{Rule: "gate:lint-blocked", N: 1}) {
		t.Errorf("gone = %+v, want gate:lint-blocked 1: a rule seen only the week before is named, so its drop to zero shows", p.Gone)
	}
	whole := Build(Input{Events: evs, Now: now, Repo: "aphrollo-tools"})
	if whole.Previous != nil {
		t.Errorf("the whole-log report has a previous window: %+v", *whole.Previous)
	}
}

func TestBuild_AnEmptyWindowBeforeIsNoComparison(t *testing.T) {
	r := build([]tdd.Event{evAt(10, 100, "run.result", "lane/a", "not-tested", "result", "not-tested", "cause", "deferred")})
	if r.Previous != nil {
		t.Errorf("previous = %+v, want none: a window before the log began would read every number as all new", *r.Previous)
	}
	if got := frictionRow(t, r, "run:deferred").Prev; got != 0 {
		t.Errorf("prev = %d, want 0", got)
	}
}
