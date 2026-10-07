package mutation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// testMapPath is the file the map of one package at one content hash is kept
// in, "" when there is no directory to keep it in.
func testMapPath(root, dir, hash string) string {
	base := CoverCacheDir(root)
	if base == "" {
		return ""
	}
	slug := strings.ReplaceAll(filepath.ToSlash(dir), "/", "__")
	if slug == "." {
		slug = "_root"
	}
	return filepath.Join(base, slug+"-"+hash[:min(len(hash), 16)]+".json")
}

// saveTestMap keeps a map under its package and hash, then trims the
// directory to its bounds.
func saveTestMap(root string, m testMap) error {
	path := testMapPath(root, m.Package, m.Hash)
	if path == "" {
		return errors.New("no git directory to keep the test map in")
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".testmap-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	trimCoverCache(filepath.Dir(path), coverCacheMaxEntries, coverCacheMaxBytes)
	return nil
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

// loadTestMap reads the map of a package at one content hash. A map of
// another schema or hash, one that does not parse, or one whose indexes do not
// fit its test list is no map. A map that is used is marked read, so what is
// trimmed and swept is what nothing has used lately.
func loadTestMap(root, dir, hash string) (*testMap, bool) {
	path := testMapPath(root, dir, hash)
	if path == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// absence-ok: no kept map is the cold-cache case, which measures the package's coverage
		return nil, false
	}
	var m testMap
	if json.Unmarshal(data, &m) != nil || m.Schema != testMapSchema || m.Hash != hash || m.Package != dir {
		return nil, false
	}
	for _, b := range m.Blocks {
		for _, i := range b.Tests {
			if i < 0 || i >= len(m.Tests) {
				return nil, false
			}
		}
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return &m, true
}

// hashPackage is the hash of a package's build inputs, from the `go list
// -deps -test` listing of it (one `dir|GoFiles|TestGoFiles|XTestGoFiles|
// EmbedFiles` line per package, standard library left out). A package inside
// root is hashed by the content of each listed file; one outside it, which is
// a module dependency whose directory names its version, by its directory
// alone. The order of the listing does not matter.
func hashPackage(root, listing string) string {
	var lines []string
	for line := range strings.SplitSeq(listing, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	slices.Sort(lines)
	lines = slices.Compact(lines)
	h := sha256.New()
	for _, line := range lines {
		parts := strings.Split(line, "|")
		dir := parts[0]
		fmt.Fprintf(h, "package %s\n", dir)
		if rel, err := filepath.Rel(root, dir); err != nil || !filepath.IsLocal(rel) {
			// A dependency outside the module is one of two things: a module
			// the module cache holds, whose directory names its version and
			// never changes, or a replacement directory on disk, which does
			// change and is read by content like the module's own.
			if inModuleCache(dir) {
				continue
			}
		}
		for _, group := range parts[1:] {
			for _, name := range strings.Split(group, ",") {
				if name != "" {
					hashFile(h, filepath.Join(dir, name), name)
				}
			}
		}
		// A test reads its fixtures from testdata, which `go list` does not name.
		hashTree(h, filepath.Join(dir, "testdata"))
	}
	return hex.EncodeToString(h.Sum(nil))
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

// inModuleCache reports whether dir is inside the Go module cache, which holds
// each module version in a directory that is never edited.
func inModuleCache(dir string) bool {
	return strings.Contains(filepath.ToSlash(dir), "/pkg/mod/")
}

// hashTree adds every file below dir, by relative path and content, in path
// order; a dir that is not there adds nothing.
func hashTree(h io.Writer, dir string) {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	slices.Sort(files)
	for _, path := range files {
		rel, _ := filepath.Rel(dir, path)
		hashFile(h, path, "testdata/"+filepath.ToSlash(rel))
	}
}
