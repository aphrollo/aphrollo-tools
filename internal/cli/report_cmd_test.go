package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/report"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

func runReportCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := Run(append([]string{"report"}, args...), strings.NewReader(""), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

// fakeTracker installs a Fake as the repo's code host and returns it.
func fakeTracker(t *testing.T) *host.Fake {
	t.Helper()
	f := &host.Fake{}
	f.OpenIssueFn = func(host.IssueRequest) (string, error) { return "https://github.com/o/r/issues/5", nil }
	prev := reportTrackerFn
	reportTrackerFn = func(string) (report.Tracker, string, bool) { return f, "aphrollo-tools", true }
	t.Cleanup(func() { reportTrackerFn = prev })
	return f
}

func callsOf(f *host.Fake, name string) int {
	n := 0
	for _, c := range f.Calls() {
		if c == name {
			n++
		}
	}
	return n
}

func TestReport_PlainPrintsTheSectionsAndTouchesNoHost(t *testing.T) {
	f := fakeTracker(t)
	repo := statsRepo(t, map[time.Duration]tdd.Event{time.Hour: denyEvent("lane/a", "r1")})
	code, out, errOut := runReportCmd(t, "--repo", repo)
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	for _, want := range []string{"1. Friction per rule", "r1", "6. Proposals"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if len(f.Calls()) != 0 {
		t.Errorf("a plain report called the host: %v", f.Calls())
	}
}

func TestReport_JSONIsTheReportModel(t *testing.T) {
	repo := statsRepo(t, map[time.Duration]tdd.Event{time.Hour: denyEvent("lane/a", "r1")})
	code, out, errOut := runReportCmd(t, "--repo", repo, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	var r report.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Friction) != 1 || r.Friction[0].Rule != "r1" {
		t.Errorf("json = %v, %+v", err, r.Friction)
	}
}

func TestReport_SinceNarrowsTheWindowAndABadOneIsRefused(t *testing.T) {
	repo := statsRepo(t, map[time.Duration]tdd.Event{time.Hour: denyEvent("lane/a", "new"), 3 * 24 * time.Hour: denyEvent("lane/a", "older")})
	_, out, _ := runReportCmd(t, "--repo", repo, "--since", "1d", "--json")
	var r report.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Friction) != 1 {
		t.Errorf("--since 1d friction = %+v (%v), want only the new rule", r.Friction, err)
	}
	if code, _, errOut := runReportCmd(t, "--repo", repo, "--since", "soon"); code != 2 || !strings.Contains(errOut, "since") {
		t.Errorf("a bad --since exit = %d, stderr %q", code, errOut)
	}
}

func TestReport_IssueOpensTheWeeksIssueAndDryOpensNothing(t *testing.T) {
	f := fakeTracker(t)
	repo := statsRepo(t, nil)
	if code, out, _ := runReportCmd(t, "--repo", repo, "--issue", "--dry"); code != 0 || !strings.Contains(out, "would open") || callsOf(f, "OpenIssue") != 0 {
		t.Errorf("--dry: code %d, out %q, opens %d", code, out, callsOf(f, "OpenIssue"))
	}
	code, out, errOut := runReportCmd(t, "--repo", repo, "--issue")
	if code != 0 || callsOf(f, "OpenIssue") != 1 || !strings.Contains(out, "issues/5") {
		t.Errorf("--issue: code %d, opens %d, out %q, stderr %q", code, callsOf(f, "OpenIssue"), out, errOut)
	}
}

func TestReport_IssueWithNoHostSaysSoAndFails(t *testing.T) {
	prev := reportTrackerFn
	reportTrackerFn = func(string) (report.Tracker, string, bool) { return nil, "", false }
	defer func() { reportTrackerFn = prev }()
	code, _, errOut := runReportCmd(t, "--repo", statsRepo(t, nil), "--issue")
	if code != 1 || !strings.Contains(errOut, "issue host") {
		t.Errorf("exit = %d, stderr %q, want 1 naming the missing issue host", code, errOut)
	}
}

func TestReport_AnUnknownFlagAndAStrayWordAreRefused(t *testing.T) {
	repo := statsRepo(t, nil)
	if code, _, _ := runReportCmd(t, "--repo", repo, "--nope"); code != 2 {
		t.Errorf("unknown flag exit = %d, want 2", code)
	}
	if code, _, errOut := runReportCmd(t, "--repo", repo, "stray"); code != 2 || !strings.Contains(errOut, "stray") {
		t.Errorf("stray word exit = %d, stderr %q", code, errOut)
	}
}

func TestWeeklyReport_RunsOnceAWeekAndIsStampedInTheGitCommonDir(t *testing.T) {
	f := fakeTracker(t)
	repo := statsRepo(t, nil)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if !weeklyReport(repo, now) || callsOf(f, "OpenIssue") != 1 {
		t.Fatalf("first run did not file: opens %d", callsOf(f, "OpenIssue"))
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", reportStampFile)); err != nil {
		t.Errorf("no stamp in the git common dir: %v", err)
	}
	if weeklyReport(repo, now.Add(6*24*time.Hour)) {
		t.Error("ran again 6 days later")
	}
	if !weeklyReport(repo, now.Add(7*24*time.Hour)) {
		t.Error("did not run 7 days later")
	}
}

func TestWeeklyReport_StaysOffWhenTheRepoOptsOutOrHasNoIssueHost(t *testing.T) {
	f := fakeTracker(t)
	repo := statsRepo(t, nil)
	if err := os.WriteFile(filepath.Join(repo, "aphrollo.toml"), []byte("[aphrollo]\nreport = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if weeklyReport(repo, time.Now()) || len(f.Calls()) != 0 {
		t.Errorf("report = false still reported: %v", f.Calls())
	}
	other := statsRepo(t, nil)
	reportTrackerFn = func(string) (report.Tracker, string, bool) { return nil, "", false }
	if weeklyReport(other, time.Now()) {
		t.Error("a repo with no issue host reported")
	}
}
