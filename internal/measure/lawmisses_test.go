package measure

import (
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func commitRefusal(sec float64, lane, hits string) tdd.Event {
	return ev(sec, lane, "commit_gate", func(e *tdd.Event) {
		e.Stage, e.Verdict = "precommit", "ratchet-rejected"
		if hits != "" {
			e.Detail = map[string]string{"hits": hits}
		}
	})
}

func editDeny(sec float64, lane, law, file string) tdd.Event {
	return ev(sec, lane, "deny", detail("rule", "ratchet:"+law, "file", file))
}

func editGuide(sec float64, lane, law, file string) tdd.Event {
	return ev(sec, lane, "guide", detail("rule", "ratchet:"+law, "file", file))
}

// A refused (law, file) pair counts as missed unless an edit-time deny or guide
// named the same pair earlier on the same lane.
func TestLawMisses_APairTheEditCheckNamedEarlierOnTheLaneIsNotMissed(t *testing.T) {
	events := []tdd.Event{
		editDeny(10, "lane/a", "module_size", "a.go"),
		editGuide(20, "lane/a", "no_todo", "b.go"),
		editDeny(30, "lane/b", "module_size", "c.go"),
		commitRefusal(100, "lane/a", "module_size|a.go;no_todo|b.go;no_todo|z.go"),
		commitRefusal(110, "lane/a", "module_size|c.go"),
		commitRefusal(120, "lane/c", "module_size|a.go"),
	}
	got := compute(events, Options{}).LawMisses
	if got.Refusals != 3 || got.Pairs != 5 || got.Missed != 3 {
		t.Fatalf("law misses = %+v, want 3 refusals, 5 pairs, 3 missed (z.go, c.go on lane a, a.go on lane c)", got)
	}
}

func TestLawMisses_AnEditEventAfterTheRefusalDoesNotCover(t *testing.T) {
	events := []tdd.Event{
		commitRefusal(10, "lane/a", "module_size|a.go"),
		editDeny(20, "lane/a", "module_size", "a.go"),
	}
	if got := compute(events, Options{}).LawMisses; got.Missed != 1 || got.Pairs != 1 {
		t.Fatalf("law misses = %+v, want the pair missed: the edit event came after", got)
	}
}

func TestLawMisses_ARefusalNamingNoPairIsUnattributedAndOthersCommitEventsAreIgnored(t *testing.T) {
	events := []tdd.Event{
		commitRefusal(10, "lane/a", ""),
		ev(20, "lane/a", "commit_gate", func(e *tdd.Event) { e.Verdict = "ratchet-clean" }),
	}
	got := compute(events, Options{}).LawMisses
	if got.Refusals != 1 || got.Unattributed != 1 || got.Pairs != 0 || got.Missed != 0 {
		t.Fatalf("law misses = %+v, want one unattributed refusal and no pairs", got)
	}
}

func TestLawMisses_TheWindowCountsRefusalsInsideItAndKeepsEarlierEditsAsCover(t *testing.T) {
	now := base.Add(48 * time.Hour)
	events := []tdd.Event{
		editDeny(10, "lane/a", "module_size", "a.go"),
		commitRefusal(20, "lane/a", "no_todo|old.go"),
		commitRefusal(47*3600, "lane/a", "module_size|a.go"),
	}
	got := Compute(events, now, Options{Window: 24 * time.Hour}).LawMisses
	if got.Refusals != 1 || got.Pairs != 1 || got.Missed != 0 {
		t.Fatalf("law misses = %+v, want only the recent refusal, covered by the old edit", got)
	}
	if got.WindowSecs != 24*3600 {
		t.Fatalf("window = %v, want 86400 s", got.WindowSecs)
	}
}

func TestText_ShowsTheMissedRefusalsAndTheGoModGap(t *testing.T) {
	r := compute([]tdd.Event{commitRefusal(10, "lane/a", "module_size|a.go")}, Options{Window: 30 * 24 * time.Hour})
	text := r.Text()
	for _, want := range []string{"commit refusals the edit check missed", "1 of 1", "last 30d", "go.mod"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
}

func TestLawMisses_ABackslashPathOfAnEditEventMatchesTheCommitsSlashPath(t *testing.T) {
	events := []tdd.Event{
		editDeny(10, "lane/a", "module_size", `internal\tdd\a.go`),
		commitRefusal(20, "lane/a", "module_size|internal/tdd/a.go"),
	}
	if got := compute(events, Options{}).LawMisses; got.Pairs != 1 || got.Missed != 0 {
		t.Fatalf("law misses = %+v, want the pair covered by the edit that spelled its path with backslashes", got)
	}
}
