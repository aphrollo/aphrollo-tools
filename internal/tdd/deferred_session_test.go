package tdd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Two Claude sessions routinely stand in the same project. Keyed by project
// alone, one session's detached build wrote the record the OTHER session's
// next hook harvested: session B reported BUILDING for work it never started,
// then adopted a result describing an edit it never made.

func TestDeferredJob_IsNotVisibleToAnotherSessionOnTheSameProject(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	saveDeferredJob(DeferredJob{
		Project: root, Session: "sess-a", Phase: "build",
		Runner: []string{"cargo", "test"}, Started: time.Now(),
	})

	if _, ok := loadDeferredJob("sess-a", root); !ok {
		t.Fatal("the session that started the build must find its own job")
	}
	if _, ok := loadDeferredJob("sess-b", root); ok {
		t.Fatal("another session on the same project must not read this job")
	}
}

func TestDeferredJob_TwoSessionsKeepSeparateJobsInOneProject(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	saveDeferredJob(DeferredJob{Project: root, Session: "sess-a", Phase: "build", Runner: []string{"cargo", "test", "-p", "a"}})
	saveDeferredJob(DeferredJob{Project: root, Session: "sess-b", Phase: "run", Runner: []string{"cargo", "test", "-p", "b"}})

	a, ok := loadDeferredJob("sess-a", root)
	if !ok || a.Phase != "build" || a.Runner[3] != "a" {
		t.Fatalf("session a's job was overwritten: %+v (ok=%v)", a, ok)
	}
	b, ok := loadDeferredJob("sess-b", root)
	if !ok || b.Phase != "run" || b.Runner[3] != "b" {
		t.Fatalf("session b's job was overwritten: %+v (ok=%v)", b, ok)
	}
	// Clearing one leaves the other alone.
	clearDeferredJob("sess-a", root)
	if _, ok := loadDeferredJob("sess-a", root); ok {
		t.Fatal("clearing session a's job must remove it")
	}
	if _, ok := loadDeferredJob("sess-b", root); !ok {
		t.Fatal("clearing one session's job must not touch another's")
	}
}

func TestStatusLine_DeferredBadgeReadsThisSessionsBuildOnly(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	commitInitial(t, root)
	saveDeferredJob(DeferredJob{
		Project: root, Session: "sess-a", Phase: "build",
		Runner: []string{"cargo", "test"}, Started: time.Now(),
	})

	payload := func(session string) []byte {
		b, err := json.Marshal(map[string]any{"session_id": session, "cwd": root})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if got := StatusLine(payload("sess-a")); !strings.Contains(got, suffixDefer) {
		t.Fatalf("the session with a running build must see %q, got %q", suffixDefer, got)
	}
	if got := StatusLine(payload("sess-b")); strings.Contains(got, suffixDefer) {
		t.Fatalf("another session must not be told a build it never started is running, got %q", got)
	}
}
