package mutation

import (
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// GCKind is which category proposed a candidate. It decides how the sweep
// may delete it, not whether: an incremental cache belongs to the target dir
// a build could be using RIGHT NOW, so it is swept only under a build slot.
// The zero value means "no special handling".
type GCKind int

const (
	GCKindOther GCKind = iota
	GCKindIncremental
	GCKindGateDir
	GCKindOrphanWorktree
	GCKindTempLitter
	GCKindMutants
	GCKindMutantsTarget
	GCKindMutantsTemp
	GCKindDepsMember
	GCKindDepsThirdParty
	GCKindStrayTarget
	GCKindGoTmp
	GCKindGatePRMerge
)

// GCCandidate is one reclaimable directory: what it is, how big, why it
// qualifies, and which category proposed it. Reason is written for a human
// reading the table, not parsed.
type GCCandidate struct {
	Path   string
	Size   int64
	Reason string
	Kind   GCKind
}

// pathKey normalises a path for comparison: case-folded on Windows, where
// one directory routinely appears as D:\... and d:\..., and a case-SENSITIVE
// compare made a registered worktree read as an orphan build dir.
func pathKey(p string) string {
	clean := filepath.Clean(p)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

// dirNewestAndSize walks a directory once for both facts a candidate needs:
// the most recent FILE modification inside it (is it idle?) and its total
// size (is it worth reclaiming?). Files only: a directory's own mtime moves
// when an entry is added or removed, including by a cleanup that left the
// cache itself untouched, so it says nothing about whether the cache is in
// use. An unreadable entry is skipped -- a permission error somewhere deep
// must not make a whole sweep fail.
func dirNewestAndSize(dir string) (newest time.Time, size int64) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		size += info.Size()
		return nil
	})
	return newest, size
}
