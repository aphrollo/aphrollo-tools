package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func eventsLines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(EventLogPath())
	if err != nil {
		t.Fatalf("events.jsonl not written: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
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

func TestAppendGateLog_WritesAVersionedEventWithRepoAndLane(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	main, lane := laneRoot(t, "lane/x")

	AppendGateLog("precommit", lane, "go test ./...", "green", 1500*time.Millisecond)

	var e Event
	if err := json.Unmarshal([]byte(eventsLines(t)[0]), &e); err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339, e.At)
	if err != nil || at.Location() != time.UTC {
		t.Fatalf("at %q is not UTC RFC3339: %v", e.At, err)
	}
	if e.V != 1 || e.Kind != "commit_gate" || e.Verdict != "green" || e.Stage != "precommit" || e.Secs != 1.5 {
		t.Fatalf("event = %+v", e)
	}
	if e.Lane != "lane/x" || filepath.ToSlash(e.Repo) != filepath.ToSlash(main) {
		t.Fatalf("repo/lane = %q / %q, want %q / lane/x", e.Repo, e.Lane, main)
	}
}

func TestAppendGateLog_EventNeverCarriesTheCommandText(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	AppendGateLog("preedit", "/r", "curl -H token=SUPERSECRET", "pretooluse-denied:no-test", 0)

	line := eventsLines(t)[0]
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
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cases := []struct{ stage, verdict, want string }{
		{"postedit", "green", "edit"},
		{"premerge", "premerge-refused:x", "merge_gate"},
		{"commitmsg", "commitmsg-rejected:x", "commit_msg"},
		{"git", "git-discard-refused:x", "deny"},
		{"state", "state-corrupt:x", "gate"},
	}
	for _, c := range cases {
		AppendGateLog(c.stage, "/r", "c", c.verdict, 0)
	}
	for i, c := range cases {
		var e Event
		_ = json.Unmarshal([]byte(eventsLines(t)[i]), &e)
		if e.Kind != c.want {
			t.Errorf("%s/%s kind = %q, want %q", c.stage, c.verdict, e.Kind, c.want)
		}
	}
}

func TestAppendEvent_ConcurrentWritersNeverInterleaveLines(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			AppendEvent(Event{Kind: "push", Root: "/r", Verdict: "ok", Detail: map[string]string{"pad": strings.Repeat("x", 3000)}})
		}()
	}
	wg.Wait()
	lines := eventsLines(t)
	if len(lines) != 40 {
		t.Fatalf("%d lines, want 40", len(lines))
	}
	for _, l := range lines {
		var e Event
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("torn line: %v", err)
		}
	}
}

func TestReadEvents_SkipsUnknownVersionsAndTornLines(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	AppendEvent(Event{Kind: "push", Verdict: "ok"})
	f, err := os.OpenFile(EventLogPath(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{\"v\":99,\"kind\":\"future\"}\nnot json\n")
	_ = f.Close()
	AppendEvent(Event{Kind: "merge", Verdict: "ok"})

	got := ReadEvents()
	if len(got) != 2 || got[0].Kind != "push" || got[1].Kind != "merge" {
		t.Fatalf("ReadEvents = %+v, want push then merge", got)
	}
}

func TestAppendEvent_ASubdirectoryResolvesToItsRepoAndBranch(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	repo := filepath.Join(t.TempDir(), "proj")
	sub := filepath.Join(repo, "internal", "pkg")
	for _, d := range []string{filepath.Join(repo, ".git"), sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	AppendEvent(Event{Kind: "push", Root: sub})

	var e Event
	_ = json.Unmarshal([]byte(eventsLines(t)[0]), &e)
	if filepath.ToSlash(e.Repo) != filepath.ToSlash(repo) || e.Lane != "main" {
		t.Fatalf("repo/lane = %q / %q, want %q / main", e.Repo, e.Lane, repo)
	}
}
