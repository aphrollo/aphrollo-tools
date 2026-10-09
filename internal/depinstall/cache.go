package depinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A merge checkout whose lane's install cannot be shared installs for itself.
// That install is keyed by what pins it, not by the lane that happened to be
// merged: the same lockfile and manifest give the same node_modules, so a
// later merge links the earlier one's instead of installing again. Each entry
// is <cacheRoot>/<key>/node_modules.

// CacheKey is the content key of the install r makes in root: a hash of the
// lockfile and the manifest. ok is false when nothing pins the install (a bare
// package.json), so no two runs are known to produce the same tree.
func CacheKey(r Rule, root string) (string, bool) {
	if !r.IsNode() || r.Marker == "package.json" {
		return "", false
	}
	lock, err := os.ReadFile(filepath.Join(root, r.Marker))
	if err != nil {
		return "", false // absence-ok: no lockfile to read means nothing pins the install, so it has no key
	}
	manifest, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return "", false // absence-ok: no manifest to read means nothing pins the install, so it has no key
	}
	h := sha256.New()
	for _, part := range [][]byte{[]byte(r.Marker), lock, manifest} {
		h.Write([]byte{byte(len(part) >> 24), byte(len(part) >> 16), byte(len(part) >> 8), byte(len(part))})
		h.Write(part)
	}
	return hex.EncodeToString(h.Sum(nil))[:32], true
}

// Cached is the stored node_modules for key, when there is one and it is still
// self-contained: every package of it resolves inside it. A use refreshes the
// entry's age.
func Cached(cacheRoot, key string) (string, bool) {
	entry := filepath.Join(cacheRoot, key)
	nm := filepath.Join(entry, NodeModules)
	fi, err := os.Lstat(nm)
	if err != nil || !fi.IsDir() || len(Escapes(nm, nm)) != 0 {
		return "", false
	}
	now := time.Now()
	_ = os.Chtimes(entry, now, now)
	return nm, true
}

// Store moves the install at nodeModules into the cache under key and returns
// where it now is. It refuses (ok false, the install left where it is) one that
// is not a real directory, that resolves outside itself (a workspace link, a
// donor), or whose key is already stored: a cache of an install that mixes
// trees would hand every later merge the mix. Rename is atomic on one volume,
// so two merges storing the same key leave one whole entry.
func Store(cacheRoot, key, nodeModules string) (string, bool) {
	fi, err := os.Lstat(nodeModules)
	if err != nil || !fi.IsDir() || len(Escapes(nodeModules, nodeModules)) != 0 {
		return "", false
	}
	entry := filepath.Join(cacheRoot, key)
	if err := os.MkdirAll(entry, 0o755); err != nil {
		return "", false
	}
	dest := filepath.Join(entry, NodeModules)
	if err := os.Rename(nodeModules, dest); err != nil {
		return "", false
	}
	return dest, true
}

// Sweep removes the entries nobody has used since maxAge before now. Entries
// are links-safe: RemoveTree unlinks before it deletes.
func Sweep(cacheRoot string, maxAge time.Duration, now time.Time) {
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		fi, err := e.Info()
		if err != nil || now.Sub(fi.ModTime()) <= maxAge {
			continue
		}
		_ = RemoveTree(filepath.Join(cacheRoot, e.Name()))
	}
}
