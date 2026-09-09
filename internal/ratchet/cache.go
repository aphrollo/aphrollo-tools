package ratchet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The gate runs `ratchet check` before every commit and every edit, so the
// scan has to cost milliseconds on a tree of thousands of files. It does
// because a file whose size and mtime are unchanged is answered from its
// recorded hits instead of being read. The cache is keyed by the LAWS too: a
// rule that changed must never be answered from verdicts reached under the old
// one, which is the failure mode that would make a stale cache look like a
// clean tree.
// 3: a doc-path-resolves citation is judged against what the commit contains,
// not against the working tree (see docpath_committed.go). The law files did
// not change, so the fingerprint below cannot see it — the version is what
// says the recorded verdicts were reached under the older rule.
const cacheVersion = 3

type cacheEntry struct {
	Size  int64            `json:"size"`
	Mtime int64            `json:"mtime"`
	Hits  map[string][]Hit `json:"hits,omitempty"`
}

type scanCache struct {
	path    string
	Version int                   `json:"version"`
	Laws    string                `json:"laws"`
	Files   map[string]cacheEntry `json:"files"`
	next    map[string]cacheEntry
	dirty   bool
}

// loadCache opens (or starts) the cache for one repo's law set. An empty dir
// disables caching entirely — every lookup misses and nothing is written.
func loadCache(dir, root string, laws []Law) *scanCache {
	c := &scanCache{Version: cacheVersion, Files: map[string]cacheEntry{}, next: map[string]cacheEntry{}}
	if dir == "" {
		return c
	}
	c.Laws = lawsFingerprint(laws)
	c.path = filepath.Join(dir, "ratchet-cache", cacheKey(root)+".json")
	data, err := os.ReadFile(c.path)
	if err != nil {
		return c
	}
	var onDisk scanCache
	if err := json.Unmarshal(data, &onDisk); err != nil {
		return c
	}
	if onDisk.Version != cacheVersion || onDisk.Laws != c.Laws {
		return c
	}
	if onDisk.Files != nil {
		c.Files = onDisk.Files
	}
	return c
}

// lookup answers one file's hits when its size and mtime are unchanged.
func (c *scanCache) lookup(root, rel string) (map[string][]Hit, bool) {
	if c.path == "" {
		return nil, false
	}
	fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, false
	}
	e, ok := c.Files[rel]
	if !ok || e.Size != fi.Size() || e.Mtime != fi.ModTime().UnixNano() {
		return nil, false
	}
	c.next[rel] = e
	return e.Hits, true
}

// store records one freshly-scanned file's hits.
func (c *scanCache) store(root, rel string, hits map[string][]Hit) {
	if c.path == "" {
		return
	}
	fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	c.next[rel] = cacheEntry{Size: fi.Size(), Mtime: fi.ModTime().UnixNano(), Hits: hits}
	c.dirty = true
}

// save writes the cache, dropping entries for files this run never saw so a
// deleted file's verdicts cannot outlive it. Best-effort: a cache that cannot
// be written costs speed, never correctness.
func (c *scanCache) save() {
	if c.path == "" || (!c.dirty && len(c.next) == len(c.Files)) {
		return
	}
	c.Files = c.next
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, c.path); err != nil {
		os.Remove(tmp)
	}
}

// lawsFingerprint hashes every law's source text, so any edit to any law
// invalidates the whole cache.
func lawsFingerprint(laws []Law) string {
	h := sha256.New()
	for _, l := range laws {
		h.Write([]byte(l.Name))
		h.Write([]byte{0})
		h.Write([]byte(l.Source))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// cacheKey names one repo's cache file: a readable stem plus a hash, so two
// checkouts of the same repo never share an entry.
func cacheKey(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.ToSlash(abs))))
	return filepath.Base(abs) + "-" + hex.EncodeToString(sum[:])[:12]
}
