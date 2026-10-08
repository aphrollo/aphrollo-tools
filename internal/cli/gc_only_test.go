package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// gcOnlyWorld builds a box with all three things a sweep could reap: an idle
// incremental cache, an over-cap Go cache entry and a probe discard backup.
func gcOnlyWorld(t *testing.T) (repo, stale, cacheEntry, backup string) {
	t.Helper()
	gateConfigDir(t)
	cache := t.TempDir()
	cacheEntry = gcCacheShard(t, cache, 40*time.Hour)
	t.Cleanup(tdd.SetGoCacheDirForTest(cache))
	repo = gcCapRepo(t, "0.00000001GB")
	stale = filepath.Join(repo, "target", "debug", "incremental", "stale-1a2b")
	mkAgedFile(t, filepath.Join(stale, "dep-graph.bin"), "0123456789", 30*24*time.Hour)
	backup = filepath.Join(tdd.StateDir(), "probe-discard", "20260101T000000.000000000Z-r.patch")
	mkAgedFile(t, backup, "# aphrollo gate probe discard backup\n# file: a.go\n", 30*24*time.Hour)
	return repo, stale, cacheEntry, backup
}

func TestRunTDDGC_OnlyGocacheTrimsTheCacheAndNothingElse(t *testing.T) {
	repo, stale, cacheEntry, backup := gcOnlyWorld(t)
	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", repo, "--only", "gocache"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(cacheEntry); err == nil {
		t.Error("--only gocache left the over-cap cache entry")
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("--only gocache reaped the incremental cache: %v", err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Errorf("--only gocache reaped the probe discard backup: %v", err)
	}
}

func TestRunTDDGC_OnlyAnUnknownKindIsRefusedNamingTheValidOnes(t *testing.T) {
	repo, stale, cacheEntry, _ := gcOnlyWorld(t)
	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", repo, "--only", "gocache,bogus"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, want := range []string{"bogus", "gocache", "incremental", "gate-dir"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("refusal lacks %q:\n%s", want, stderr.String())
		}
	}
	if _, err := os.Stat(stale); err != nil {
		t.Error("a refused run deleted the incremental cache")
	}
	if _, err := os.Stat(cacheEntry); err != nil {
		t.Error("a refused run trimmed the Go cache")
	}
}

func TestRunTDDGC_OnlyDryPlansJustTheNamedKinds(t *testing.T) {
	repo, _, _, _ := gcOnlyWorld(t)
	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", repo, "--dry", "--only", "gocache"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "would remove 1 files") {
		t.Errorf("dry --only gocache lacks the trim plan:\n%s", out)
	}
	for _, not := range []string{"stale-1a2b", "probe discard backups", "state sizes"} {
		if strings.Contains(out, not) {
			t.Errorf("dry --only gocache names %q:\n%s", not, out)
		}
	}

	stdout.Reset()
	if code := runGateGC([]string{"--repo", repo, "--dry", "--only", "incremental"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out = stdout.String()
	if !strings.Contains(out, "stale-1a2b") {
		t.Errorf("dry --only incremental lacks the incremental cache:\n%s", out)
	}
	if strings.Contains(out, "would remove 1 files") || strings.Contains(out, "probe discard backups") {
		t.Errorf("dry --only incremental names other kinds:\n%s", out)
	}
}
