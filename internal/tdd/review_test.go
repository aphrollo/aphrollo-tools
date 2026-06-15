package tdd

import (
	"errors"
	"strings"
	"testing"
)

func TestParseFindings(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantLen int
		wantErr bool
	}{
		{"clean array", `[{"severity":"high","file":"a.go","line":3,"issue":"npe"}]`, 1, false},
		{"empty array", `[]`, 0, false},
		{"prose preamble", "Sure, here are the findings:\n[{\"severity\":\"low\",\"file\":\"x\",\"line\":1,\"issue\":\"y\"}]", 1, false},
		{"code fence", "```json\n[{\"severity\":\"critical\",\"file\":\"x\",\"line\":1,\"issue\":\"y\"}]\n```", 1, false},
		{"no array", "I could not review this.", 0, true},
		{"malformed json", "[{severity: high}]", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseFindings(c.raw)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if !c.wantErr && len(got) != c.wantLen {
				t.Fatalf("len = %d, want %d", len(got), c.wantLen)
			}
		})
	}
}

func TestBlockingFindings(t *testing.T) {
	in := []Finding{
		{Severity: "critical"}, {Severity: "High"}, {Severity: "medium"}, {Severity: "low"}, {Severity: ""},
	}
	if got := blockingFindings(in); len(got) != 2 {
		t.Fatalf("blocking = %d, want 2 (critical+high only)", len(got))
	}
}

func TestIsTrivialDiff(t *testing.T) {
	codeDiff := "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@\n+code"
	docDiff := "diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@\n+docs"
	if isTrivialDiff(codeDiff) {
		t.Fatal("a source diff is not trivial")
	}
	if !isTrivialDiff(docDiff) {
		t.Fatal("a docs-only diff is trivial")
	}
}

func TestBuildReviewPrompt(t *testing.T) {
	p := buildReviewPrompt("DIFFBODY")
	if !strings.Contains(p, "DIFFBODY") {
		t.Fatal("prompt must include the diff")
	}
	if !strings.Contains(p, "JSON array") {
		t.Fatal("prompt must state the JSON output contract")
	}
}

func TestPrepush_RealGit(t *testing.T) {
	root := makeGoRepo(t)
	// Diverge from the base so there is a push diff to review.
	gitDo(t, root, "branch", "--move", "main")
	gitDo(t, root, "checkout", "-q", "-b", "feat")
	write(t, root, "widget.go", "package m\n\nfunc Widget() int { return 1 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "feat")

	// The base resolves to main (local). A reviewer that reports a high finding
	// blocks; an empty review allows; a reviewer error fails open.
	highReviewer := func(string) (string, error) {
		return `[{"severity":"high","file":"widget.go","line":3,"issue":"bug"}]`, nil
	}
	if res := prepushFrom(root, "main", highReviewer); !res.Blocked {
		t.Fatalf("a high finding should block, got %+v", res)
	}

	cleanReviewer := func(string) (string, error) { return "[]", nil }
	if res := prepushFrom(root, "main", cleanReviewer); res.Blocked {
		t.Fatalf("a clean review should allow, got %+v", res)
	}

	errReviewer := func(string) (string, error) { return "", errors.New("offline") }
	if res := prepushFrom(root, "main", errReviewer); res.Blocked {
		t.Fatalf("a reviewer error must fail open, got %+v", res)
	}
}

func TestPrepush_NoBase_FailsOpenWithNote(t *testing.T) {
	root := makeGoRepo(t) // committed, but no upstream and no origin/* refs
	called := false
	res := Prepush(root, func(string) (string, error) { called = true; return "[]", nil })
	if res.Blocked {
		t.Fatalf("unresolved base must not block, got %+v", res)
	}
	if res.Message == "" {
		t.Fatal("unresolved base must surface a note, not skip silently")
	}
	if called {
		t.Fatal("reviewer should not run when no base resolves")
	}
}

// prepushFrom runs the prepush logic against an explicit base, isolating the
// review policy from upstream-resolution (which has no remote in tests).
func prepushFrom(repoRoot, base string, review Reviewer) GateResult {
	diff, err := git(repoRoot, "diff", base+"...HEAD")
	if err != nil || isTrivialDiff(diff) {
		return GateResult{}
	}
	raw, err := review(buildReviewPrompt(diff))
	if err != nil {
		return GateResult{Message: "review unavailable"}
	}
	findings, err := parseFindings(raw)
	if err != nil {
		return GateResult{Message: "unparseable"}
	}
	if b := blockingFindings(findings); len(b) > 0 {
		return GateResult{Blocked: true, Message: renderFindings(b)}
	}
	return GateResult{}
}
