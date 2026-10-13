package measure

import (
	"reflect"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func mergeEv(sec float64, lane, pr, head string) tdd.Event {
	return ev(sec, lane, "merge", verdictDetail("ok", "pr", pr, "head", head))
}

func noPRBranch(int) (string, bool) { return "", false }

// An escape record is attributed to the lane it was recorded for, keyed by its PR when it
// names one.
func TestAttributeEscapes_ARecordNamesItsLane(t *testing.T) {
	evs := []tdd.Event{
		ev(10, "lane/a", "escape", verdictDetail("escape", "id", "e1")),
		ev(11, "lane/b", "escape", verdictDetail("false-positive", "id", "e2")),
	}
	got := AttributeEscapes(AttributionInput{Events: evs, PRBranch: noPRBranch})
	want := []Attribution{{Lane: "lane/a", Source: "record", Key: "record:e1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v (a false positive is no escape)", got, want)
	}
}

// An escape issue names a PR or a merge sha; the PR maps to the lane that merged it.
func TestAttributeEscapes_AnEscapeIssueMapsItsPRToTheLaneThatMergedIt(t *testing.T) {
	evs := []tdd.Event{mergeEv(5, "lane/a", "481", "abcdef1234567890abcdef1234567890abcdef12")}
	issues := []EscapeIssue{
		{Number: 500, Title: "escape: login 500s after the cart change", Body: "Caused by PR #481."},
		{Number: 501, Title: "escape: wrong total", Body: "Introduced by the merge abcdef1 on main."},
		{Number: 502, Title: "bug: unrelated", Body: "PR #481"},
	}
	got := AttributeEscapes(AttributionInput{Events: evs, Issues: issues, PRBranch: noPRBranch})
	want := []Attribution{{Lane: "lane/a", Source: "issue", Key: "pr:481"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want one escape for lane/a: the two issues name the same PR, and a non-escape title is ignored", got)
	}
}

// A PR no merge event names is mapped through its head branch, read from the host.
func TestAttributeEscapes_APRWithNoMergeEventIsMappedThroughItsHeadBranch(t *testing.T) {
	issues := []EscapeIssue{{Number: 7, Title: "escape: x", Body: "see (#90)"}}
	branch := func(n int) (string, bool) { return "lane/z", n == 90 }
	got := AttributeEscapes(AttributionInput{Issues: issues, PRBranch: branch})
	want := []Attribution{{Lane: "lane/z", Source: "issue", Key: "pr:90"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// An issue that names nothing mappable is left unattributed, never guessed.
func TestAttributeEscapes_AnIssueThatNamesNoKnownMergeIsUnattributed(t *testing.T) {
	issues := []EscapeIssue{{Number: 7, Title: "escape: x", Body: "it broke"}, {Number: 8, Title: "escape: y", Body: "PR #77"}}
	if got := AttributeEscapes(AttributionInput{Issues: issues, PRBranch: noPRBranch}); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

// The first red run on main after a merge, before the next merge, blames that merge's lane.
func TestAttributeEscapes_TheFirstRedOnMainAfterAMergeBlamesItsLane(t *testing.T) {
	evs := []tdd.Event{
		mergeEv(100, "lane/a", "1", "aaaaaaa"),
		mergeEv(300, "lane/b", "2", "bbbbbbb"),
		mergeEv(500, "lane/c", "3", "ccccccc"),
	}
	at := func(sec float64) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	reds := []RedRun{{At: at(150), Ref: "r1"}, {At: at(160), Ref: "r2"}, {At: at(550), Ref: "r3"}, {At: at(50), Ref: "before-any-merge"}}
	got := AttributeEscapes(AttributionInput{Events: evs, MainReds: reds, PRBranch: noPRBranch})
	want := []Attribution{
		{Lane: "lane/a", Source: "main-red", Key: "pr:1"},
		{Lane: "lane/c", Source: "main-red", Key: "pr:3"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v (lane/b merged and main stayed green until lane/c; the second red of lane/a's interval and the red before any merge blame no one)", got, want)
	}
}

// The same escape seen by two sources counts once, by the first source of the table.
func TestAttributeEscapes_AnEscapeSeenByTwoSourcesCountsOnce(t *testing.T) {
	evs := []tdd.Event{
		mergeEv(100, "lane/a", "481", "aaaaaaa"),
		ev(120, "lane/a", "escape", verdictDetail("escape", "id", "e1", "pr", "481")),
	}
	issues := []EscapeIssue{{Number: 9, Title: "escape: x", Body: "PR #481"}}
	reds := []RedRun{{At: base.Add(130 * time.Second), Ref: "r"}}
	got := AttributeEscapes(AttributionInput{Events: evs, Issues: issues, MainReds: reds, PRBranch: noPRBranch})
	want := []Attribution{{Lane: "lane/a", Source: "record", Key: "pr:481"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A blocked merge-gate run on trunk is a red run on main.
func TestMainRedsFromEvents_ABlockedMergeGateOnTrunkIsARedRun(t *testing.T) {
	evs := []tdd.Event{
		ev(10, "main", "merge_gate_result", func(e *tdd.Event) { e.Verdict = "blocked" }),
		ev(20, "main", "merge_gate_result", func(e *tdd.Event) { e.Verdict = "pass" }),
		ev(30, "lane/a", "merge_gate_result", func(e *tdd.Event) { e.Verdict = "blocked" }),
	}
	got := MainRedsFromEvents(evs)
	if len(got) != 1 || !got[0].At.Equal(base.Add(10*time.Second)) {
		t.Errorf("got %+v, want the one blocked gate run on main at +10s", got)
	}
}
