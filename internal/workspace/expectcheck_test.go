package workspace

import (
	"bytes"
	"strings"
	"testing"
)

func TestPR_RefusesAnExpectLineNamingNoMetric(t *testing.T) {
	repo := repoWithRemote(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil },
		func(wt string, req PRCreate) (*PRInfo, error) {
			t.Fatal("the PR must not be created with an unreadable expect line")
			return nil, nil
		},
	)
	pr, err := PRPlan(targetFor(repo, "main"), "", "title", "version: none\nexpect: coffee p50 down", false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	err = pr.Apply(&out, &errb)
	if err == nil {
		t.Fatal("expected the PR to be refused")
	}
	for _, must := range []string{"expect: coffee p50 down", "merge-queue", "wrong-blocks"} {
		if !strings.Contains(err.Error(), must) {
			t.Errorf("refusal %q does not name %q", err, must)
		}
	}
}

func TestClosureChecksBeforePR_HoldsASubmitSummaryToTheExpectSyntaxToo(t *testing.T) {
	repo := repoWithRemote(t)
	var out bytes.Buffer
	_, _, err := closureChecksBeforePR(repo, "main", "lane/x", "t", "expect: merge-queue p50", &out)
	if err == nil || !strings.Contains(err.Error(), "merge-queue") {
		t.Fatalf("err = %v, want a refusal naming the valid metrics", err)
	}
}
