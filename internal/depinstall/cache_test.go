package depinstall

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func keyOf(t *testing.T, files map[string]string) (string, bool) {
	t.Helper()
	root := nodeRoot(t, false, files)
	return CacheKey(mustDetect(t, root), root)
}

// Two merges with the same lockfile and manifest share one install, whichever
// lane or checkout they run in; a moved dependency is another one.
func TestCacheKey_FollowsTheLockfileAndManifestBytes(t *testing.T) {
	a, ok := keyOf(t, map[string]string{"package.json": `{"name":"x"}`, "package-lock.json": `{"v":1}`})
	b, _ := keyOf(t, map[string]string{"package.json": `{"name":"x"}`, "package-lock.json": `{"v":1}`})
	c, _ := keyOf(t, map[string]string{"package.json": `{"name":"x"}`, "package-lock.json": `{"v":2}`})
	d, _ := keyOf(t, map[string]string{"package.json": `{"name":"y"}`, "package-lock.json": `{"v":1}`})
	if !ok || a == "" || a != b {
		t.Errorf("same bytes: keys %q and %q (ok=%v), want one equal key", a, b, ok)
	}
	if a == c || a == d {
		t.Errorf("a changed lockfile or manifest kept the key: %q %q %q", a, c, d)
	}
}

// A bare package.json pins nothing, so no install of it can be reused by key.
func TestCacheKey_BareManifestHasNoKey(t *testing.T) {
	if k, ok := keyOf(t, map[string]string{"package.json": `{}`}); ok {
		t.Errorf("CacheKey = %q, want none for an unpinned install", k)
	}
}

// An install made in a checkout is moved into the cache and found by key; a
// second store of the same key keeps the first.
func TestStore_AMovedInstallIsFoundByKey(t *testing.T) {
	cache := t.TempDir()
	co := nodeRoot(t, true, map[string]string{"package.json": `{}`, "package-lock.json": `{}`})
	nm := filepath.Join(co, NodeModules)

	dir, ok := Store(cache, "k1", nm)
	if !ok {
		t.Fatal("a self-contained install was not stored")
	}
	if _, err := os.Stat(filepath.Join(dir, "fakepkg", "index.js")); err != nil {
		t.Errorf("the stored install lacks its package: %v", err)
	}
	if _, err := os.Stat(nm); err == nil {
		t.Error("the install is still in the checkout after the move")
	}
	got, ok := Cached(cache, "k1")
	if !ok || got != dir {
		t.Errorf("Cached = %q,%v want %q,true", got, ok, dir)
	}
	if _, ok := Cached(cache, "other"); ok {
		t.Error("Cached found a key nobody stored")
	}
}

// An install with a package resolving outside itself is never stored: the next
// merge would link the mix this cache exists to prevent.
func TestStore_ARefusesAnInstallThatEscapesItself(t *testing.T) {
	cache := t.TempDir()
	root := t.TempDir()
	co, donor := filepath.Join(root, "co"), filepath.Join(root, "donor")
	replaceWithLink(t, lanePkg(t, co, "react"), lanePkg(t, donor, "react"))

	if dir, ok := Store(cache, "k1", filepath.Join(co, "frontend", NodeModules)); ok {
		t.Fatalf("stored an install that escapes itself at %s", dir)
	}
	if _, ok := Cached(cache, "k1"); ok {
		t.Error("Cached serves an install that was refused")
	}
}

// A cached install that has since grown a link out of itself is not served.
func TestCached_RefusesAnEntryThatNowEscapes(t *testing.T) {
	cache := t.TempDir()
	co := nodeRoot(t, true, map[string]string{"package.json": `{}`, "package-lock.json": `{}`})
	dir, ok := Store(cache, "k1", filepath.Join(co, NodeModules))
	if !ok {
		t.Fatal("not stored")
	}
	outside := nodeRoot(t, true, nil)
	replaceWithLink(t, filepath.Join(dir, "fakepkg"), filepath.Join(outside, NodeModules, "fakepkg"))

	if _, ok := Cached(cache, "k1"); ok {
		t.Error("Cached served an entry whose package now resolves outside it")
	}
}

// Entries unused for longer than the age go; a recently used one stays.
func TestSweep_RemovesOnlyEntriesUnusedPastTheAge(t *testing.T) {
	cache := t.TempDir()
	for _, k := range []string{"old", "fresh"} {
		co := nodeRoot(t, true, nil)
		if _, ok := Store(cache, k, filepath.Join(co, NodeModules)); !ok {
			t.Fatalf("%s not stored", k)
		}
	}
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(cache, "old"), old, old); err != nil {
		t.Fatal(err)
	}

	Sweep(cache, 14*24*time.Hour, now)

	if _, ok := Cached(cache, "old"); ok {
		t.Error("an entry unused for 30 days survived")
	}
	if _, ok := Cached(cache, "fresh"); !ok {
		t.Error("a fresh entry was swept")
	}
}
