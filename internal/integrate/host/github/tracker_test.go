package github

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// The reads the gate's escape, session-line and mutation code make: each is
// pinned on the argv gh is given and on what the adapter makes of the answer,
// including the noise a stub or a real gh prints around a payload.

func TestListIssues_AsksOneLabelAndOnlyTheFieldsWanted(t *testing.T) {
	s := &scripted{t: t, reply: func([]string) ([]byte, error) {
		return []byte("notice\n[{\"number\":4,\"title\":\"a\",\"body\":\"b\",\"state\":\"CLOSED\",\"closedAt\":\"2026-09-02T10:00:00Z\",\"labels\":[{\"name\":\"escape\"},{\"name\":\"x\"}]}]\n"), nil
	}}
	got, err := s.host(originURL).ListIssues(host.IssueQuery{Label: "escape", State: "all", Limit: 200, Fields: []string{"number", "state", "closedAt", "labels"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"issue", "list", "--label", "escape", "--state", "all", "--limit", "200", "--json", "number,state,closedAt,labels"}
	if !slices.Equal(s.calls[0].args, want) {
		t.Fatalf("argv = %v, want %v", s.calls[0].args, want)
	}
	if len(got) != 1 || got[0].Number != 4 || got[0].State != "CLOSED" || got[0].Body != "b" || !slices.Equal(got[0].Labels, []string{"escape", "x"}) {
		t.Fatalf("issues = %+v", got)
	}
	if !got[0].ClosedAt.Equal(time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("ClosedAt = %v", got[0].ClosedAt)
	}
}

func TestListIssues_WithoutALabelAsksNoLabel(t *testing.T) {
	s := &scripted{t: t, reply: func([]string) ([]byte, error) { return []byte("[]"), nil }}
	if _, err := s.host(originURL).ListIssues(host.IssueQuery{State: "open", Limit: 1000, Fields: []string{"labels"}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"issue", "list", "--state", "open", "--limit", "1000", "--json", "labels"}
	if !slices.Equal(s.calls[0].args, want) {
		t.Fatalf("argv = %v, want %v", s.calls[0].args, want)
	}
}

func TestListIssues_AnUnreadableAnswerIsAnErrorNotAnEmptyList(t *testing.T) {
	s := &scripted{t: t, reply: func([]string) ([]byte, error) { return []byte("not json"), nil }}
	if got, err := s.host(originURL).ListIssues(host.IssueQuery{State: "open", Limit: 1, Fields: []string{"number"}}); err == nil {
		t.Fatalf("want an error, got %v", got)
	}
}

func TestListIssues_AFailureCarriesGhsOwnWordsCapped(t *testing.T) {
	s := &scripted{t: t, reply: func([]string) ([]byte, error) {
		return []byte(strings.Repeat("x", 900)), errors.New("exit status 1")
	}}
	_, err := s.host(originURL).ListIssues(host.IssueQuery{State: "open", Limit: 1, Fields: []string{"number"}})
	if err == nil || !strings.HasPrefix(err.Error(), "gh issue: exit status 1: ") || len([]rune(err.Error())) > 460 {
		t.Fatalf("err = %v", err)
	}
}

func TestIssue_ReadsLabelsAndBody(t *testing.T) {
	s := &scripted{t: t, reply: func([]string) ([]byte, error) {
		return []byte(`{"labels":[{"name":"escape"}],"body":"closes-by: a/b.go"}`), nil
	}}
	got, err := s.host(originURL).Issue("12")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"issue", "view", "12", "--json", "labels,body"}; !slices.Equal(s.calls[0].args, want) {
		t.Fatalf("argv = %v, want %v", s.calls[0].args, want)
	}
	if got.Body != "closes-by: a/b.go" || !slices.Equal(got.Labels, []string{"escape"}) {
		t.Fatalf("issue = %+v", got)
	}
}

func TestPRClosure_ReadsBodyCommitsAndBothEnds(t *testing.T) {
	s := &scripted{t: t, reply: func([]string) ([]byte, error) {
		return []byte(`{"body":"Closes #1","commits":[{"messageHeadline":"h","messageBody":"Closes #2"}],"baseRefOid":"b1","headRefOid":"h1"}`), nil
	}}
	got, err := s.host(originURL).PRClosure("7")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"pr", "view", "7", "--json", "body,commits,baseRefOid,headRefOid"}; !slices.Equal(s.calls[0].args, want) {
		t.Fatalf("argv = %v, want %v", s.calls[0].args, want)
	}
	if got.Body != "Closes #1" || got.Base != "b1" || got.Head != "h1" || len(got.Commits) != 1 || got.Commits[0].Body != "Closes #2" || got.Commits[0].Headline != "h" {
		t.Fatalf("facts = %+v", got)
	}
}

func TestPRBody_AsksOnlyForTheBody(t *testing.T) {
	s := &scripted{t: t, reply: func([]string) ([]byte, error) { return []byte(`{"body":"text"}`), nil }}
	got, err := s.host(originURL).PRBody("7")
	if err != nil || got != "text" {
		t.Fatalf("got %q, %v", got, err)
	}
	if want := []string{"pr", "view", "7", "--json", "body"}; !slices.Equal(s.calls[0].args, want) {
		t.Fatalf("argv = %v, want %v", s.calls[0].args, want)
	}
}

func TestPRDiff_AFailureKeepsGhsWordsSoATooLargeDiffIsRecognisable(t *testing.T) {
	s := &scripted{t: t, reply: func([]string) ([]byte, error) {
		return []byte("HTTP 406: diff too_large"), errors.New("exit status 1")
	}}
	_, err := s.host(originURL).PRDiff("7")
	if err == nil || !strings.Contains(err.Error(), "too_large") {
		t.Fatalf("err = %v, want gh's words", err)
	}
	if want := []string{"pr", "diff", "7"}; !slices.Equal(s.calls[0].args, want) {
		t.Fatalf("argv = %v, want %v", s.calls[0].args, want)
	}
}

func TestCheckState_ReadsTheNewestRunNamedSoOnTheCommit(t *testing.T) {
	s := &scripted{t: t, reply: func([]string) ([]byte, error) { return []byte("completed/success\n"), nil }}
	status, conclusion, found, err := s.host(originURL).CheckState("abc123", "mutants-verdict")
	if err != nil || !found || status != "completed" || conclusion != "success" {
		t.Fatalf("got %q %q %v %v", status, conclusion, found, err)
	}
	a := s.calls[0].args
	if a[0] != "api" || a[1] != "repos/{owner}/{repo}/commits/abc123/check-runs?per_page=100&filter=latest" || a[2] != "--jq" || !strings.Contains(a[3], `select(.name=="mutants-verdict")`) {
		t.Fatalf("argv = %v", a)
	}
}

func TestCheckState_NoSuchCheckIsNotFoundAndAnInProgressOneHasNoConclusion(t *testing.T) {
	answer := ""
	s := &scripted{t: t, reply: func([]string) ([]byte, error) { return []byte(answer), nil }}
	h := s.host(originURL)
	if _, _, found, err := h.CheckState("abc", "x"); err != nil || found {
		t.Fatalf("empty answer: found=%v err=%v", found, err)
	}
	answer = "in_progress/\n"
	status, conclusion, found, err := h.CheckState("abc", "x")
	if err != nil || !found || status != "in_progress" || conclusion != "" {
		t.Fatalf("got %q %q %v %v", status, conclusion, found, err)
	}
}

func TestArtifactRun_NamesTheRunThatPublishedTheNewestArtifact(t *testing.T) {
	answer := "9001\n"
	s := &scripted{t: t, reply: func([]string) ([]byte, error) { return []byte(answer), nil }}
	h := s.host(originURL)
	run, found, err := h.ArtifactRun("mutants-verdict-tree1")
	if err != nil || !found || run != 9001 {
		t.Fatalf("got %d %v %v", run, found, err)
	}
	a := s.calls[0].args
	if a[0] != "api" || a[1] != "repos/{owner}/{repo}/actions/artifacts?per_page=1&name=mutants-verdict-tree1" || a[2] != "--jq" {
		t.Fatalf("argv = %v", a)
	}
	answer = "\n"
	if _, found, err := h.ArtifactRun("n"); err != nil || found {
		t.Fatalf("empty answer: found=%v err=%v", found, err)
	}
	answer = "oops"
	if _, _, err := h.ArtifactRun("n"); err == nil {
		t.Fatal("a run id that is not a number must be an error")
	}
}

func TestDownloadArtifact_NamesTheRunTheArtifactAndWhereItGoes(t *testing.T) {
	s := &scripted{t: t, reply: func([]string) ([]byte, error) { return nil, nil }}
	if err := s.host(originURL).DownloadArtifact(9001, "art", "/tmp/d"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"run", "download", "9001", "-n", "art", "-D", "/tmp/d"}; !slices.Equal(s.calls[0].args, want) {
		t.Fatalf("argv = %v, want %v", s.calls[0].args, want)
	}
}

func TestOptions_NegativeTimeoutIsNoBound(t *testing.T) {
	s := &scripted{t: t, reply: func([]string) ([]byte, error) { return []byte("{}"), nil }}
	h := New(Options{Dir: "/lane", Runner: s.run, Timeout: -1})
	if _, err := h.PRBody("1"); err != nil {
		t.Fatal(err)
	}
	if s.calls[0].timeout != 0 {
		t.Fatalf("timeout = %v, want none (0)", s.calls[0].timeout)
	}
}

func TestOptions_ADeadlineStillBoundsAnUnboundedHost(t *testing.T) {
	s := &scripted{t: t, reply: func([]string) ([]byte, error) { return []byte("{}"), nil }}
	h := New(Options{Dir: "/lane", Runner: s.run, Timeout: -1, Deadline: time.Now().Add(time.Hour)})
	if _, err := h.PRBody("1"); err != nil {
		t.Fatal(err)
	}
	if s.calls[0].timeout <= 0 || s.calls[0].timeout > time.Hour {
		t.Fatalf("timeout = %v, want what is left of the deadline", s.calls[0].timeout)
	}
}

func TestListIssues_NoLimitLeavesGhsOwnPageAndAnEmptyStderrLeavesABareError(t *testing.T) {
	s := &scripted{t: t, reply: func([]string) ([]byte, error) { return nil, errors.New("exit status 1") }}
	_, err := s.host(originURL).ListIssues(host.IssueQuery{Label: "x", State: "open", Fields: []string{"title"}})
	want := []string{"issue", "list", "--label", "x", "--state", "open", "--json", "title"}
	if !slices.Equal(s.calls[0].args, want) {
		t.Fatalf("argv = %v, want %v", s.calls[0].args, want)
	}
	if err == nil || err.Error() != "gh issue: exit status 1" {
		t.Fatalf("err = %v, want the verb and the cause alone", err)
	}
}
