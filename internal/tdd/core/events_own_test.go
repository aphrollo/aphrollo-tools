package core

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// isolateEvents gives a test its own gate state dir and its own state root,
// so no other test's events are in the log it reads.
func isolateEvents(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
}

// eventFiles are the month files of the repository root belongs to.
func eventFiles(t *testing.T, root string) []string {
	t.Helper()
	_, _, common := repoIdentity(root)
	names, err := filepath.Glob(filepath.Join(RepoStateDir(common), "events-*.jsonl"))
	if err != nil || len(names) == 0 {
		t.Fatalf("no events-*.jsonl for %q (glob err %v)", root, err)
	}
	return names
}

// eventsLines is every line of the repository's event files, oldest file first.
func eventsLines(t *testing.T, root string) []string {
	t.Helper()
	var lines []string
	for _, name := range eventFiles(t, root) {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(string(data), "\n") {
			if l != "" {
				lines = append(lines, l)
			}
		}
	}
	return lines
}

// A lane worktree: .git is a file pointing into <main>/.git/worktrees/<name>.
func laneRoot(t *testing.T, branch string) (main, lane string) {
	t.Helper()
	base := t.TempDir()
	main = filepath.Join(base, "proj")
	lane = filepath.Join(base, "lane")
	gitdir := filepath.Join(main, ".git", "worktrees", "lane")
	for _, d := range []string{gitdir, lane} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(lane, ".git"), []byte("gitdir: "+filepath.ToSlash(gitdir)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/"+branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return main, lane
}

// assertSeqGrowsWithTheFile fails unless every record has a sequence number and
// each is above the one before it: the order of the file is the order of seq.
func assertSeqGrowsWithTheFile(t *testing.T, events []Event) {
	t.Helper()
	var prev int64
	for i, e := range events {
		if e.Seq <= prev {
			t.Fatalf("record %d has seq %d after seq %d: the sequence does not grow with the file", i, e.Seq, prev)
		}
		prev = e.Seq
	}
}

// eventsTestRepo is a repository with a .git directory on branch main.
func eventsTestRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestAppendGateLog_WritesAVersionedEventWithRepoAndLane(t *testing.T) {
	isolateEvents(t)
	main, lane := laneRoot(t, "lane/x")

	AppendGateLog("precommit", lane, "go test ./...", "green", 1500*time.Millisecond)

	var e Event
	if err := json.Unmarshal([]byte(eventsLines(t, lane)[0]), &e); err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339, e.At)
	if err != nil || at.Location() != time.UTC {
		t.Fatalf("at %q is not UTC RFC3339: %v", e.At, err)
	}
	if e.V != 1 || e.Seq <= 0 || e.Kind != "commit_gate" || e.Verdict != "green" || e.Stage != "precommit" || e.Secs != 1.5 {
		t.Fatalf("event = %+v", e)
	}
	if e.Lane != "lane/x" || filepath.ToSlash(e.Repo) != filepath.ToSlash(main) {
		t.Fatalf("repo/lane = %q / %q, want %q / lane/x", e.Repo, e.Lane, main)
	}
}

// The final path of design section 3: <root>/state/<first 16 hex of the sha256
// of the git common dir>/events-YYYY-MM.jsonl, the same file for the main
// checkout and every lane worktree of one repository.
func TestAppendEvent_FilesUnderTheStateRootByRepoAndMonth(t *testing.T) {
	isolateEvents(t)
	main, lane := laneRoot(t, "lane/x")

	AppendEvent(Event{Kind: "push", Root: lane, At: "2026-10-02T09:14:03.120Z"})
	AppendEvent(Event{Kind: "push", Root: main, At: "2026-10-03T09:14:03.120Z"})

	key := repoStateKey(filepath.Join(main, ".git"))
	if len(key) != 16 {
		t.Fatalf("repo key %q is not 16 hex digits", key)
	}
	want := filepath.Join(os.Getenv("TRELLIS_DATA"), "state", key, "events-2026-10.jsonl")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("no log at %s: %v", want, err)
	}
	if n := strings.Count(string(data), "\n{"); n != 2 {
		t.Fatalf("%d records at %s, want the lane's and the main checkout's: %s", n, want, data)
	}
}

func TestAppendGateLog_EventNeverCarriesTheCommandText(t *testing.T) {
	isolateEvents(t)

	AppendGateLog("preedit", "/r", "curl -H token=SUPERSECRET", "pretooluse-denied:no-test", 0)

	line := eventsLines(t, "/r")[0]
	if strings.Contains(line, "SUPERSECRET") {
		t.Fatalf("command text leaked into the event: %s", line)
	}
	var e Event
	_ = json.Unmarshal([]byte(line), &e)
	if e.Kind != "deny" {
		t.Fatalf("kind = %q, want deny", e.Kind)
	}
}

func TestAppendGateLog_KindsByStage(t *testing.T) {
	isolateEvents(t)
	cases := []struct{ stage, verdict, want string }{
		{"postedit", "green", "stage.timing"},
		{"premerge", "premerge-refused:x", "merge_gate"},
		{"commitmsg", "commitmsg-rejected:x", "commit_msg"},
		{"git", "git-discard-refused:x", "deny"},
		{"state", "state-corrupt:x", "gate"},
		{"session", "override-off", "override"},
		{"preedit", "smell-escape:test-sleep", "override"},
		{"postedit", "queued-skipped", "run.result"},
	}
	for _, c := range cases {
		AppendGateLog(c.stage, "/r", "c", c.verdict, 0)
	}
	lines := eventsLines(t, "/r")
	for i, c := range cases {
		var e Event
		_ = json.Unmarshal([]byte(lines[i]), &e)
		if e.Kind != c.want {
			t.Errorf("%s/%s kind = %q, want %q", c.stage, c.verdict, e.Kind, c.want)
		}
	}
}

// Every way a run proves nothing carries its cause, so "not tested, by cause"
// is a count of one field.
func TestAppendGateLog_NotTestedRunsCarryTheirCause(t *testing.T) {
	isolateEvents(t)
	cases := []struct{ verdict, cause string }{
		{"timeout", "timeout"},
		{"timeout-rejected", "timeout"},
		{"lint-timeout", "timeout"},
		{"skipped", "skipped"},
		{"skipped-tool-missing", "skipped"},
		{"queued-skipped", "queued"},
		{"queued-rejected", "queued"},
		{"deferred", "deferred"},
		{"deferred-abandoned", "deferred"},
		{"infra-failed", "infra"},
		{"no-tests-selected", "no-tests"},
	}
	for _, c := range cases {
		AppendGateLog("postedit", "/r", "go test", c.verdict, 2*time.Second)
	}

	got := ReadEvents("/r")
	if len(got) != len(cases) {
		t.Fatalf("%d events, want %d", len(got), len(cases))
	}
	for i, c := range cases {
		e := got[i]
		if e.Kind != "run.result" || e.Detail["result"] != "not-tested" || e.Detail["cause"] != c.cause || e.Secs != 2 || e.Stage != "postedit" {
			t.Errorf("%s: event = %+v, want a not-tested run.result with cause %q on stage postedit taking 2s", c.verdict, e, c.cause)
		}
	}
}

func TestAppendGateLog_ARunThatProvedSomethingIsNotMarkedNotTested(t *testing.T) {
	isolateEvents(t)

	AppendGateLog("postedit", "/r", "go test", "red", time.Second)
	AppendGateLog("postedit", "/r", "go test", "green", time.Second)

	for _, e := range ReadEvents("/r") {
		if e.Kind == "run.result" || e.Detail["cause"] != "" {
			t.Fatalf("a settled run read as not tested: %+v", e)
		}
	}
}

func TestAppendGateLog_ADenyNamesItsRuleAndAnOverrideNamesItself(t *testing.T) {
	isolateEvents(t)

	AppendGateLog("preedit", "/r", "f.go", "pretooluse-denied:test-sleep", 0)
	AppendGateLog("session", "/r", "s1", "override-primary-allow", 0)

	got := ReadEvents("/r")
	if got[0].Kind != "deny" || got[0].Detail["rule"] != "test-sleep" {
		t.Errorf("deny event = %+v, want rule test-sleep", got[0])
	}
	if got[1].Kind != "override" || got[1].Detail["override"] != "override-primary-allow" {
		t.Errorf("override event = %+v, want override override-primary-allow", got[1])
	}
}

// A site that knows a deny's cause and the override it offered passes them;
// they ride on the event beside the rule the verdict names.
func TestAppendGateLogDetail_TheSitesDetailRidesOnTheEvent(t *testing.T) {
	isolateEvents(t)

	AppendGateLogDetail("preedit", "/r", "f.go", "pretooluse-denied:test-sleep", 0,
		map[string]string{"cause": "smell", "override": "real-time:"})

	e := ReadEvents("/r")[0]
	want := map[string]string{"rule": "test-sleep", "cause": "smell", "override": "real-time:"}
	if !reflect.DeepEqual(e.Detail, want) {
		t.Fatalf("detail = %v, want %v", e.Detail, want)
	}
}

func TestAppendEvent_ConcurrentWritersNeverInterleaveLines(t *testing.T) {
	isolateEvents(t)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			AppendEvent(Event{Kind: "push", Root: "/r", Verdict: "ok", Detail: map[string]string{"pad": strings.Repeat("x", 3000)}})
		}()
	}
	wg.Wait()
	lines := eventsLines(t, "/r")
	if len(lines) != 40 {
		t.Fatalf("%d lines, want 40", len(lines))
	}
	for _, l := range lines {
		var e Event
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("torn line: %v", err)
		}
	}
	// A writer that waits out the lock's bound on a loaded box writes its record
	// unnumbered, so the numbers are the reader's: one per record, growing with
	// the file, whichever writers got the lock.
	read := ReadEvents("/r")
	if len(read) != 40 {
		t.Fatalf("%d records read, want 40", len(read))
	}
	assertSeqGrowsWithTheFile(t, read)
}

// runEventAppendHelper is what a child process of the two-process test does
// instead of running the tests (TestMain hands over to it): append its share
// of events to the shared log and exit.
func runEventAppendHelper() int {
	n, err := strconv.Atoi(os.Getenv("EVENT_HELPER_COUNT"))
	if err != nil {
		return 2
	}
	for i := 0; i < n; i++ {
		AppendEvent(Event{Kind: "push", Root: os.Getenv("EVENT_HELPER_ROOT"), Verdict: "ok",
			Detail: map[string]string{"who": os.Getenv("EVENT_HELPER_WHO"), "pad": strings.Repeat("y", 2000)}})
	}
	return 0
}

func TestAppendEvent_TwoProcessesNeverLoseOrTearARecord(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	const each = 60
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var procs []*exec.Cmd
	for _, who := range []string{"a", "b"} {
		cmd := exec.CommandContext(ctx, os.Args[0])
		cmd.Env = append(os.Environ(), "EVENT_HELPER_COUNT="+strconv.Itoa(each), "EVENT_HELPER_ROOT="+repo, "EVENT_HELPER_WHO="+who)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		procs = append(procs, cmd)
	}
	for _, cmd := range procs {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper process: %v", err)
		}
	}

	byWho := map[string]int{}
	events := ReadEvents(repo)
	for _, e := range events {
		byWho[e.Detail["who"]]++
	}
	if byWho["a"] != each || byWho["b"] != each {
		t.Fatalf("records by writer = %v, want %d each (a record was lost or torn)", byWho, each)
	}
	assertSeqGrowsWithTheFile(t, events)
}

func TestReadEvents_SkipsUnknownVersionsAndTornLines(t *testing.T) {
	isolateEvents(t)
	AppendEvent(Event{Kind: "push", Verdict: "ok"})
	f, err := os.OpenFile(eventFiles(t, "")[0], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{\"v\":99,\"kind\":\"future\"}\nnot json\n")
	_ = f.Close()
	AppendEvent(Event{Kind: "merge", Verdict: "ok"})

	got := ReadEvents("")
	if len(got) != 2 || got[0].Kind != "push" || got[1].Kind != "merge" {
		t.Fatalf("ReadEvents = %+v, want push then merge", got)
	}
}

// A writer that crashed mid-record leaves a last line with no newline: it is
// skipped, and the records before it are kept.
func TestReadEvents_SkipsATornLastLine(t *testing.T) {
	isolateEvents(t)
	AppendEvent(Event{Kind: "push", Verdict: "ok"})
	f, err := os.OpenFile(eventFiles(t, "")[0], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"v":1,"seq":2,"kind":"to`)
	_ = f.Close()

	got := ReadEvents("")
	if len(got) != 1 || got[0].Kind != "push" {
		t.Fatalf("ReadEvents = %+v, want only the push", got)
	}
}

// The files are split by month of the event's "at", and the sequence keeps
// growing across the split.
func TestAppendEvent_RecordsSplitByMonthAndTheSequenceGrowsAcrossThem(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)

	AppendEvent(Event{Kind: "push", Root: repo, At: "2026-09-30T23:59:59.000Z"})
	AppendEvent(Event{Kind: "push", Root: repo, At: "2026-09-30T23:59:59.500Z"})
	AppendEvent(Event{Kind: "push", Root: repo, At: "2026-10-01T00:00:00.100Z"})

	events := ReadEvents(repo)
	if len(events) != 3 {
		t.Fatalf("%d events, want 3", len(events))
	}
	assertSeqGrowsWithTheFile(t, events)
	if n := len(eventFiles(t, repo)); n != 2 {
		t.Fatalf("%d month files, want 2", n)
	}
}

func TestEvent_EveryFieldSurvivesTheRoundTrip(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	want := Event{
		V: EventSchema, Seq: 1, At: "2026-10-02T09:14:03.120Z", Lane: "lane/x", Actor: "s1/a1", Kind: "deny",
		Repo: filepath.ToSlash(repo), Stage: "preedit", Verdict: "pretooluse-denied:r", Secs: 0.25,
		Detail: map[string]string{"rule": "r", "cause": "smell"},
		Root:   repo, Cmd: "go test ./pkg", BinVer: "3.2.1",
	}

	AppendEvent(Event{Root: repo, Cmd: want.Cmd, Lane: want.Lane, Actor: want.Actor, Kind: want.Kind, At: want.At, Stage: want.Stage,
		Verdict: want.Verdict, Secs: want.Secs, Detail: want.Detail, BinVer: want.BinVer})

	got := ReadEvents(repo)
	if len(got) == 1 && got[0].Seq > 0 {
		want.Seq = got[0].Seq // the writer's number; the test cannot know it
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("ReadEvents = %+v, want [%+v]", got, want)
	}
}

func TestAppendEvent_TheActorDefaultsToTheSessionInTheEnvironment(t *testing.T) {
	isolateEvents(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-7")
	t.Setenv("CLAUDE_SESSION_ID", "")

	AppendEvent(Event{Kind: "push"})
	AppendEvent(Event{Kind: "push", Actor: "sess-9/agent-1"})

	got := ReadEvents("")
	if got[0].Actor != "sess-7" || got[1].Actor != "sess-9/agent-1" {
		t.Fatalf("actors = %q, %q, want sess-7 then the explicit sess-9/agent-1", got[0].Actor, got[1].Actor)
	}
}

// Records the older single-file log holds stay readable, under today's kind
// names, for the repository they name.
func TestReadEvents_ReadsTheOlderSingleFileLogForItsRepo(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	legacy := `{"v":1,"at":"2026-09-01T00:00:00Z","kind":"edit","repo":"` + filepath.ToSlash(repo) + `","stage":"postedit","verdict":"green"}` + "\n" +
		`{"v":1,"at":"2026-09-01T00:00:01Z","kind":"push","repo":"/somewhere/else"}` + "\n"
	if err := os.MkdirAll(StateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(StateDir(), "events.jsonl"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	AppendEvent(Event{Kind: "push", Root: repo})

	got := ReadEvents(repo)
	if len(got) != 2 || got[0].Kind != "stage.timing" || got[0].Verdict != "green" || got[1].Kind != "push" {
		t.Fatalf("ReadEvents = %+v, want the old edit line as stage.timing, then the new push; the other repo's line left out", got)
	}
}

func TestAppendEvent_ASubdirectoryResolvesToItsRepoAndBranch(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	sub := filepath.Join(repo, "internal", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	AppendEvent(Event{Kind: "push", Root: sub})

	var e Event
	_ = json.Unmarshal([]byte(eventsLines(t, repo)[0]), &e)
	if filepath.ToSlash(e.Repo) != filepath.ToSlash(repo) || e.Lane != "main" {
		t.Fatalf("repo/lane = %q / %q, want %q / main", e.Repo, e.Lane, repo)
	}
}

// An event log that cannot be written must say so once: the targets are all
// computed from it, and a silently empty file reads as "nothing happened".
func TestAppendEvent_WarnsOncePerProcessWhenTheLogCannotBeWritten(t *testing.T) {
	resetEventLogWarnForTest()
	isolateEvents(t)
	// A directory where the month file belongs makes the open fail.
	if err := os.MkdirAll(eventLogFile(RepoStateDir(""), time.Now()), 0o755); err != nil {
		t.Fatal(err)
	}

	stderr := captureStderr(t, func() {
		AppendEvent(Event{Kind: "push", Verdict: "ok"})
		AppendEvent(Event{Kind: "merge", Verdict: "ok"})
	})

	if n := strings.Count(stderr, "the event log is not being written"); n != 1 {
		t.Fatalf("warning printed %d time(s) across two failed writes, want 1:\n%s", n, stderr)
	}
}

// An event that already knows its lane (an escape recorded from the main
// checkout, naming the PR's lane) keeps it instead of the checkout's branch.
func TestAppendEvent_AnExplicitLaneBeatsTheRootsBranch(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)

	AppendEvent(Event{Kind: "escape", Root: repo, Lane: "lane/fix"})

	var e Event
	_ = json.Unmarshal([]byte(eventsLines(t, repo)[0]), &e)
	if e.Lane != "lane/fix" || filepath.ToSlash(e.Repo) != filepath.ToSlash(repo) {
		t.Fatalf("repo/lane = %q / %q, want %q / lane/fix", e.Repo, e.Lane, repo)
	}
}

// A settled CI result is recorded once per commit, however many times the
// verbs that read it run.
func TestAppendEventOnce_WritesOneRecordPerKey(t *testing.T) {
	isolateEvents(t)

	first := AppendEventOnce(Event{Kind: "ci", Verdict: "green", Detail: map[string]string{"sha": "abc"}}, "sha")
	again := AppendEventOnce(Event{Kind: "ci", Verdict: "green", Detail: map[string]string{"sha": "abc"}}, "sha")
	other := AppendEventOnce(Event{Kind: "ci", Verdict: "red", Detail: map[string]string{"sha": "def"}}, "sha")

	if !first || again || !other {
		t.Fatalf("wrote first/again/other = %v/%v/%v, want true/false/true", first, again, other)
	}
	if n := len(eventsLines(t, "")); n != 2 {
		t.Fatalf("%d lines, want 2", n)
	}
}

// ratchet: test_removed TestStateRoot_PrefersTheExplicitRootThenTheWindowsThenTheXdgDirectory: split by platform into TestStateRoot_PrefersTheExplicitRootThenTheXdgDirectory and the windows and other files, because LOCALAPPDATA now counts on Windows only

// The append budget is 2 ms. The bound here is ten times that so a busy box
// does not fail it; the mean is logged for the record.
func TestAppendEvent_CostPerRecord(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	AppendEvent(Event{Kind: "push", Root: repo}) // creates the directory and the lock file
	const n = 200

	start := time.Now()
	for i := 0; i < n; i++ {
		AppendEvent(Event{Kind: "stage.timing", Root: repo, Stage: "postedit", Verdict: "green", Secs: 1.2,
			Detail: map[string]string{"rule": "r", "cause": "smell"}})
	}
	mean := time.Since(start) / n

	t.Logf("append cost: %v per record (mean of %d)", mean, n)
	if mean > 20*time.Millisecond {
		t.Fatalf("append cost %v per record, want under 2 ms (bound 20 ms for a loaded box)", mean)
	}
}

// The sequence number is the month as YYYYMM above the byte offset the record
// starts at, so it grows with the file and across month files.
func TestEventSeq_IsTheMonthAboveTheByteOffset(t *testing.T) {
	at := time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)

	if got, want := eventSeq(at, 5), int64(222772050903695365); got != want {
		t.Fatalf("eventSeq(2026-10, 5) = %d, want %d (202610 << 40 | 5)", got, want)
	}
	if next := eventSeq(time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC), 0); next <= eventSeq(at, 1<<39) {
		t.Fatalf("a record of the next month numbers %d, not above the last of the month before", next)
	}
}

func TestStateRoot_FallsBackToTheStateDirectoryUnderTheHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TRELLIS_DATA", "")
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if got, want := StateRoot(), filepath.Join(home, ".local", "state", "trellis"); got != want {
		t.Fatalf("StateRoot = %q, want %q", got, want)
	}
}

// With no home to resolve there is no state root, and an event is dropped
// rather than written somewhere unintended.
func TestStateRoot_IsEmptyWhenNoHomeCanBeResolved(t *testing.T) {
	t.Setenv("TRELLIS_DATA", "")
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	if got := StateRoot(); got != "" {
		t.Fatalf("StateRoot = %q, want none", got)
	}
}

func TestEventLogDir_isWhereARecordFromAnyWorktreeOfTheRepoLands(t *testing.T) {
	isolateEvents(t)
	main, lane := laneRoot(t, "lane/why")
	AppendEvent(Event{Kind: "deny", Root: lane})
	written := filepath.Dir(eventFiles(t, main)[0])
	for _, root := range []string{main, lane} {
		if got := EventLogDir(root); got != written {
			t.Errorf("EventLogDir(%s) = %q, want the directory the record landed in: %q", root, got, written)
		}
	}
}
