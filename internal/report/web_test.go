package report

import (
	"fmt"
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
		Speed:     webSpeed(),
		Proposals: []Proposal{{Rule: "disabled-test", Numbers: "5 denies, 3 waived", Change: "lower the rule from block to guide", Refs: refs}},
		Usage: &Usage{
			Repo: "aphrollo-tools", Sessions: 3,
			Total:  UsageGroup{Key: "total", Turns: 40, Fresh: 100, CacheWrite: 2000, CacheRead: 90000, Output: 5000, CostUSD: 12.5},
			ByDay:  []UsageGroup{{Key: "2026-10-05", Fresh: 10, CacheRead: 40000, Output: 2000, CostUSD: 5}, {Key: "2026-10-06", Fresh: 20, CacheRead: 50000, Output: 3000, CostUSD: 7.5}},
			ByLane: []UsageGroup{{Key: "lane/a", Output: 4000, CostUSD: 10}, {Key: "coordination", Output: 700, CostUSD: 1.5}, {Key: "unattributed", Output: 300, CostUSD: 1}},
			ByRole: []UsageGroup{{Key: "coordinator", Output: 3000, CostUSD: 8}, {Key: "subagent", Output: 2000, CostUSD: 4.5}},
			ByModel: []UsageGroup{
				{Key: "claude-opus-5", Output: 5000, CostUSD: 12.5},
			},
			TopSessions: []UsageGroup{{Key: "s-1", Turns: 30, Output: 4000, CostUSD: 9}},
			Injection: Injection{Tokens: 700, FreshShare: "0.760%", ByEvent: []UsageGroup{{Key: "PostToolUse", Tokens: 500, Count: 20}},
				ByKind: []UsageGroup{{Key: "green", Tokens: 300}, {Key: "not-tested", Tokens: 200}}},
			Compare: &UsageCompare{At: "2026-10-05",
				Before: UsagePeriod{Days: 5, Group: UsageGroup{Output: 1000, CostUSD: 5}, Injected: 100, FreshShare: "1.0%"},
				After:  UsagePeriod{Days: 3, Group: UsageGroup{Output: 4000, CostUSD: 7.5}, Injected: 600, FreshShare: "0.5%"}},
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
	for _, want := range []string{"aphrollo why 12", "+4 more", `name="viewport"`, "prefers-color-scheme: dark", "<svg"} {
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

func TestText_MatchesTheGoldenTextByteForByte(t *testing.T) {
	got := webFixture().Text()
	path := filepath.Join("testdata", "text_golden.txt")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden text (run with UPDATE_GOLDEN=1 once): %v", err)
	}
	if got != string(want) {
		t.Errorf("the text differs from testdata/text_golden.txt; UPDATE_GOLDEN=1 rewrites it after a reviewed change:\n%s", got)
	}
}

func TestRenderHTML_ProposalsSharingAChangeAreOneGroupHeadedByIt(t *testing.T) {
	r := webFixture()
	cheaper := "make the stage cheaper or move the check earlier, to the edit"
	r.Proposals = []Proposal{
		{Rule: "gate:still-red", Numbers: "2626s lost", Change: cheaper},
		{Rule: "gate:timeout-rejected", Numbers: "3000s lost", Change: cheaper},
		{Rule: "disabled-test", Numbers: "5 denies, 3 waived", Change: "lower the rule from block to guide"},
	}
	page := render(t, r)
	if n := strings.Count(page, cheaper); n != 1 {
		t.Errorf("the shared change is printed %d times, want once as its group's heading", n)
	}
	if strings.Index(page, cheaper) > strings.Index(page, "lower the rule from block to guide") {
		t.Error("the group of two rules is not before the group of one")
	}
	for _, rule := range []string{"gate:still-red", "gate:timeout-rejected", "disabled-test"} {
		if !strings.Contains(page, rule) {
			t.Errorf("rule %q is missing from its group", rule)
		}
	}
}

func TestRenderHTML_BarChartTextIsPageTextNotScaledSVG(t *testing.T) {
	page := render(t, webFixture())
	if strings.Contains(page, `class="lbl"`) && strings.Contains(page, "<text") {
		t.Error("a bar chart draws its labels as SVG text, which shrinks to about 6px on a phone")
	}
	if !strings.Contains(page, `<span class="bar" style="width:100.0%">`) {
		t.Error("the longest bar is not drawn as a full-width page element")
	}
}

func TestRenderHTML_TheLaneChartScalesOnLanesAndTheSplitShowsWhatNoLaneHolds(t *testing.T) {
	page := render(t, webFixture())
	lanes := between(page, `aria-label="cost per lane"`, "</figure>")
	if strings.Contains(lanes, "coordination") || strings.Contains(lanes, "unattributed") {
		t.Errorf("the lane chart carries coordination or unattributed, which dwarf the lanes:\n%s", lanes)
	}
	split := between(page, `class="split"`, "</figure>")
	for _, want := range []string{"lanes 80%", "coordination 12%", "unattributed 8%"} {
		if !strings.Contains(split, want) {
			t.Errorf("the attribution split lacks %q:\n%s", want, split)
		}
	}
}

func TestRenderHTML_LongTablesFoldTheirTailAndDropNoRow(t *testing.T) {
	r := webFixture()
	for i := range 20 {
		r.Usage.ByLane = append(r.Usage.ByLane, UsageGroup{Key: fmt.Sprintf("lane/extra-%02d", i), CostUSD: 0.5})
		r.Friction = append(r.Friction, Friction{Rule: fmt.Sprintf("rule-%02d", i), Denies: 1})
	}
	page := render(t, r)
	for i := range 20 {
		for _, want := range []string{fmt.Sprintf("lane/extra-%02d", i), fmt.Sprintf("rule-%02d", i)} {
			if !strings.Contains(page, want) {
				t.Errorf("row %q was dropped", want)
			}
		}
	}
	for _, want := range []string{"<summary>8 more lanes</summary>", "<summary>8 more rules</summary>"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks the fold %q", want)
		}
	}
	if strings.Contains(page, "more rows in the JSON") {
		t.Error("the page still sends the reader to the JSON for rows it could fold")
	}
}

func TestRenderHTML_ABProgressIsDrawnAgainstTheThirtyLanesNotTheBiggerArm(t *testing.T) {
	page := render(t, webFixture())
	ab := between(page, `aria-label="lanes per A/B arm`, "</figure>")
	for _, want := range []string{`style="width:40.0%"`, "12 of 30", `style="width:100.0%"`, "31 of 30"} {
		if !strings.Contains(ab, want) {
			t.Errorf("the A/B chart lacks %q:\n%s", want, ab)
		}
	}
}

func TestRenderHTML_BigNumbersCarryThousandsSeparators(t *testing.T) {
	r := webFixture()
	r.Events = 52199
	r.Usage.Total.CostUSD = 1099.32
	page := render(t, r)
	for _, want := range []string{"52,199 events", "$1,099.32"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
}

func TestRenderHTML_TheChangeSinceTheWeekBeforeIsShown(t *testing.T) {
	r := webFixture()
	r.Friction[0].Prev = 2
	r.Previous = &Previous{Window: "last 7d", NotTested: 4, Denies: 9}
	page := render(t, r)
	if !strings.Contains(between(page, `id="friction"`, "</section>"), `title="2 the window before">+3</td>`) {
		t.Error("a rule's change against the window before is not in its row")
	}
	summary := between(page, `class="facts"`, "</dl>")
	for _, want := range []string{"<dt>Not tested</dt><dd>9 <span class=\"chg up\">(&#43;5)</span>", "<dt>Denies</dt><dd>4 <span class=\"chg down\">(−5)</span>"} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary lacks %q:\n%s", want, summary)
		}
	}
}

func TestRenderHTML_EvidenceShowsOneCommandAndFoldsTheOtherSeqs(t *testing.T) {
	page := render(t, webFixture())
	want := `<code>aphrollo why 12</code><details><summary>+4 more</summary>14 (+3 in the JSON)</details>`
	if !strings.Contains(page, want) {
		t.Errorf("the evidence is not one command and a fold; want %q", want)
	}
}

// between is the text of page from the first from up to the next to.
func between(page, from, to string) string {
	i := strings.Index(page, from)
	if i < 0 {
		return ""
	}
	rest := page[i:]
	if j := strings.Index(rest, to); j >= 0 {
		return rest[:j]
	}
	return rest
}

func TestRenderHTML_AnInjectedTextIsDrawnAgainstItsOwnCap(t *testing.T) {
	briefs := between(render(t, webFixture()), `aria-label="injected text tokens against its cap"`, "</figure>")
	for _, want := range []string{`style="width:91.5%"></span></span><span class="val">366 / 400`, `style="width:100.0%"></span></span><span class="val">425 / 400`} {
		if !strings.Contains(briefs, want) {
			t.Errorf("the brief chart lacks %q: a bar is its text's share of its own cap, not of the biggest text:\n%s", want, briefs)
		}
	}
}

func webSpeed() Speed {
	slower, faster := 5.0, -6.0
	return Speed{
		Rows: []SpeedRow{
			{Stage: "edit suite", N: 120, P50: 4, P90: 18, Max: 90, PrevN: 100, PrevP50: 10, Change: &faster, Clear: true, P: 0.01, Versions: []SpeedVersion{
				{Label: "since v1.0.0, 2026-10-01", N: 60, P50: 10, P90: 20, Max: 90},
				{Label: "since v1.1.0, 2026-10-05", N: 60, P50: 4, P90: 18, Max: 80, PrevP50: 10, Change: &faster, Clear: true, P: 0.01},
			}},
			{Stage: "commit gate: go test ./...", N: 14, P50: 95, P90: 210, Max: 240, PrevN: 9, PrevP50: 90, Change: &slower, Clear: true, P: 0.03},
			{Stage: "PR lead time", N: 3, P50: 5400, P90: 7200, Max: 7200},
		},
		Gaps: []string{"CI pipeline: a ci event carries no run duration"},
	}
}

func TestRenderHTML_SpeedSectionReadsASlowerP50AsWorse(t *testing.T) {
	page := render(t, webFixture())
	i := strings.Index(page, `id="speed"`)
	if i < 0 {
		t.Fatal("the page has no speed section")
	}
	if i > strings.Index(page, `id="proposals"`) {
		t.Error("the speed section is not near the top summary")
	}
	sec := page[i:]
	sec = sec[:strings.Index(sec, "</section>")]
	for _, want := range []string{"commit gate: go test ./...", `<td class="n up" title="1.5m the window before">+5s</td>`, `<td class="n down" title="10s the window before">−6s</td>`, "not derivable", `href="#speed"`} {
		if !strings.Contains(sec+page[:strings.Index(page, "</header>")], want) {
			t.Errorf("the speed section lacks %q", want)
		}
	}
}

func TestRenderHTML_SpeedSectionWithoutRunsSaysSo(t *testing.T) {
	if page := render(t, Report{Title: "T", Repo: "r"}); !strings.Contains(page, "no runs timed") {
		t.Error("an empty speed section does not say no runs")
	}
}

func TestRenderHTML_ChangedLeadsThePageAndAnUnclearChangeReadsTilde(t *testing.T) {
	r := webFixture()
	r.Previous = &Previous{Window: "last 7d", Gone: []RuleCount{{Rule: "old-rule", N: 2}}}
	unclear := 3.0
	r.Speed.Rows = append(r.Speed.Rows, SpeedRow{Stage: "merge gate: x", N: 5, P50: 9, PrevN: 5, PrevP50: 6, Change: &unclear})
	page := render(t, r)
	c, f := strings.Index(page, `id="changed"`), strings.Index(page, `<dl class="facts">`)
	if c < 0 || c > f {
		t.Fatalf("the Changed section is not first (changed at %d, facts at %d)", c, f)
	}
	sec := page[c:]
	sec = sec[:strings.Index(sec, "</section>")]
	for _, want := range []string{"slower: commit gate: go test ./...", "gone: old-rule (was 2)"} {
		if !strings.Contains(sec, want) {
			t.Errorf("the Changed section lacks %q", want)
		}
	}
	if !strings.Contains(page, `<td class="n muted" title="6s the window before">~</td>`) {
		t.Error("an unclear change does not read ~")
	}
	if !strings.Contains(page, "since v1.1.0, 2026-10-05") {
		t.Error("the version columns are missing")
	}
}

func TestRenderHTML_NothingChangedIsOneLine(t *testing.T) {
	page := render(t, Report{Title: "T", Repo: "r", Window: "last 7d", Previous: &Previous{Window: "last 7d"}})
	if !strings.Contains(page, "no clear change against the previous 7d") {
		t.Error("the one line is missing")
	}
}

func TestRenderHTML_AVersionCellSaysTheVersionBeforeAndTheNoteSaysWhoIsLeftOut(t *testing.T) {
	page := render(t, webFixture())
	if !strings.Contains(page, `title="10s the version before">−6s`) {
		t.Error("a version's change cell does not name the version before")
	}
	if !strings.Contains(page, "no binary version are left out of the version split") || !strings.Contains(page, "first version with fewer than four runs stays on its own") {
		t.Error("the note does not say which runs the version split leaves out")
	}
}
