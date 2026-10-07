package mutation

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Where the test maps live and how one is told to be out of date. A map is
// kept in the repository's shared git directory, so every worktree of the
// repository reads what any of them measured, one file per package and
// content: the name carries the package and the hash of everything its test
// binary is built from (the content of the package's and its dependencies'
// files, the import path, the Go version and the flags), and a file is read
// only when the hash it was asked for matches the one inside it. A map of
// other content is never used, not even meanwhile: lines are what it is keyed
// by, and a changed line is a different line.
//
// The directory is bounded: saving a map keeps the newest coverCacheMaxEntries
// files within coverCacheMaxBytes and removes the rest, and `gate gc` removes
// what has not been read for coverCacheMaxAge.

const (
	// coverCacheMaxEntries and coverCacheMaxBytes bound the kept maps of one
	// repository.
	coverCacheMaxEntries = 64
	coverCacheMaxBytes   = 64 << 20
	// CoverCacheMaxAge is how long a map may go unread before gc removes it.
	CoverCacheMaxAge = 30 * 24 * time.Hour
)

// CoverCacheDir is the directory the repository's maps are kept in, "" when
// the checkout is not in a git repository.
func CoverCacheDir(root string) string {
	repo := RepoRoot(root)
	if repo == "" {
		repo = root
	}
	common := gitCommonDir(repo)
	if common == "" {
		return ""
	}
	return filepath.Join(common, "aphrollo-mutcover")
}

// trimCoverCache removes the least recently read maps of dir until at most
// entries files remain, totalling at most maxBytes. The newest file is always
// kept, whatever its size.
func trimCoverCache(dir string, entries int, maxBytes int64) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type kept struct {
		path string
		mod  time.Time
		size int64
	}
	var all []kept
	for _, f := range files {
		info, err := f.Info()
		if err != nil || f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		all = append(all, kept{filepath.Join(dir, f.Name()), info.ModTime(), info.Size()})
	}
	slices.SortFunc(all, func(a, b kept) int {
		if c := b.mod.Compare(a.mod); c != 0 {
			return c
		}
		return strings.Compare(a.path, b.path)
	})
	var total int64
	for i, f := range all {
		total += f.size
		if i > 0 && (i >= entries || total > maxBytes) {
			_ = os.Remove(f.path)
		}
	}
}

// hashFile adds one file to the hash: its name and content, or its name and
// the fact that it could not be read.
func hashFile(h io.Writer, path, name string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(h, "file %s unreadable\n", name)
		return
	}
	fmt.Fprintf(h, "file %s %d\n", name, len(data))
	_, _ = h.Write(data)
}
