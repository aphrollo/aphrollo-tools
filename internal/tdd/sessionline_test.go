package tdd

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// The open points are on GitHub now, which means a session never sees them
// unless something says the number out loud. One line, at the one moment
// nothing is waiting on a build.
func TestIssueSummaryLineCountsOpenIssuesByLabel(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	stubGhScript(t, map[string]string{
		"issue list": `[{"labels":[{"name":"physics"}]},{"labels":[{"name":"netcode"}]},{"labels":[{"name":"physics"}]}]`,
	})
	if _, err := RecordEscape(EscapeOptions{Reason: "one that got through"}, io.Discard); err != nil {
		t.Fatal(err)
	}

	line := issueSummaryLine(repo, time.Now())
	for _, want := range []string{"3 open issues", "physics:2", "netcode:1", "1 open escape", "aphrollo gate issue", "gate escape record"} {
		if !strings.Contains(line, want) {
			t.Errorf("the session line must state %q; got %q", want, line)
		}
	}
}

// A network call per prompt is a session that pauses to talk to GitHub for a
// number nobody asked for. The answer is cached, and the cache is what keeps
// the line free.
func TestIssueSummaryLineServesTheCacheWithinTheHour(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	log := stubGhScript(t, map[string]string{"issue list": `[{"labels":[{"name":"physics"}]}]`})

	at := time.Now()
	first := issueSummaryLine(repo, at)
	if first == "" {
		t.Fatal("the first call must fetch and render a line")
	}
	if second := issueSummaryLine(repo, at.Add(59*time.Minute)); second != first {
		t.Errorf("within the hour the cached line is served verbatim: %q vs %q", second, first)
	}
	if n := strings.Count(ghArgv(t, log), "issue list"); n != 1 {
		t.Fatalf("gh ran `issue list` %d times inside the cache window, want 1:\n%s", n, ghArgv(t, log))
	}

	if issueSummaryLine(repo, at.Add(61*time.Minute)) == "" {
		t.Error("past the window the line is fetched again, not dropped")
	}
	if n := strings.Count(ghArgv(t, log), "issue list"); n != 2 {
		t.Errorf("gh ran `issue list` %d times across an expired window, want 2", n)
	}
}

// A session start is not the place to report that GitHub was unreachable, and
// a failure that reports nothing at all is a failure nobody can diagnose. So
// it prints nothing and leaves one line in gate.log.
func TestIssueSummaryLineIsSilentAndLoggedOnceWhenTheFetchFails(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	stubGhScript(t, map[string]string{"issue list": ""})
	t.Setenv("GH_STUB_ISSUE_LIST_FAIL", "gh: could not resolve host")

	at := time.Now()
	if line := issueSummaryLine(repo, at); line != "" {
		t.Fatalf("a failed fetch prints nothing, got %q", line)
	}
	// Second call inside the window: the failure is remembered, so neither
	// the fetch nor the log line repeats every prompt.
	if line := issueSummaryLine(repo, at.Add(time.Minute)); line != "" {
		t.Fatalf("a failed fetch stays silent, got %q", line)
	}
	data, err := os.ReadFile(GateLogPath())
	if err != nil {
		t.Fatalf("no gate.log written: %v", err)
	}
	if n := strings.Count(string(data), issuesFetchFailedVerdict); n != 1 {
		t.Errorf("gate.log carries %s %d times, want 1:\n%s", issuesFetchFailedVerdict, n, data)
	}
}

// A repo with no GitHub remote has no issues to count, and must not pay a gh
// process at every session start to learn that.
func TestIssueSummaryLineIsEmptyWithoutAGitHubRemote(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	if line := issueSummaryLine(t.TempDir(), time.Now()); line != "" {
		t.Errorf("no remote means no line, got %q", line)
	}
}

// The line is worth nothing unless the session start actually carries it —
// the count exists to be read at the one moment nothing is waiting on a
// build.
func TestSessionStartCarriesTheIssueSummaryLine(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// The detached sweep would still hold the fixture's directory when the
	// test tries to remove it.
	gcSpawnForTest = func(string) {}
	t.Cleanup(func() { gcSpawnForTest = nil })
	repo := makeGitHubRepo(t)
	stubGhScript(t, map[string]string{"issue list": `[{"labels":[{"name":"physics"}]}]`})

	msg := HandleSessionStart([]byte(`{"session_id":"s1","cwd":` + jsonString(repo) + `}`))
	if !strings.Contains(msg, "1 open issue") || !strings.Contains(msg, "physics:1") {
		t.Errorf("session start must carry the issue line:\n%s", msg)
	}
}

// jsonString quotes a path for a hook payload — a Windows root is full of
// backslashes, and a raw one would make the payload unparseable.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// A session start that waits on GitHub is a session start that hangs. The
// fetch carries its own deadline, and a slow remote reads as a failed fetch —
// silent, cached, one log line — not as a stalled prompt.
func TestIssueSummaryLineGivesUpOnASlowFetch(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := makeGitHubRepo(t)
	stubGhScript(t, map[string]string{"issue list": `[{"labels":[]}]`})
	t.Setenv("GH_STUB_SLEEP_MS", "3000")
	defer func(d time.Duration) { issuesFetchTimeout = d }(issuesFetchTimeout)
	issuesFetchTimeout = 200 * time.Millisecond

	started := time.Now()
	line := issueSummaryLine(repo, time.Now())
	elapsed := time.Since(started)

	if line != "" {
		t.Fatalf("a fetch past the deadline is a failed fetch, got %q", line)
	}
	// Generous headroom over the 200 ms deadline: the assertion is that the
	// call did not wait out the stub's full three seconds.
	if elapsed > 2*time.Second {
		t.Fatalf("the fetch waited %s — the deadline did not fire", elapsed)
	}
	data, err := os.ReadFile(GateLogPath())
	if err != nil || !strings.Contains(string(data), issuesFetchFailedVerdict) {
		t.Errorf("a timed-out fetch must be logged like any other failure: %v", err)
	}
}
