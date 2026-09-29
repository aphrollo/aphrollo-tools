package gc

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGraphCache(t *testing.T, state, name, header string) string {
	t.Helper()
	dir := filepath.Join(state, "ratchet-cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(header+"\n{\"ImportPath\":\"x\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Issue #994: the graph cache of a pruned worktree is proposed; the cache of a
// live one, an entry with no recorded root and the scan and dependency-graph
// caches beside them are not.
func TestGCGraphCaches_ProposesOnlyTheCachesOfWorktreesThatAreGone(t *testing.T) {
	state := t.TempDir()
	live := t.TempDir()
	gone := filepath.Join(t.TempDir(), "pruned-lane")
	goneCache := writeGraphCache(t, state, "golist-pruned-lane-000000000001.json", "fp "+gone)
	goneFirst := writeGraphCache(t, state, "golist-a-lane-000000000009.json", "fp "+gone)
	writeGraphCache(t, state, "golist-live-000000000002.json", "fp "+live)
	writeGraphCache(t, state, "golist-old-000000000003.json", "fp-only")
	writeGraphCache(t, state, "depgraph-x-000000000004.json", "fp "+gone)
	writeGraphCache(t, state, "lane-000000000005.json", "fp "+gone)

	got := gcGraphCaches(state)
	if len(got) != 2 || got[0].Path != goneFirst || got[1].Path != goneCache || got[0].Size == 0 || got[1].Size == 0 {
		t.Fatalf("candidates = %+v, want %s then %s, each with its size", got, goneFirst, goneCache)
	}
	if gcGraphCaches("") != nil {
		t.Error("an empty state dir proposed something")
	}
}

// Dry by default, deleted only by ApplyGC: the scan reads, the apply removes.
func TestScanGC_SweepsAPrunedWorktreesGraphCacheUnderGateDirs(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	gone := filepath.Join(t.TempDir(), "pruned-lane")
	path := writeGraphCache(t, StateDir(), "golist-pruned-lane-000000000001.json", "fp "+gone)

	found := false
	for _, c := range ScanGC(t.TempDir(), DefaultGCAge, GCScope{GateDirs: true}) {
		if c.Path == path {
			found = true
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("the scan removed the file: %v", err)
			}
			if freed, refused := ApplyGC([]GCCandidate{c}); freed == 0 || len(refused) != 0 {
				t.Fatalf("ApplyGC freed %d, refused %v", freed, refused)
			}
		}
	}
	if !found {
		t.Fatal("the GateDirs scope did not propose the pruned worktree's graph cache")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the cache survived ApplyGC: %v", err)
	}
}
