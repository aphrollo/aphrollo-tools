package report

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/measure"
)

// tracker is a Fake that keeps issues in memory, so a second run sees the first's.
type tracker struct {
	*host.Fake
	issues []host.Issue
	opened []host.IssueRequest
	closed map[int]string
}

func newTracker(existing ...host.Issue) *tracker {
	tr := &tracker{Fake: &host.Fake{}, issues: existing, closed: map[int]string{}}
	tr.ListIssuesFn = func(host.IssueQuery) ([]host.Issue, error) { return tr.issues, nil }
	tr.OpenIssueFn = func(r host.IssueRequest) (string, error) {
		n := 100 + len(tr.opened)
		tr.opened = append(tr.opened, r)
		tr.issues = append(tr.issues, host.Issue{Number: n, Title: r.Title, State: "OPEN"})
		return "https://github.com/o/r/issues/" + itoa(n), nil
	}
	tr.CloseIssueFn = func(n int, c string) error {
		tr.closed[n] = c
		for i := range tr.issues {
			if tr.issues[i].Number == n {
				tr.issues[i].State = "CLOSED"
			}
		}
		return nil
	}
	return tr
}

func itoa(n int) string { return strconv.Itoa(n) }

func decidable() Report {
	r := build(nil)
	r.ABTotal.Decidable = true
	return r
}

func TestDeliver_OpensThisWeeksIssueAndClosesLastWeeksWithALinkingComment(t *testing.T) {
	tr := newTracker(host.Issue{Number: 7, Title: "Report 2026-W40", State: "OPEN", Labels: []string{"report"}}, host.Issue{Number: 8, Title: "Some other issue", State: "OPEN"})
	lines, err := dl(tr, build(nil), DeliverOptions{Repo: "aphrollo-tools", Labels: []string{"report"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.opened) != 1 || tr.opened[0].Title != "Report 2026-W41" || tr.opened[0].Body != build(nil).Text() {
		t.Fatalf("opened = %+v, want one issue titled Report 2026-W41 with the report as its body", tr.opened)
	}
	if got := tr.closed[7]; !strings.Contains(got, "https://github.com/o/r/issues/100") {
		t.Errorf("comment on the old report = %q, want a link to the new issue", got)
	}
	if _, ok := tr.closed[8]; ok {
		t.Error("an issue that is no report was closed")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "[ok]") {
		t.Errorf("lines = %v", lines)
	}
}

func TestDeliver_ASecondRunInTheSameWeekUpdatesNothing(t *testing.T) {
	tr := newTracker()
	opts := DeliverOptions{Repo: "r"}
	if _, err := dl(tr, build(nil), opts); err != nil {
		t.Fatal(err)
	}
	before := len(tr.Calls())
	lines, err := dl(tr, build(nil), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range tr.Calls()[before:] {
		if c == "OpenIssue" || c == "CloseIssue" || c == "EnsureLabel" {
			t.Errorf("second run called %s", c)
		}
	}
	if !strings.Contains(strings.Join(lines, "\n"), "[skip] Report 2026-W41") {
		t.Errorf("lines = %v, want a [skip] for the week", lines)
	}
}

func TestDeliver_DryOpensAndClosesNothing(t *testing.T) {
	tr := newTracker(host.Issue{Number: 7, Title: "Report 2026-W40", State: "OPEN"})
	lines, err := dl(tr, decidable(), DeliverOptions{Repo: "r", Dry: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.opened) != 0 || len(tr.closed) != 0 {
		t.Errorf("dry opened %d, closed %d", len(tr.opened), len(tr.closed))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "would open") {
		t.Errorf("lines = %v, want the preview to say what it would open", lines)
	}
}

func TestDeliver_TheABReadyIssueIsOpenedOnceEverWhenTheVerdictIsIn(t *testing.T) {
	tr := newTracker()
	opts := DeliverOptions{Repo: "aphrollo-tools"}
	if _, err := dl(tr, decidable(), opts); err != nil {
		t.Fatal(err)
	}
	// A later week: a new report, but the A/B issue exists.
	later := decidable()
	opts.Now = now.Add(7 * 24 * time.Hour)
	if _, err := dl(tr, later, opts); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, o := range tr.opened {
		if o.Title == "A/B ready: aphrollo-tools" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("A/B ready issues opened = %d, want 1 (once, ever)", n)
	}
}

func TestDeliver_NoABReadyIssueWhileTheMetricIsStillDeciding(t *testing.T) {
	tr := newTracker()
	if _, err := dl(tr, build(nil), DeliverOptions{Repo: "r"}); err != nil {
		t.Fatal(err)
	}
	for _, o := range tr.opened {
		if strings.HasPrefix(o.Title, "A/B ready") {
			t.Errorf("opened %q while deciding", o.Title)
		}
	}
}

func TestDeliver_AListFailureIsReturnedAndNothingIsOpened(t *testing.T) {
	tr := newTracker()
	tr.ListIssuesFn = func(host.IssueQuery) ([]host.Issue, error) { return nil, errors.New("no network") }
	if _, err := dl(tr, build(nil), DeliverOptions{Repo: "r"}); err == nil || len(tr.opened) != 0 {
		t.Errorf("err = %v, opened = %d; want the error and no issue (a blind open would duplicate)", err, len(tr.opened))
	}
}

// dl delivers a prebuilt report, dated now unless the options say.
func dl(t Tracker, r Report, o DeliverOptions) ([]string, error) {
	if o.Now.IsZero() {
		o.Now = now
	}
	return Deliver(t, func(bool) Report { return r }, o)
}

func withUsage() Report {
	r := build(nil)
	r.Usage = &Usage{Repo: "r", ByModel: []UsageGroup{
		{Key: "claude-opus-5-5", Turns: 2, Output: 5, CostUSD: 3}, {Key: "claude-opus-4-8", Turns: 1, Output: 1, CostUSD: 1},
		{Key: "claude-sonnet-5-5", Turns: 4}, {Key: "claude-haiku-4-5-20251001", Turns: 1}, {Key: "mystery-9", Turns: 1}}} // gitleaks:allow (model ids, not keys)
	return r
}

func TestPublished_ModelIdsBecomeTheirSizeAndNoIdReachesTheBody(t *testing.T) {
	text := withUsage().Published().Text()
	for _, bad := range []string{"claude", "opus", "sonnet", "haiku", "mystery"} {
		if strings.Contains(text, bad) {
			t.Errorf("the published text carries %q:\n%s", bad, text)
		}
	}
	for _, want := range []string{"large model", "medium model", "small model", "other model"} {
		if !strings.Contains(text, want) {
			t.Errorf("the published text lacks %q", want)
		}
	}
	if !strings.Contains(withUsage().Text(), "claude-opus-5-5") {
		t.Error("the local report lost the real model ids")
	}
	if g := withUsage().Published().Usage.ByModel[0]; g.Key != "large model" || g.Turns != 3 || g.CostUSD != 4 {
		t.Errorf("the two large models = %+v, want one row of 3 turns and $4", g)
	}
}

// refuseWord is an undercover check that refuses any text with word in it.
func refuseWord(word string) func(title, body string) string {
	return func(title, body string) string {
		if strings.Contains(title+body, word) {
			return "text carries " + word
		}
		return ""
	}
}

func TestDeliver_TheUndercoverCheckRunsOnTheTitleAndBodyBeforeAnythingIsOpened(t *testing.T) {
	tr := newTracker()
	if _, err := dl(tr, build(nil), DeliverOptions{Repo: "r", Refuse: refuseWord("Report")}); !errors.Is(err, ErrRefused) || len(tr.opened) != 0 {
		t.Errorf("a refused title: err %v, opened %d; want ErrRefused and nothing opened", err, len(tr.opened))
	}
	tr = newTracker()
	if _, err := dl(tr, build(nil), DeliverOptions{Repo: "r", Refuse: refuseWord("Friction")}); !errors.Is(err, ErrRefused) || len(tr.opened) != 0 {
		t.Errorf("a body refused even without its usage: err %v, opened %d; want ErrRefused", err, len(tr.opened))
	}
}

func TestDeliver_ARefusedUsageSectionIsWithheldAndSaidSoNotSilentlyDropped(t *testing.T) {
	tr := newTracker()
	r := withUsage()
	r.Usage.Sessions = 77
	lines, err := dl(tr, r, DeliverOptions{Repo: "r", Refuse: refuseWord("77 sessions")})
	if err != nil || len(tr.opened) != 1 {
		t.Fatalf("err %v, opened %d; want the report opened without its usage", err, len(tr.opened))
	}
	body := tr.opened[0].Body
	if strings.Contains(body, "77 sessions") || !strings.Contains(body, "withheld, because the undercover check refused it") {
		t.Errorf("body = %q, want the usage withheld with a line saying so", body)
	}
	_ = lines
}

func TestDeliver_OnlyReportIssuesThatAreOursAreClosed(t *testing.T) {
	tr := newTracker(
		host.Issue{Number: 1, Title: "Report 2026-W38", State: "OPEN", Labels: []string{"report"}},
		host.Issue{Number: 2, Title: "Report 2026-W39", State: "OPEN", Author: "me"},
		host.Issue{Number: 3, Title: "Report 2026-W40", State: "OPEN", Author: "stranger"})
	if _, err := dl(tr, build(nil), DeliverOptions{Repo: "r", Author: "me"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := tr.closed[1]; !ok {
		t.Error("the report-labelled issue was not closed")
	}
	if _, ok := tr.closed[2]; !ok {
		t.Error("the issue by the same account was not closed")
	}
	if _, ok := tr.closed[3]; ok {
		t.Error("a stranger's issue titled like a report was closed")
	}
}

func TestDeliver_TheReportIsNotBuiltWhenTheWeekAndTheABIssuesAlreadyExist(t *testing.T) {
	tr := newTracker(host.Issue{Number: 1, Title: "Report 2026-W41", State: "OPEN"}, host.Issue{Number: 2, Title: "A/B ready: r", State: "OPEN"})
	built := 0
	if _, err := Deliver(tr, func(bool) Report { built++; return build(nil) }, DeliverOptions{Repo: "r", Now: now}); err != nil || built != 0 {
		t.Errorf("err %v, built %d times; want no build when there is nothing to open", err, built)
	}
}

func TestPropose_TheABDecisionIsNotProposedOnceTheReadyIssueExists(t *testing.T) {
	r := Build(Input{Now: now, Window: week, Repo: "r"})
	r.ABTotal.Decidable = true
	has := func(rep Report) bool {
		for _, p := range propose(rep) {
			if p.Rule == "red-green" {
				return true
			}
		}
		return false
	}
	if !has(r) {
		t.Fatal("no red-green proposal for a decidable A/B")
	}
	r.abReadyIssued = true
	if has(r) {
		t.Error("red-green proposed again after the A/B ready issue was opened")
	}
}

// The A/B ready issue names the verdict it opened on, and at the maximum says the
// experiment is too small to measure.
func TestDeliver_TheABReadyIssueNamesTheVerdictItOpenedOn(t *testing.T) {
	for verdict, want := range map[string]string{
		measure.VerdictWarnBetter: "decided: warn better",
		measure.VerdictMaxReached: "too small to measure — decide on friction and cost",
	} {
		tr := newTracker()
		r := decidable()
		r.ABTotal.Verdict = verdict
		if _, err := dl(tr, r, DeliverOptions{Repo: "r"}); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, o := range tr.opened {
			if o.Title == "A/B ready: r" {
				found = true
				if !strings.Contains(o.Body, verdict) || !strings.Contains(o.Body, want) {
					t.Errorf("body for %q lacks %q:\n%s", verdict, want, o.Body)
				}
			}
		}
		if !found {
			t.Errorf("no A/B ready issue for the verdict %q", verdict)
		}
	}
}

// ratchet: test_removed TestDeliver_TheABReadyIssueIsOpenedOnceEverWhenBothArmsReachThirty: renamed TestDeliver_TheABReadyIssueIsOpenedOnceEverWhenTheVerdictIsIn: the issue opens on a verdict, not 30 lanes

// ratchet: test_removed TestDeliver_NoABReadyIssueWhileAnArmIsShort: renamed TestDeliver_NoABReadyIssueWhileTheMetricIsStillDeciding: the issue waits on a verdict, not 30 lanes
