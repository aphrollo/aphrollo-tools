package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/measure"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// statsRepo is a repository with an event log of its own: a .git directory and a
// state root the test owns, seeded with events of the given ages.
func statsRepo(t *testing.T, events map[time.Duration]tdd.Event) string {
	t.Helper()
	t.Setenv("TRELLIS_DATA", t.TempDir())
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for age, e := range events {
		e.Root = repo
		e.At = now.Add(-age).Format("2006-01-02T15:04:05.000Z07:00")
		tdd.AppendEvent(e)
	}
	return repo
}

func runStatsCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := Run(append([]string{"stats"}, args...), strings.NewReader(""), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

func denyEvent(lane, rule string) tdd.Event {
	return tdd.Event{Kind: "deny", Lane: lane, Detail: map[string]string{"rule": rule}}
}

func TestStats_FoldsTheRepoEventsAsJSONUnderTheWindowAndLane(t *testing.T) {
	repo := statsRepo(t, map[time.Duration]tdd.Event{
		time.Hour:           denyEvent("lane/a", "r1"),
		2 * time.Hour:       denyEvent("lane/b", "r2"),
		30 * 24 * time.Hour: denyEvent("lane/a", "old"),
	})
	denies := func(args ...string) int {
		t.Helper()
		code, out, errOut := runStatsCmd(t, append([]string{"--repo", repo, "--json"}, args...)...)
		if code != 0 {
			t.Fatalf("stats %v exit = %d, stderr: %s", args, code, errOut)
		}
		var r measure.Report
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatalf("stats --json is not a Report: %v\n%s", err, out)
		}
		return r.Denies.Denies
	}
	if got := denies(); got != 3 {
		t.Errorf("the whole log has 3 denies, got %d", got)
	}
	if got := denies("--week"); got != 2 {
		t.Errorf("--week drops the 30-day-old deny, got %d", got)
	}
	if got := denies("--since", "90m"); got != 1 {
		t.Errorf("--since 90m keeps only the 1h-old deny, got %d", got)
	}
	if got := denies("--lane", "lane/a"); got != 2 {
		t.Errorf("--lane lane/a keeps its two denies, got %d", got)
	}
}

func TestStats_TextOutputNamesTheMeasures(t *testing.T) {
	repo := statsRepo(t, map[time.Duration]tdd.Event{time.Hour: denyEvent("lane/a", "r1")})
	code, out, errOut := runStatsCmd(t, "--repo", repo)
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	for _, want := range []string{"speed", "first-run CI", "gate wall time", "not tested", "edit->verdict", "edits per message", "denies", "wrong blocks", "escapes"} {
		if !strings.Contains(out, want) {
			t.Errorf("output has no %q line:\n%s", want, out)
		}
	}
}

func TestStats_RefusesWhatItDoesNotKnow(t *testing.T) {
	for _, args := range [][]string{
		{"--nope"},
		{"--week", "--since", "2d"},
		{"--since", "soon"},
		{"stray"},
		{"--repo", filepath.Join(t.TempDir(), "missing")},
	} {
		if code, _, errOut := runStatsCmd(t, args...); code != 2 || errOut == "" {
			t.Errorf("stats %v: exit = %d, stderr = %q; want exit 2 and a message", args, code, errOut)
		}
	}
}

func TestStats_FlagsAreHonouredAfterThePositionalsToo(t *testing.T) {
	repo := statsRepo(t, map[time.Duration]tdd.Event{time.Hour: denyEvent("lane/a", "r1")})
	code, out, errOut := runStatsCmd(t, "--json", "--repo", repo, "--lane", "lane/zzz")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(out, `"denies":0`) && !strings.Contains(out, `"denies": 0`) {
		t.Fatalf("a lane with no events has no denies:\n%s", out)
	}
}

// --briefs prints one line per installed text with its tokens and cap, and
// marks the over-cap ones. The numbers are today's; the test pins that they are
// printed, not what they are.
func TestStats_BriefsPrintsEveryInstalledTextAgainstItsCap(t *testing.T) {
	repo := statsRepo(t, nil)
	code, out, errOut := runStatsCmd(t, "--repo", repo, "--briefs")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	line := regexp.MustCompile(`\d+ bytes\s+\d+ tokens\s+cap \d+`)
	for _, name := range []string{"managed CLAUDE.md block", "tdd skill", "agent builder", "agent researcher", "agent reviewer"} {
		found := false
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, name) && line.MatchString(l) {
				found = true
			}
		}
		if !found {
			t.Errorf("no %q line with bytes, tokens and cap:\n%s", name, out)
		}
	}
}

func TestStats_BriefsAsJSONCarriesTheOverMark(t *testing.T) {
	repo := statsRepo(t, nil)
	code, out, errOut := runStatsCmd(t, "--repo", repo, "--briefs", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	var lines []measure.BriefLine
	if err := json.Unmarshal([]byte(out), &lines); err != nil || len(lines) != 5 {
		t.Fatalf("want 5 brief lines as JSON, got %d (%v):\n%s", len(lines), err, out)
	}
	for _, l := range lines {
		if l.Over != (l.Tokens > l.Cap) {
			t.Errorf("%s: over = %v with %d tokens against cap %d", l.Name, l.Over, l.Tokens, l.Cap)
		}
	}
}

func TestStats_ShadowSectionCountsTheFiresAndSaysWhenThereAreTooFewForARate(t *testing.T) {
	fire := func(rule, relation string) tdd.Event {
		return tdd.Event{Kind: "shadow", Lane: "lane/a", Detail: map[string]string{"rule": rule, "relation": relation}}
	}
	repo := statsRepo(t, map[time.Duration]tdd.Event{
		time.Hour:     fire("rerun-suite", "trellis-softer"),
		2 * time.Hour: fire("primary-write", "trellis-stricter"),
		3 * time.Hour: fire("primary-write", "agree"),
	})
	code, out, errOut := runStatsCmd(t, "--repo", repo, "--shadow")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	for _, want := range []string{"shadow fires          3 (whole log)", "primary-write", "would-be block 1", "rerun-suite", "softer 1", "under 10 fires: no rate"} {
		if !strings.Contains(out, want) {
			t.Errorf("--shadow output has no %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "denies") {
		t.Errorf("--shadow prints the shadow section alone, got:\n%s", out)
	}
	code, out, _ = runStatsCmd(t, "--repo", repo, "--shadow", "--json", "--week")
	var s measure.Shadow
	if err := json.Unmarshal([]byte(out), &s); code != 0 || err != nil || s.Fires != 3 || s.Window != "last 7d" {
		t.Errorf("--shadow --json --week = code %d, err %v, %+v; want 3 fires over the last 7d", code, err, s)
	}
}
