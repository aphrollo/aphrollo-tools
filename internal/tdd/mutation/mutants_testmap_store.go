package mutation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Where the per-function test maps live and how one is told to be out of
// date. A package's map is kept under the gate's state for the repo, one file
// per package. It carries the hash of the package's sources and everything
// its test binary is built from: a map whose hash is not the tree's is
// rebuilt in the background, but is still used meanwhile, because it is keyed
// by function and a function's tests rarely change when its body does.

// testMapPath is the file one package's map is kept in, "" when the gate has
// no state directory for the repo.
func testMapPath(root, dir string) string {
	// Keyed on the repository's primary checkout, not the worktree asking:
	// the map is built where a merge landed and read from every lane.
	repo := primaryCheckoutRoot(root)
	if repo == "" {
		repo = root
	}
	base := mutantsLogDir(repo)
	if base == "" {
		return ""
	}
	slug := strings.ReplaceAll(filepath.ToSlash(dir), "/", "__")
	if slug == "." {
		slug = "_root"
	}
	return filepath.Join(base, "testmap", slug+".json")
}

// saveTestMap keeps a map, replacing the last one for its package whole.
func saveTestMap(root string, m testMap) error {
	path := testMapPath(root, m.Package)
	if path == "" {
		return errors.New("no state directory to keep the test map in")
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
	return os.Rename(tmp.Name(), path)
}

// loadTestMap reads a package's map. A map of another schema, one that does
// not parse, or one whose indexes do not fit its test list is no map.
func loadTestMap(root, dir string) (*testMap, bool) {
	path := testMapPath(root, dir)
	if path == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// absence-ok: no kept map is the cold-cache case, which runs the whole package for each mutant
		return nil, false
	}
	var m testMap
	if json.Unmarshal(data, &m) != nil || m.Schema != testMapSchema {
		return nil, false
	}
	for _, b := range m.Blocks {
		for _, i := range b.Tests {
			if i < 0 || i >= len(m.Tests) {
				return nil, false
			}
		}
	}
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
			continue
		}
		for _, group := range parts[1:] {
			for _, name := range strings.Split(group, ",") {
				if name != "" {
					hashFile(h, filepath.Join(dir, name), name)
				}
			}
		}
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
