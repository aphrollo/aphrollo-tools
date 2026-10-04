package github

import (
	"errors"
	"strings"
	"testing"
)

// How a PR is found and read over REST, on the argv gh is given and on the
// documented shape of the answers.

// pullsScript answers the list-pulls call with number and a pull read with
// body, and refuses anything else so an unexpected call fails the test.
func pullsScript(t *testing.T, number, body string) *scripted {
	t.Helper()
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		switch {
		case has(args, "repos/acme/widgets/pulls"):
			return []byte(number), nil
		case len(args) == 2 && strings.HasPrefix(args[1], "repos/acme/widgets/pulls/"):
			return []byte(body), nil
		}
		t.Errorf("unexpected gh call: %v", args)
		return nil, errors.New("unexpected gh call")
	}
	return s
}

func TestFindPR_NoneFoundIsNotFoundAndAPRIsItsNumber(t *testing.T) {
	n, ok, err := pullsScript(t, "", "").host(originURL).FindPR("lane/x")
	if err != nil || ok || n != 0 {
		t.Fatalf("FindPR = (%d, %v, %v), want (0, false, nil)", n, ok, err)
	}
	n, ok, err = pullsScript(t, "42", "").host(originURL).FindPR("lane/x")
	if err != nil || !ok || n != 42 {
		t.Fatalf("FindPR = (%d, %v, %v), want (42, true, nil)", n, ok, err)
	}
}

// gh api turns any -f field into a POST unless the method is set, and a POST
// to the pulls list is a create call that fails with HTTP 422 ("base" wasn't
// supplied): the lookup must say GET.
func TestFindPR_LooksUpWithGETNotACreatePOST(t *testing.T) {
	s := pullsScript(t, "7", "")
	if _, _, err := s.host(originURL).FindPR("lane/x"); err != nil {
		t.Fatal(err)
	}
	if !has(s.calls[0].args, "--method", "GET") {
		t.Errorf("argv = %v, want --method GET", s.calls[0].args)
	}
}

func TestFindPR_AFailedLookupIsAnErrorWithGHsWords(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func([]string) ([]byte, error) {
		return []byte("gh: authentication required"), errors.New("exit status 1")
	}
	_, _, err := s.host(originURL).FindPR("lane/x")
	if err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("err = %v, want gh's own words", err)
	}
}

func TestPR_ReadsTheFieldsIncludingAnUnknownMergeable(t *testing.T) {
	body := `{"number":42,"html_url":"https://github.com/acme/widgets/pull/42","state":"open","draft":false,"merged":false,"mergeable":null,"mergeable_state":"unknown","head":{"ref":"lane/x","sha":"deadbeef"}}`
	p, err := pullsScript(t, "", body).host(originURL).PR(42)
	if err != nil {
		t.Fatalf("PR: %v", err)
	}
	if p.Number != 42 || p.State != "OPEN" || p.Mergeable != "UNKNOWN" || p.MergeStateStatus != "UNKNOWN" {
		t.Fatalf("pr = %+v, want OPEN/UNKNOWN/UNKNOWN", p)
	}
	if p.HeadSHA != "deadbeef" || p.HeadRef != "lane/x" {
		t.Fatalf("head = %s on %s, want deadbeef on lane/x", p.HeadSHA, p.HeadRef)
	}
}

func TestPR_MergedBeatsClosedAndCleanIsMergeable(t *testing.T) {
	body := `{"number":7,"html_url":"https://github.com/acme/widgets/pull/7","state":"closed","draft":false,"merged":true,"merged_at":"2026-09-01T10:00:00Z","mergeable":true,"mergeable_state":"clean","head":{"ref":"lane/y","sha":"cafef00d"}}`
	p, err := pullsScript(t, "", body).host(originURL).PR(7)
	if err != nil {
		t.Fatalf("PR: %v", err)
	}
	if p.State != "MERGED" {
		t.Fatalf("state = %q, want MERGED (merged=true beats state=closed)", p.State)
	}
	if p.Mergeable != "MERGEABLE" || p.MergeStateStatus != "CLEAN" {
		t.Fatalf("mergeable/state = %q/%q, want MERGEABLE/CLEAN", p.Mergeable, p.MergeStateStatus)
	}
	if p.MergedAt != "2026-09-01T10:00:00Z" {
		t.Fatalf("MergedAt = %q, want the time the host gave", p.MergedAt)
	}
	conflicting := `{"number":8,"state":"open","mergeable":false,"mergeable_state":"dirty"}`
	c, err := pullsScript(t, "", conflicting).host(originURL).PR(8)
	if err != nil || c.Mergeable != "CONFLICTING" || c.MergeStateStatus != "DIRTY" {
		t.Fatalf("pr = %+v, %v; want CONFLICTING/DIRTY", c, err)
	}
}

func TestPRByBranch_AbsenceIsNilNilAndANumericRefFetchesDirectly(t *testing.T) {
	p, err := pullsScript(t, "", "").host(originURL).PRByBranch("lane/x")
	if err != nil || p != nil {
		t.Fatalf("PRByBranch = (%+v, %v), want (nil, nil)", p, err)
	}
	body := `{"number":9,"html_url":"https://github.com/acme/widgets/pull/9","state":"open","head":{"ref":"lane/z","sha":"abc"}}`
	s := pullsScript(t, "", body)
	p, err = s.host(originURL).PRByRef("9")
	if err != nil || p == nil || p.Number != 9 {
		t.Fatalf("PRByRef(9) = (%+v, %v), want number 9", p, err)
	}
	if len(s.calls) != 1 {
		t.Errorf("a numeric ref made %d calls, want 1 (no branch lookup)", len(s.calls))
	}
}

func TestMarkReady_AFailedLookupInTheSandboxFallbackIsPropagated(t *testing.T) {
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		if args[0] == "pr" && args[1] == "ready" {
			return []byte("HTTP 403: GitHub GraphQL is not available from Claude Code sessions; use the REST API"), errors.New("exit status 1")
		}
		return []byte("gh: authentication required"), errors.New("exit status 1")
	}
	err := s.host(originURL).MarkReady("feat/x")
	if err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("MarkReady = %v, want the find error propagated", err)
	}
}
