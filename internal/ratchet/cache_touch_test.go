package ratchet

import (
	"os"
	"testing"
	"time"
)

// The state directory's sweep evicts the least recently modified cache file
// first, so a cache that is read every run must have its time renewed by the
// read: an unchanged one is otherwise never rewritten and looks cold.
func TestCacheReads_RenewTheTimeOfAHotFileAndLeaveARecentOneAlone(t *testing.T) {
	law := Law{Name: "deps", CacheDir: t.TempDir()}
	writeDepGraphCache(law, "root", "fp", []Hit{{}})
	path := depGraphCachePath(law, "root")
	old := time.Now().Add(-3 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok := readDepGraphCache(law, "root", "fp"); !ok {
		t.Fatal("cache miss")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Hour {
		t.Errorf("a cache hit left the file dated %v", info.ModTime())
	}

	recent := time.Now().Add(-time.Minute).Truncate(time.Second)
	if err := os.Chtimes(path, recent, recent); err != nil {
		t.Fatal(err)
	}
	readDepGraphCache(law, "root", "fp")
	if info, _ := os.Stat(path); !info.ModTime().Equal(recent) {
		t.Errorf("a hit a minute after the last use rewrote the time to %v", info.ModTime())
	}
}
