package ratchet

import (
	"os"
	"time"
)

// cacheTouchAfter is how stale a cache file's time must be before a read
// renews it, so a hot cache is not rewritten on every run.
const cacheTouchAfter = time.Hour

// touchCacheFile dates a cache file that was just read from now. The state
// directory's retention evicts the least recently modified file first, and a
// cache that never changes would otherwise look the coldest. Best effort: a
// file that cannot be touched only ages sooner.
func touchCacheFile(path string) {
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > cacheTouchAfter {
		now := time.Now()
		_ = os.Chtimes(path, now, now)
	}
}
