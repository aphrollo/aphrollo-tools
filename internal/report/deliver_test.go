package report

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
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
	tr := newTracker(host.Issue{Number: 7, Title: "Report 2026-W40", State: "OPEN"}, host.Issue{Number: 8, Title: "Some other issue", State: "OPEN"})
	lines, err := Deliver(tr, build(nil), DeliverOptions{Repo: "aphrollo-tools", Labels: []string{"report"}})
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
	if _, err := Deliver(tr, build(nil), opts); err != nil {
		t.Fatal(err)
	}
	before := len(tr.Calls())
	lines, err := Deliver(tr, build(nil), opts)
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
	lines, err := Deliver(tr, decidable(), DeliverOptions{Repo: "r", Dry: true})
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

func TestDeliver_TheABReadyIssueIsOpenedOnceEverWhenBothArmsReachThirty(t *testing.T) {
	tr := newTracker()
	opts := DeliverOptions{Repo: "aphrollo-tools"}
	if _, err := Deliver(tr, decidable(), opts); err != nil {
		t.Fatal(err)
	}
	// A later week: a new report, but the A/B issue exists.
	later := decidable()
	later.Title = "Report 2026-W42"
	if _, err := Deliver(tr, later, opts); err != nil {
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

func TestDeliver_NoABReadyIssueWhileAnArmIsShort(t *testing.T) {
	tr := newTracker()
	if _, err := Deliver(tr, build(nil), DeliverOptions{Repo: "r"}); err != nil {
		t.Fatal(err)
	}
	for _, o := range tr.opened {
		if strings.HasPrefix(o.Title, "A/B ready") {
			t.Errorf("opened %q with no arm at 30 lanes", o.Title)
		}
	}
}

func TestDeliver_AListFailureIsReturnedAndNothingIsOpened(t *testing.T) {
	tr := newTracker()
	tr.ListIssuesFn = func(host.IssueQuery) ([]host.Issue, error) { return nil, errors.New("no network") }
	if _, err := Deliver(tr, build(nil), DeliverOptions{Repo: "r"}); err == nil || len(tr.opened) != 0 {
		t.Errorf("err = %v, opened = %d; want the error and no issue (a blind open would duplicate)", err, len(tr.opened))
	}
}
