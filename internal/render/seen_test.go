package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
)

func TestReaches_followsWhatTheRecordedHooksDeliver(t *testing.T) {
	want := map[Hook]bool{
		// F3 results, 2026-10-03: additionalContext at these two reached the agent,
		// recorded with a payload fixture each.
		HookPostToolBatch: true, HookSubagentStart: true,
		// Fired in the recordings, but no recording shows what the agent read of
		// their output: an unproven hook repeats a line rather than lose it.
		HookPostToolUse: false, HookPreToolUse: false, HookUserPromptSubmit: false, HookSessionStart: false,
		HookStop: false, HookSubagentStop: false,
		// Output of these never reaches the agent, or the hook did not fire.
		HookCwdChanged: false, HookDirectoryAdded: false, HookSetup: false, HookSessionEnd: false,
		HookTaskCompleted: false, "FromTheFuture": false, "": false,
	}
	for h, w := range want {
		if got := Reaches(h); got != w {
			t.Errorf("Reaches(%q) = %v, want %v", h, got, w)
		}
	}
}

// TestReaches_everyCountedHookHasARecordedPayload ties the list to the
// evidence: a hook counts only while its recorded payload is committed.
func TestReaches_everyCountedHookHasARecordedPayload(t *testing.T) {
	fixtures := map[Hook]string{HookPostToolBatch: "posttoolbatch.json", HookSubagentStart: "subagentstart.json"}
	for h, file := range fixtures {
		if !Reaches(h) {
			t.Errorf("%s has a recorded payload but does not count", h)
		}
		if _, err := os.Stat(filepath.Join("..", "tdd", "internal", "tddtest", "testdata", "hooks", file)); err != nil {
			t.Errorf("%s counts as reaching the agent without its recorded payload: %v", h, err)
		}
	}
}

func TestDue_aLineIsSeenOnlyAfterADeliveryThatReachedThisActor(t *testing.T) {
	line := Green(Run{Unit: "internal/lane", Tree: "t1", Job: "j1"})
	other := Green(Run{Unit: "internal/lane", Tree: "t2", Job: "j2"})
	const me, sub = "s1/", "s1/a26c"
	cases := []struct {
		name string
		log  []Delivery
		who  string
		want bool
	}{
		{"never delivered", nil, me, true},
		{"delivered at PostToolBatch", []Delivery{line.Deliver(HookPostToolBatch, me)}, me, false},
		{"delivered by a hook that reaches nobody", []Delivery{line.Deliver(HookSetup, me)}, me, true},
		{"delivered to another actor", []Delivery{line.Deliver(HookSubagentStart, sub)}, me, true},
		{"the subagent saw it", []Delivery{line.Deliver(HookSubagentStart, sub)}, sub, false},
		{"another line was delivered", []Delivery{other.Deliver(HookPostToolBatch, me)}, me, true},
		{"a reaching delivery among useless ones", []Delivery{line.Deliver(HookCwdChanged, me), line.Deliver(HookPostToolBatch, me)}, me, false},
	}
	for _, c := range cases {
		if got := Due(line, c.who, c.log); got != c.want {
			t.Errorf("%s: Due = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDue_anEmptyLineIsNeverDue(t *testing.T) {
	if Due(Line{}, "s/", nil) {
		t.Error("a line that says nothing was reported due: it would be recorded as delivered for ever")
	}
}

func TestLineID_followsTheFactNotTheWording(t *testing.T) {
	base := Run{Unit: "internal/lane", Test: "TestX", Tree: "t1", Job: "j1", Verdict: kernel.VerdictRed, Assertion: "a"}
	same := base
	same.Assertion = "a different first line of the same red"
	if Red(base).ID != Red(same).ID {
		t.Error("the same red at the same tree changed identity with its wording: it would be delivered twice")
	}
	moved := base
	moved.Tree = "t2"
	if Red(base).ID == Red(moved).ID {
		t.Error("a red at a new tree kept the old identity: it would count as seen")
	}
	if Red(base).ID == Stale(base).ID {
		t.Error("a red and its stale copy share an identity")
	}
	if got := Red(base).ID; len(got) != 16 {
		t.Errorf("ID = %q, want 16 hex digits", got)
	}
}
