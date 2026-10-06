package report

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/measure"
)

// webFixture is a hand-built report model, so the page's bytes depend on the
// renderer alone.
func webFixture() Report {
	refs := Refs{Seqs: []int64{12, 14}, More: 3}
	return Report{
		Title: "Report 2026-W41", Repo: "aphrollo-tools", Window: "last 7d", Until: "2026-10-07T12:00:00Z", Events: 1234,
		Friction: []Friction{
			{Rule: "ratchet:module_size", Denies: 4, Overrides: 1, SecsLost: 130, Refs: refs},
			{Rule: "gate:lint-blocked", Refusals: 3, SecsLost: 40, SecsBackground: 15, Refs: Refs{Seqs: []int64{30}}},
			{Rule: "run:deferred", NotTested: 9, Refs: Refs{Seqs: []int64{32}}},
		},
		WrongBlocks: []WrongBlock{{Rule: "disabled-test", Denies: 5, Waived: 3, Rate: "60%", Refs: refs}},
		ShadowWrong: []ShadowWrong{{Rule: "red-green", Fires: 40, Stricter: 8, Wrong: 2, Catches: 1, Open: 5}},
		Standdowns:  []Standdown{{Matcher: "unknown-matcher-kind:tautology", N: 25, Refs: refs}},
		Escapes:     Escapes{Rows: []EscapeRow{{Class: "product", Caught: "local test gate (CI job test caught it)", N: 2, Refs: refs}}, FalsePositives: 1},
		AB: measure.AB{Arms: []measure.ABArm{
			{Arm: "enforce", Lanes: 4, Denies: 3, HeldOut: 1, Dropped: 2},
			{Arm: "warn", Lanes: 6, Warnings: 5},
		}},
		ABTotal: measure.AB{Arms: []measure.ABArm{{Arm: "enforce", Lanes: 12}, {Arm: "warn", Lanes: 31, Reached: true}}},
		Shadow: measure.Shadow{Fires: 40, Dropped: 2, Languages: []measure.ShadowLang{
			{Lang: "go", Fires: 30, Agree: 24, HeldOut: 1, Dropped: 2}, {Lang: "python", Fires: 4},
		}},
		Tokens: Tokens{
			Briefs:  []measure.BriefLine{{Name: "managed CLAUDE.md block", Bytes: 1463, Tokens: 366, Cap: 400}, {Name: "tdd skill", Bytes: 1700, Tokens: 425, Cap: 400, Over: true}},
			Biggest: []GateLine{{Name: "commit_gate:precommit:mutants-passed", N: 96, Tokens: 900, Refs: refs}},
		},
		Proposals: []Proposal{{Rule: "disabled-test", Numbers: "5 denies, 3 waived", Change: "lower the rule from block to guide", Refs: refs}},
		Usage: &Usage{
			Repo: "aphrollo-tools", Sessions: 3,
			Total:  UsageGroup{Key: "total", Turns: 40, Fresh: 100, CacheWrite: 2000, CacheRead: 90000, Output: 5000, CostUSD: 12.5},
			ByDay:  []UsageGroup{{Key: "2026-10-05", Fresh: 10, CacheRead: 40000, Output: 2000, CostUSD: 5}, {Key: "2026-10-06", Fresh: 20, CacheRead: 50000, Output: 3000, CostUSD: 7.5}},
			ByLane: []UsageGroup{{Key: "lane/a", Output: 4000, CostUSD: 10}, {Key: "main", Output: 1000, CostUSD: 2.5}},
			ByRole: []UsageGroup{{Key: "coordinator", Output: 3000, CostUSD: 8}, {Key: "subagent", Output: 2000, CostUSD: 4.5}},
			ByModel: []UsageGroup{
				{Key: "claude-opus-5", Output: 5000, CostUSD: 12.5},
			},
			TopSessions: []UsageGroup{{Key: "s-1", Turns: 30, Output: 4000, CostUSD: 9}},
			Injection: Injection{Tokens: 700, InputShare: "0.760%", ByEvent: []UsageGroup{{Key: "PostToolUse", Tokens: 500, Count: 20}},
				ByKind: []UsageGroup{{Key: "green", Tokens: 300}, {Key: "not-tested", Tokens: 200}}},
			Compare: &UsageCompare{At: "2026-10-05",
				Before: UsagePeriod{Days: 5, Group: UsageGroup{Output: 1000, CostUSD: 5}, Injected: 100, InputShare: "1.0%"},
				After:  UsagePeriod{Days: 3, Group: UsageGroup{Output: 4000, CostUSD: 7.5}, Injected: 600, InputShare: "0.5%"}},
		},
	}
}

func render(t *testing.T, r Report) string {
	t.Helper()
	out, err := RenderHTML(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestRenderHTML_MatchesTheGoldenPageByteForByte(t *testing.T) {
	got := render(t, webFixture())
	path := filepath.Join("testdata", "web_golden.html")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden page (run with UPDATE_GOLDEN=1 once): %v", err)
	}
	if got != string(want) {
		t.Errorf("the page differs from testdata/web_golden.html (%d bytes, golden %d); UPDATE_GOLDEN=1 rewrites it after a reviewed change", len(got), len(want))
	}
}

func TestRenderHTML_SameModelSameBytes(t *testing.T) {
	first := render(t, webFixture())
	if second := render(t, webFixture()); first != second {
		t.Error("two renders of one model differ")
	}
}

var externalRef = regexp.MustCompile(`(?i)(src|href|action|data)\s*=\s*["']?\s*(https?:)?//|url\(\s*["']?(https?:)?//|@import|<script|<link|<iframe`)

func TestRenderHTML_FetchesNothingAndRunsNothing(t *testing.T) {
	page := render(t, webFixture())
	if m := externalRef.FindString(page); m != "" {
		t.Errorf("the page references an outside resource or runs script: %q", m)
	}
	for _, bad := range []string{"http://", "https://"} {
		if strings.Contains(page, bad) {
			t.Errorf("the page carries %q", bad)
		}
	}
}

func TestRenderHTML_ARuleNameIsEscapedEverywhereItAppears(t *testing.T) {
	r := webFixture()
	evil := `<script>alert("x")</script>`
	r.Friction[0].Rule = evil
	r.WrongBlocks[0].Rule = evil
	r.Proposals[0].Rule = evil
	page := render(t, r)
	if strings.Contains(page, "<script") {
		t.Errorf("a rule name reached the page unescaped")
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Error("the escaped rule name is not on the page")
	}
}

func TestRenderHTML_EvidenceIsCopyableWhyCommandText(t *testing.T) {
	page := render(t, webFixture())
	for _, want := range []string{"aphrollo why 12", "+3 more", `name="viewport"`, "prefers-color-scheme: dark", "<svg"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Index(page, "Proposals") > strings.Index(page, "Friction per rule") {
		t.Error("proposals are not at the top")
	}
}

func TestRenderHTML_AReportWithNothingInItStillRenders(t *testing.T) {
	page := render(t, Report{Title: "Report 2026-W41", Repo: "r"})
	if !strings.Contains(page, "Report 2026-W41") || !strings.Contains(page, "none") {
		t.Errorf("an empty report page = %q", page)
	}
}
