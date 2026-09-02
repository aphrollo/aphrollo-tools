package tdd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// mergeRejectedPrefix names the marker family under stateDir: one file per
// repo, key-first so a directory listing groups every merge-rejected marker
// together.
const mergeRejectedPrefix = "merge-rejected."

// repoStateKey is the stable short key naming repoRoot in a per-repo state
// file name: sha256/8 hex of the cleaned absolute path, case-folded on
// Windows -- the same shape targetDirKey (buildslots.go) and projectKey
// (deferred.go) already use for a per-target/per-project state file, so this
// is a third instance of one hashing convention rather than a new one.
func repoStateKey(repoRoot string) string {
	clean := filepath.Clean(repoRoot)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	sum := sha256.Sum256([]byte(clean))
	return hex.EncodeToString(sum[:8])
}

// MergeRejectedMarkerPath is where a rejected `git merge` (the pre-merge-
// commit gate blocking an automatic, conflict-free merge) leaves its
// marker -- one file per repo. Git leaves MERGE_HEAD and the merged index in
// place on a rejection ("Not committing merge; use 'git commit' to complete
// the merge."), which then refuses every OTHER session sharing the checkout
// until a human runs `git merge --abort`; the git-queue shim reads this
// marker to recognise that IT caused the rejection and clean up
// automatically. "" when there is no state dir or no repoRoot to key on.
func MergeRejectedMarkerPath(repoRoot string) string {
	dir := stateDir()
	if dir == "" || repoRoot == "" {
		return ""
	}
	return filepath.Join(dir, mergeRejectedPrefix+repoStateKey(repoRoot))
}

// WriteMergeRejectedMarker records that the premergecommit gate blocked a
// merge in repoRoot: the unix time (so a reader can judge freshness) and the
// rejection's first line (for a human inspecting the marker directly -- the
// git-queue shim itself only checks existence and mtime). Best-effort: a
// write failure only loses the automatic cleanup, never the gate's own
// verdict, which has already been decided by the time this is called.
func WriteMergeRejectedMarker(repoRoot, message string) {
	path := MergeRejectedMarkerPath(repoRoot)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	body := strconv.FormatInt(time.Now().Unix(), 10) + "\n" + firstLine(message) + "\n"
	_ = os.WriteFile(path, []byte(body), 0o600)
}

// firstLine returns s up to (excluding) its first newline.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
