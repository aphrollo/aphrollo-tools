package report

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// laneAt is a time on 2026-10-06 at hh:mm UTC.
func laneAt(hh, mm int) time.Time { return time.Date(2026, 10, 6, hh, mm, 0, 0, time.UTC) }

// turnRec is an assistant record of an actor at a time, in the primary checkout
// (the cwd that names no lane), with out output tokens.
func turnRec(session, agent, req string, at time.Time, cwd string, out int, extra string) string {
	agentField := ""
	if agent != "" {
		agentField = fmt.Sprintf(`"agentId":%q,`, agent)
	}
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"sessionId":%q,%s"requestId":%q,"cwd":%s,"isSidechain":%t,`+
		`"message":{"model":"claude-opus-5","content":[%s],"usage":{"input_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":%d}}}`,
		at.Format(time.RFC3339), session, agentField, req, jsonStr(cwd), agent != "", extra, out)
}

func toolUse(name, input string) string {
	return fmt.Sprintf(`{"type":"tool_use","name":%q,"input":%s}`, name, input)
}

func logEv(kind, lane, actor string, at time.Time) tdd.Event {
	return tdd.Event{V: 1, Kind: kind, Lane: lane, Actor: actor, At: at.Format(time.RFC3339)}
}

func (f *usageFixture) scanWith(events []tdd.Event) UsageFacts {
	return ScanUsage(ScanOptions{ConfigDir: f.config, Repo: "myrepo", Since: usageNow.Add(-7 * 24 * time.Hour), Now: usageNow, Events: events})
}

func laneOut(u Usage, lane string) int64 { return group(u.ByLane, lane).Output }

func TestLane_ASubagentWhoseEventsAllNameOneLaneGivesItAllItsUsageToIt(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", filepath.Join("s1", "subagents", "agent-agA.jsonl"),
		turnRec("s1", "agA", "r1", laneAt(8, 0), f.repo, 10, ""),  // before its first lane event
		turnRec("s1", "agA", "r2", laneAt(10, 0), f.repo, 20, ""), // among them
		turnRec("s1", "agA", "r3", laneAt(15, 0), f.repo, 40, "")) // long after the last
	u := usage(t, f.scanWith([]tdd.Event{
		logEv("hook.timing", "lane/a", "s1/agA", laneAt(9, 0)), logEv("hook.timing", "lane/a", "s1/agA", laneAt(10, 30))}))
	if got := laneOut(u, "lane/a"); got != 70 {
		t.Errorf("lane/a output = %d, want all 70 of the builder's usage; lanes: %+v", got, u.ByLane)
	}
}

func TestLane_ACoordinatorMergingTwoLanesIsSplitByItsMergesAndOnlyItsOwnKindsCount(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", "s1.jsonl",
		turnRec("s1", "", "r1", laneAt(10, 5), f.repo, 1, ""),  // 5 minutes after merging lane/a
		turnRec("s1", "", "r2", laneAt(12, 10), f.repo, 2, ""), // after merging lane/b
		turnRec("s1", "", "r3", laneAt(11, 0), f.repo, 4, ""))  // an hour from either: coordination
	u := usage(t, f.scanWith([]tdd.Event{
		logEv("merge", "lane/a", "s1", laneAt(10, 0)),
		logEv("merge", "lane/b", "s1", laneAt(12, 0)),
		// A builder's edit carries the session id alone, so it must not move the coordinator.
		logEv("edit", "lane/c", "s1", laneAt(11, 0)),
	}))
	for lane, want := range map[string]int64{"lane/a": 1, "lane/b": 2, "coordination": 4, "lane/c": 0} {
		if got := laneOut(u, lane); got != want {
			t.Errorf("%s output = %d, want %d; lanes %+v", lane, got, want, u.ByLane)
		}
	}
}

func TestLane_EnterAndExitWorktreeToolCallsSwitchAndEndTheLaneAndNothingElseOfTheInputIsRead(t *testing.T) {
	f := newUsageFixture(t)
	enter := func(name string) string {
		return toolUse("EnterWorktree", fmt.Sprintf(`{"name":%q,"note":%s}`, name, jsonStr(secret)))
	}
	f.transcript("p", "s1.jsonl",
		turnRec("s1", "", "e1", laneAt(9, 0), f.repo, 0, enter("x")),
		turnRec("s1", "", "r1", laneAt(9, 10), f.repo, 1, ""),
		turnRec("s1", "", "e2", laneAt(10, 0), f.repo, 0, enter("y")),
		turnRec("s1", "", "r2", laneAt(10, 10), f.repo, 2, ""),
		turnRec("s1", "", "x1", laneAt(11, 0), f.repo, 0, toolUse("ExitWorktree", `{"action":"keep"}`)),
		turnRec("s1", "", "r3", laneAt(11, 20), f.repo, 4, ""))
	u := usage(t, f.scanWith(nil))
	for lane, want := range map[string]int64{"lane/x": 1, "lane/y": 2, "coordination": 4} {
		if got := laneOut(u, lane); got != want {
			t.Errorf("%s output = %d, want %d; lanes %+v", lane, got, want, u.ByLane)
		}
	}
	if j := fmt.Sprint(u.ByLane); strings.Contains(j, "SECRET") {
		t.Error("a tool input other than the worktree name reached the report")
	}
}

func TestLane_ASubagentOnSeveralLanesFollowsItsLatestLaneEventThenTheNextOne(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", filepath.Join("s1", "subagents", "agent-agB.jsonl"),
		turnRec("s1", "agB", "r1", laneAt(9, 0), f.repo, 1, ""),   // before any: the next one, lane/a
		turnRec("s1", "agB", "r2", laneAt(10, 30), f.repo, 2, ""), // after lane/a's event
		turnRec("s1", "agB", "r3", laneAt(13, 0), f.repo, 4, ""))  // after lane/b's, hours later
	u := usage(t, f.scanWith([]tdd.Event{
		logEv("hook.timing", "lane/a", "s1/agB", laneAt(10, 0)), logEv("hook.timing", "lane/b", "s1/agB", laneAt(11, 0))}))
	for lane, want := range map[string]int64{"lane/a": 3, "lane/b": 4} {
		if got := laneOut(u, lane); got != want {
			t.Errorf("%s output = %d, want %d; lanes %+v", lane, got, want, u.ByLane)
		}
	}
}

func TestLane_NoEventsAtAllIsUnattributedAndNeverGuessed(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", "s9.jsonl", turnRec("s9", "", "r1", laneAt(10, 0), f.repo, 7, ""))
	f.transcript("p", filepath.Join("s9", "subagents", "agent-agZ.jsonl"), turnRec("s9", "agZ", "r2", laneAt(10, 0), f.repo, 9, ""))
	u := usage(t, f.scanWith([]tdd.Event{logEv("merge", "lane/a", "other-session", laneAt(10, 0))}))
	if got := laneOut(u, "unattributed"); got != 16 {
		t.Errorf("unattributed output = %d, want 16 (the coordinator's and the subagent's); lanes %+v", got, u.ByLane)
	}
	if got := laneOut(u, "lane/a"); got != 0 {
		t.Errorf("another session's merge took %d tokens", got)
	}
}

func TestLane_AWorktreeCwdIsAlsoALaneEvent(t *testing.T) {
	f := newUsageFixture(t)
	f.transcript("p", "s1.jsonl", turnRec("s1", "", "r1", laneAt(10, 0), f.lane, 5, ""))
	if got := laneOut(usage(t, f.scanWith(nil)), "lane/lane1"); got != 5 {
		t.Errorf("lane/lane1 output = %d, want 5 from the cwd", got)
	}
}

func TestLane_ARemovedLanesPathShapeIsReadBeforeWalkingUpToAnEnclosingRepository(t *testing.T) {
	base := t.TempDir()
	// A path under .worktrees/<repo>/<lane> that sits inside some other repository.
	lane := filepath.Join(base, ".worktrees", "myrepo", "deep-lane", "sub")
	if err := os.MkdirAll(filepath.Join(base, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The lane directory is gone, as a removed lane's is: only the path says what it was.
	if repo, l := resolveCwd(lane); repo != "myrepo" || l != "lane/deep-lane" {
		t.Errorf("resolveCwd = %q, %q, want myrepo and lane/deep-lane (not the enclosing repository)", repo, l)
	}
}
