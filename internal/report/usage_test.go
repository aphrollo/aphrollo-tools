package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// secret is a string the report must never carry: it sits in a prompt, a tool
// input, a tool result and an injected text of the fixtures.
const secret = "sk-live-SECRET-0123456789" // gitleaks:allow (a fake key the privacy test plants)

type usageFixture struct {
	t      *testing.T
	config string
	repo   string // the main checkout, named myrepo
	lane   string // a linked worktree of it
}

func newUsageFixture(t *testing.T) *usageFixture {
	t.Helper()
	base := t.TempDir()
	f := &usageFixture{t: t, config: filepath.Join(base, "claude")}
	f.repo = filepath.Join(base, "myrepo")
	f.lane = filepath.Join(base, "wt", "lane1")
	gitdir := filepath.Join(f.repo, ".git", "worktrees", "lane1")
	for _, d := range []string{filepath.Join(f.repo, ".git"), gitdir, f.lane} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.write(filepath.Join(gitdir, "commondir"), "../..\n")
	f.write(filepath.Join(f.lane, ".git"), "gitdir: "+gitdir+"\n")
	return f
}

func (f *usageFixture) write(path, text string) {
	f.t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// transcript writes lines to <config>/projects/<dir>/<name>.
func (f *usageFixture) transcript(dir, name string, lines ...string) {
	f.t.Helper()
	p := filepath.Join(f.config, "projects", dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.write(p, strings.Join(lines, "\n")+"\n")
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

// asst is an assistant record of the harness's shape, with a prompt-like text and a tool input that carry the secret.
func asst(session, req, ts, cwd, model string, in, cc, cr, out, think int, sidechain bool) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"sessionId":%q,"requestId":%q,"cwd":%s,"isSidechain":%t,"gitBranch":"x","newField":{"a":1},`+
		`"message":{"model":%q,"content":[{"type":"text","text":%s},{"type":"tool_use","input":{"command":%s}}],`+
		`"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d,"output_tokens_details":{"thinking_tokens":%d},"future":1}}}`,
		ts, session, req, jsonStr(cwd), sidechain, model, jsonStr("reply "+secret), jsonStr("echo "+secret), in, cc, cr, out, think)
}

func hook(session, ts, cwd, event string, lines ...string) string {
	content, _ := json.Marshal([]string{strings.Join(lines, "\n")})
	return fmt.Sprintf(`{"type":"attachment","timestamp":%q,"sessionId":%q,"cwd":%s,"attachment":{"type":"hook_additional_context","hookEvent":%q,"content":%s}}`,
		ts, session, jsonStr(cwd), event, content)
}

var usageNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func (f *usageFixture) scan() UsageFacts {
	return ScanUsage(ScanOptions{ConfigDir: f.config, Repo: "myrepo", Since: usageNow.Add(-7 * 24 * time.Hour), Now: usageNow})
}

func usage(t *testing.T, facts UsageFacts) Usage {
	t.Helper()
	return BuildUsage(facts, usageNow, 7*24*time.Hour, time.Time{})
}

func group(gs []UsageGroup, key string) UsageGroup {
	for _, g := range gs {
		if g.Key == key {
			return g
		}
	}
	return UsageGroup{Key: "(missing " + key + ")"}
}

func TestUsage_AReplySplitOverRecordsSharingARequestIDIsCountedOnce(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", "s1.jsonl",
		asst("s1", "req_1", "2026-10-06T10:00:00Z", f.repo, "claude-opus-5", 10, 100, 1000, 5, 0, false),
		asst("s1", "req_1", "2026-10-06T10:00:01Z", f.repo, "claude-opus-5", 10, 100, 1000, 50, 7, false),
		asst("s1", "req_2", "2026-10-06T10:01:00Z", f.repo, "claude-opus-5", 20, 0, 2000, 10, 0, false))
	u := usage(t, f.scan())
	if u.Total.Turns != 2 || u.Total.Output != 60 || u.Total.Fresh != 30 || u.Total.Thinking != 7 {
		t.Errorf("total = %+v, want 2 turns, output 50+10, fresh 10+20, thinking 7 (req_1 counted once, by its fullest record)", u.Total)
	}
}

func TestUsage_TokensRegroupByDayLaneRoleAndModel(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p1", "s1.jsonl",
		asst("s1", "a", "2026-10-05T10:00:00Z", f.repo, "claude-opus-5", 1, 0, 0, 10, 0, false),
		asst("s1", "b", "2026-10-06T10:00:00Z", f.lane, "claude-sonnet-5", 1, 0, 0, 20, 0, false),
		asst("s1", "c", "2026-10-06T11:00:00Z", f.lane, "claude-sonnet-5", 1, 0, 0, 40, 0, true))
	u := usage(t, f.scan())
	if got := group(u.ByDay, "2026-10-06").Output; got != 60 {
		t.Errorf("2026-10-06 output = %d, want 60", got)
	}
	if got := group(u.ByLane, "lane/lane1").Output; got != 60 {
		t.Errorf("lane/lane1 output = %d, want 60 (the lane is read from the checkout, not the directory's name)", got)
	}
	if got := group(u.ByLane, laneCoordination).Output; got != 10 {
		t.Errorf("coordination output = %d, want 10: a coordinator turn in the primary checkout, far from its session's lane event", got)
	}
	if got := group(u.ByRole, "subagent").Output; got != 40 {
		t.Errorf("subagent output = %d, want 40", got)
	}
	if got := group(u.ByRole, "coordinator").Output; got != 30 {
		t.Errorf("coordinator output = %d, want 30", got)
	}
	if got := group(u.ByModel, "claude-sonnet-5").Turns; got != 2 {
		t.Errorf("sonnet turns = %d, want 2", got)
	}
}

func TestUsage_AnotherRepoAndRecordsOutsideTheWindowAreNotCounted(t *testing.T) {
	f := newUsageFixture(t)
	other := filepath.Join(filepath.Dir(f.repo), "otherrepo")
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.transcript("p", "s.jsonl",
		asst("s", "a", "2026-10-06T10:00:00Z", other, "claude-opus-5", 1, 0, 0, 99, 0, false),
		asst("s", "b", "2026-09-01T10:00:00Z", f.repo, "claude-opus-5", 1, 0, 0, 98, 0, false),
		asst("s", "c", "2026-10-06T10:00:00Z", f.repo, "claude-opus-5", 1, 0, 0, 7, 0, false))
	if u := usage(t, f.scan()); u.Total.Output != 7 {
		t.Errorf("output = %d, want 7 (one record of this repo in the window)", u.Total.Output)
	}
}

func TestUsage_ARemovedLaneIsReadFromItsWorktreePath(t *testing.T) {
	f := newUsageFixture(t)
	gone := `D:\Projects\.worktrees\myrepo\gone-lane`
	f.transcript("p", "s.jsonl", asst("s", "a", "2026-10-06T10:00:00Z", gone, "claude-opus-5", 1, 0, 0, 5, 0, false))
	if g := group(usage(t, f.scan()).ByLane, "lane/gone-lane"); g.Output != 5 {
		t.Errorf("lane/gone-lane = %+v, want the removed lane named by its path", g)
	}
}

func TestUsage_SubagentFilesAreSubagentTurns(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", filepath.Join("s1", "subagents", "agent-ab.jsonl"),
		asst("s1", "x", "2026-10-06T10:00:00Z", f.repo, "claude-haiku-4-5", 1, 0, 0, 3, 0, false))
	if g := group(usage(t, f.scan()).ByRole, "subagent"); g.Turns != 1 {
		t.Errorf("subagent = %+v, want the file under subagents/ counted as a subagent turn", g)
	}
}

func TestUsage_AMalformedLineIsCountedUnreadableAndTheRestStillCounts(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", "s.jsonl", `{"type":"assistant", broken`, asst("s", "a", "2026-10-06T10:00:00Z", f.repo, "claude-opus-5", 1, 0, 0, 5, 0, false), `not json at all`)
	u := usage(t, f.scan())
	if u.Unreadable != 2 || u.Total.Turns != 1 {
		t.Errorf("unreadable = %d, turns = %d, want 2 and 1", u.Unreadable, u.Total.Turns)
	}
	if !strings.Contains(u.Text(), "unreadable 2") {
		t.Errorf("the text does not say how many lines were skipped:\n%s", u.Text())
	}
}

func TestUsage_CostIsComputedAtReadTimeFromThePriceTable(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", "s.jsonl",
		asst("s", "a", "2026-10-06T10:00:00Z", f.repo, "claude-opus-5", 1_000_000, 1_000_000, 1_000_000, 1_000_000, 0, false),
		asst("s", "b", "2026-10-06T10:01:00Z", f.repo, "claude-mystery-9", 500, 0, 0, 500, 0, false))
	u := usage(t, f.scan())
	// 1M fresh at 5, 1M cache write at 6.25, 1M cache read at 0.5, 1M output at 25.
	if u.Total.CostUSD != 36.75 {
		t.Errorf("cost = %v, want 36.75", u.Total.CostUSD)
	}
	if u.UnpricedTokens != 1000 {
		t.Errorf("unpriced tokens = %d, want the unknown model's 1000 counted, not priced", u.UnpricedTokens)
	}
}

func TestUsage_InjectedTextIsMeasuredByHookEventAndGateLineKindNeverQuoted(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", "s.jsonl",
		asst("s", "a", "2026-10-06T10:00:00Z", f.repo, "claude-opus-5", 1000, 0, 100_000, 10, 0, false),
		hook("s", "2026-10-06T10:00:01Z", f.repo, "PostToolUse",
			"gate: go test ./x → green (3 passed)", "gate: go test ./y → outcome=red-missing-impl", "gate: go test ./z → TIMEOUT "+secret, "another hook said something long that is not ours"),
		hook("s", "2026-10-06T10:00:02Z", f.repo, "SessionStart", "gate: before writing code this session, read the skill"))
	u := usage(t, f.scan())
	if u.Injection.Tokens == 0 {
		t.Fatal("no injected tokens counted")
	}
	kinds := map[string]bool{}
	for _, k := range u.Injection.ByKind {
		kinds[k.Key] = true
	}
	for _, want := range []string{"green", "red-missing-impl", "not-tested", "session-start"} {
		if !kinds[want] {
			t.Errorf("no injection kind %q in %+v", want, u.Injection.ByKind)
		}
	}
	if g := group(u.Injection.ByEvent, "PostToolUse"); g.Count != 1 {
		t.Errorf("PostToolUse injections = %+v, want 1", g)
	}
	if u.Injection.FreshShare == "" {
		t.Error("no share of input")
	}
}

func TestUsage_ASecretInAPromptToolInputOrInjectedTextNeverReachesTheOutput(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", "s.jsonl",
		asst("s", "a", "2026-10-06T10:00:00Z", f.repo, "claude-opus-5", 1, 0, 0, 5, 0, false),
		hook("s", "2026-10-06T10:00:01Z", f.repo, "PostToolUse", "gate: leaked "+secret),
		`{"type":"user","timestamp":"2026-10-06T10:00:02Z","sessionId":"s","cwd":`+jsonStr(f.repo)+`,"message":{"content":"my password is `+secret+`"}}`)
	u := usage(t, f.scan())
	j, _ := json.Marshal(u)
	r := Build(Input{Now: usageNow, Window: 7 * 24 * time.Hour, Repo: "myrepo", Usage: &UsageFacts{}})
	r.Usage = &u
	rj, _ := json.Marshal(r)
	for name, out := range map[string]string{"usage text": u.Text(), "usage json": string(j), "report text": r.Text(), "report json": string(rj)} {
		if strings.Contains(out, secret) || strings.Contains(out, "SECRET") {
			t.Errorf("%s carries the secret:\n%s", name, out)
		}
	}
}

func TestUsage_CompareSplitsTheWindowAtTheGivenDay(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", "s.jsonl",
		asst("s", "a", "2026-10-02T10:00:00Z", f.repo, "claude-opus-5", 1, 0, 0, 100, 0, false),
		asst("s", "b", "2026-10-03T10:00:00Z", f.repo, "claude-opus-5", 1, 0, 0, 100, 0, false),
		asst("s", "c", "2026-10-06T10:00:00Z", f.repo, "claude-opus-5", 1, 0, 0, 40, 0, false))
	u := BuildUsage(f.scan(), usageNow, 7*24*time.Hour, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
	if u.Compare == nil || u.Compare.Before.Group.Output != 200 || u.Compare.After.Group.Output != 40 {
		t.Fatalf("compare = %+v, want 200 output before 2026-10-05 and 40 from it", u.Compare)
	}
	if u.Compare.Before.Days != 5 || u.Compare.After.Days != 3 {
		t.Errorf("days before/after = %d/%d, want 5/3 (Sep 30 to Oct 4, Oct 5 to 7)", u.Compare.Before.Days, u.Compare.After.Days)
	}
}

func TestUsage_AreplayOfTheSameFixtureIsTheSameBytes(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", "s.jsonl",
		asst("s", "a", "2026-10-06T10:00:00Z", f.repo, "claude-opus-5", 1, 5, 9, 5, 0, false),
		asst("t", "b", "2026-10-06T10:00:00Z", f.lane, "claude-sonnet-5", 1, 5, 9, 5, 0, false))
	a, _ := json.Marshal(usage(t, f.scan()))
	b, _ := json.Marshal(usage(t, f.scan()))
	if string(a) != string(b) {
		t.Error("two scans of one fixture differ")
	}
}

func TestUsage_OnlyAphrollosOwnInjectionsAreCountedAndTheShareIsOfFreshInput(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", "s.jsonl",
		asst("s", "a", "2026-10-06T10:00:00Z", f.repo, "claude-opus-5", 1000, 3000, 900_000, 10, 0, false),
		hook("s", "2026-10-06T10:00:01Z", f.repo, "PostToolUse", "an unrelated hook text that is long enough to count if it were counted"),
		hook("s", "2026-10-06T10:00:02Z", f.repo, "PostToolUse", "gate: go test ./x → green (3 passed)"))
	u := usage(t, f.scan())
	if u.Injection.Tokens != 10 { // "gate: go test ./x → green (3 passed)" is 38 bytes: 10 tokens
		t.Errorf("injected tokens = %d, want only the gate line's 10", u.Injection.Tokens)
	}
	if u.Injection.FreshShare != "0.250%" { // 10 of 1000 fresh + 3000 cache write
		t.Errorf("share = %q, want 0.250%% of the fresh input (the cache read is not in it)", u.Injection.FreshShare)
	}
	if !strings.Contains(u.Text(), "cache re-reads not counted") {
		t.Errorf("the text does not say what is counted:\n%s", u.Text())
	}
}

func TestUsage_AnOneHourCacheWriteCostsTwiceTheInputPriceAndTheFiveMinuteOneOnePointTwoFive(t *testing.T) {
	f := newUsageFixture(t)
	rec := `{"type":"assistant","timestamp":"2026-10-06T10:00:00Z","sessionId":"s","requestId":"a","cwd":` + jsonStr(f.repo) +
		`,"message":{"model":"claude-opus-5","usage":{"input_tokens":0,"cache_creation_input_tokens":2000000,"cache_read_input_tokens":0,"output_tokens":0,` +
		`"cache_creation":{"ephemeral_1h_input_tokens":1000000,"ephemeral_5m_input_tokens":1000000}}}}`
	f.transcript("p", "s.jsonl", rec)
	// 1M at 1.25 x 5 and 1M at 2 x 5.
	if u := usage(t, f.scan()); u.Total.CostUSD != 16.25 || u.Total.CacheWrite1h != 1_000_000 {
		t.Errorf("cost = %v, 1h = %d, want 16.25 and 1000000", u.Total.CostUSD, u.Total.CacheWrite1h)
	}
}

func TestUsage_ASyntheticRecordIsNotATurn(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", "s.jsonl",
		asst("s", "a", "2026-10-06T10:00:00Z", f.repo, "<synthetic>", 0, 0, 0, 0, 0, false),
		asst("s", "b", "2026-10-06T10:01:00Z", f.repo, "claude-opus-5", 1, 0, 0, 5, 0, false))
	if u := usage(t, f.scan()); u.Total.Turns != 1 || len(u.ByModel) != 1 {
		t.Errorf("turns %d, models %d, want the synthetic record left out", u.Total.Turns, len(u.ByModel))
	}
}

func TestUsage_ALineOverTheCapIsCountedUnreadableAndTheNextLineStillCounts(t *testing.T) {
	f := newUsageFixture(t)
	huge := `{"type":"user","timestamp":"2026-10-06T10:00:00Z","pad":"` + strings.Repeat("x", maxLineBytes+10) + `"}`
	f.transcript("p", "s.jsonl", huge, asst("s", "a", "2026-10-06T10:01:00Z", f.repo, "claude-opus-5", 1, 0, 0, 5, 0, false))
	u := usage(t, f.scan())
	if u.Unreadable != 1 || u.Total.Turns != 1 {
		t.Errorf("unreadable %d, turns %d, want the over-long line counted unreadable and the next one read", u.Unreadable, u.Total.Turns)
	}
}
