package mutation

import (
	"context"
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

// The keep of the selection index (mutants_select_index.go). An index is
// valid for exactly the content it was measured on, so its key is that
// content: the toolchain and build settings, the tag set, the mutation
// environment, the module files, the packages measured, and every file of the
// packages whose tests and code the measurement ran (their sources, tests,
// testdata and embeds). Nothing is kept ahead of a run that needs it, and an
// index is never rebuilt while its key still names a kept one. It lives beside
// the commit stage's coverage store and is trimmed with it.

// selKey is the key of the index of one tag set: targets are the package
// directories under mutation (what -coverpkg names), dirs every directory
// whose files the measurement reads.
func selKey(ctx context.Context, root string, cfg MutantsConfig, tags, targets, dirs []string) (string, error) {
	return selKeyFrom(ctx, root, cfg, tags, targets, selContentHashFn(root, dirs))
}

// selContentHashFn is the seam the tests count the hashing of files through.
var selContentHashFn = selContentHash

// selContentHash is the part of a key that is the files: the module files and
// every file of the package directories dirs. It is the costly part, so a caller
// that keys several targets over the same dirs makes it once.
func selContentHash(root string, dirs []string) string {
	h := sha256.New()
	for _, name := range []string{"go.mod", "go.sum", "go.work", "go.work.sum"} {
		hashFile(h, filepath.Join(root, name), name)
	}
	sorted := slices.Clone(dirs)
	slices.Sort(sorted)
	for _, dir := range slices.Compact(sorted) {
		fmt.Fprintf(h, "dir %s\n", dir)
		hashPackageDir(h, root, dir)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// selKeyFrom keys the targets over a content hash made by selContentHash.
func selKeyFrom(ctx context.Context, root string, cfg MutantsConfig, tags, targets []string, content string) (string, error) {
	env, err := goEnvFn(ctx, root)
	if err != nil {
		return "", fmt.Errorf("go env: %w", err)
	}
	h := sha256.New()
	fmt.Fprintf(h, "select schema %d\nmodule %s\nenv %s\ntags %s\nmutants-env %s\ntargets %s\ncontent %s\n",
		selSchema, modulePath(root), strings.TrimSpace(env), strings.Join(tags, ","), strings.Join(cfg.Env, ";"), strings.Join(targets, ","), content)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashPackageDir hashes every file directly in the package directory, its
// testdata recursively, and every subdirectory that holds no Go file (what a
// //go:embed may name): what a test of the package can read.
func hashPackageDir(h io.Writer, root, dir string) {
	base := filepath.Join(root, filepath.FromSlash(dir))
	entries, err := os.ReadDir(base)
	if err != nil {
		fmt.Fprintf(h, "unreadable %s\n", dir)
		return
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			hashFile(h, filepath.Join(base, e.Name()), dir+"/"+e.Name())
		}
	}
	hashTree := func(p string, skipPackages bool) {
		_ = filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // absence-ok: a package with no such directory has none to hash
			}
			if d.IsDir() {
				if skipPackages && q != p && selHoldsGo(q) {
					return filepath.SkipDir // another package, not an embed
				}
				return nil
			}
			if d.Type().IsRegular() {
				rel, _ := filepath.Rel(root, q)
				hashFile(h, q, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	hashTree(filepath.Join(base, "testdata"), false)
	for _, e := range entries {
		if e.IsDir() && e.Name() != "testdata" && !selHoldsGo(filepath.Join(base, e.Name())) {
			hashTree(filepath.Join(base, e.Name()), true)
		}
	}
}

// selHoldsGo reports whether the directory has a Go file directly in it.
func selHoldsGo(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

// selFileHash is the hash of one module-relative file as it is on disk, "" when
// it cannot be read.
func selFileHash(root, rel string) string {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		// absence-ok: an unreadable file hashes to nothing, which never equals a kept hash
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// selStorePath is the file the index of one key is kept in, "" when there is no
// directory to keep it in.
func selStorePath(root, key string) string {
	base := CoverCacheDir(root)
	if base == "" {
		return ""
	}
	return filepath.Join(base, "select-v"+fmt.Sprint(selSchema)+"-"+key[:min(len(key), 16)]+".json")
}

// loadSelIndex is the kept index of a key, nil when there is none: an index of
// another schema or key, or one that does not parse, is no index.
func loadSelIndex(root, key string) *selIndex {
	path := selStorePath(root, key)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// absence-ok: no kept index is the cold case, which builds one
		return nil
	}
	var idx selIndex
	if json.Unmarshal(data, &idx) != nil || idx.Schema != selSchema || idx.Key != key {
		return nil
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return &idx
}

// save keeps the index, then trims the directory to its bounds.
func (x *selIndex) save(root string) error {
	path := selStorePath(root, x.Key)
	if path == "" {
		return errors.New("no git directory to keep the coverage in")
	}
	data, err := json.Marshal(x)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".select-*")
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

// stale says why the index cannot speak for the file as it is now: the file is
// not one it measured, or it is not the content it measured. "" is a file the
// index's lines hold for.
func (x *selIndex) stale(root, file string) string {
	want, ok := x.FileHash[file]
	if !ok {
		return file + " is not a file this coverage measured"
	}
	if selFileHash(root, file) != want {
		return file + " is not the content this coverage measured, so its lines moved"
	}
	return ""
}

// selCost is what one package's tests and one build of them were measured to
// cost, kept by the same content key as coverage: the decision to select is
// made once for the content, not on every run.
type selCost struct {
	Schema int    `json:"schema"`
	Key    string `json:"key"`
	// Build and Run are nanoseconds: one rebuild of the package's test binary
	// as a mutant forces it, and one run of the whole suite.
	Build int64 `json:"build"`
	Run   int64 `json:"run"`
	Cheap bool  `json:"cheap"`
}

// selCostPath is the file the cost of one key is kept in.
func selCostPath(root, key string) string {
	base := CoverCacheDir(root)
	if base == "" {
		return ""
	}
	return filepath.Join(base, "select-cost-v"+fmt.Sprint(selSchema)+"-"+key[:min(len(key), 16)]+".json")
}

// loadSelCost is the kept cost of a key, nil when there is none.
func loadSelCost(root, key string) *selCost {
	path := selCostPath(root, key)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// absence-ok: no kept cost is the cold case, which measures it
		return nil
	}
	var c selCost
	if json.Unmarshal(data, &c) != nil || c.Schema != selSchema || c.Key != key {
		return nil
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return &c
}

// save keeps the cost, then trims the directory to its bounds.
func (c *selCost) save(root string) error {
	path := selCostPath(root, c.Key)
	if path == "" {
		return errors.New("no git directory to keep the cost in")
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	trimCoverCache(filepath.Dir(path), coverCacheMaxEntries, coverCacheMaxBytes)
	return nil
}
