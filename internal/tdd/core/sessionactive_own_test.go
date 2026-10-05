package core

import (
	"path/filepath"
	"testing"
	"time"
)

func sessactWrite(t *testing.T, id, root string, ts time.Time) {
	t.Helper()
	s, path := loadSession(id)
	if path == "" {
		t.Fatalf("no save path for session %s", id)
	}
	s.ByProject[root] = projectState{TS: ts.UTC().Format(time.RFC3339)}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
}

func TestLastSessionActivityIn_NamesTheNewestSessionThatWorkedInTheRoot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane := filepath.Join(t.TempDir(), "lane")
	other := filepath.Join(t.TempDir(), "other")
	now := time.Now().UTC().Truncate(time.Second)
	sessactWrite(t, "sess-old", lane, now.Add(-5*time.Hour))
	sessactWrite(t, "sess-new", lane, now.Add(-10*time.Minute))
	sessactWrite(t, "sess-elsewhere", other, now)

	got, ok := LastSessionActivityIn(lane)

	if !ok || !got.Equal(now.Add(-10*time.Minute)) {
		t.Errorf("LastSessionActivityIn = %v, %v; want %v", got, ok, now.Add(-10*time.Minute))
	}
}

func TestLastSessionActivityIn_ARootNoSessionWorkedInIsNotActive(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	sessactWrite(t, "sess-a", filepath.Join(t.TempDir(), "lane"), time.Now())

	if got, ok := LastSessionActivityIn(filepath.Join(t.TempDir(), "never")); ok {
		t.Errorf("a root no session touched reads as active at %v", got)
	}
}

func TestLastSessionActivityIn_ASessionInsideTheRootCounts(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	lane := filepath.Join(t.TempDir(), "lane")
	sessactWrite(t, "sess-a", filepath.Join(lane, "sub", "dir"), time.Now())

	if _, ok := LastSessionActivityIn(lane); !ok {
		t.Error("a session working in a subdirectory of the lane is not counted as holding it")
	}
}
