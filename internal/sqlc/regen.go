package sqlc

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// sqlcBin resolves the sqlc binary: APHROLLO_SQLC_BIN, then $PATH, then the
// operator's go-install location. Returns "" when none is found — mirrors
// workspace.gooseBin so the external dependency is overridable in tests/CI.
func sqlcBin() string {
	if b := os.Getenv("APHROLLO_SQLC_BIN"); b != "" {
		return b
	}
	if p, err := exec.LookPath("sqlc"); err == nil {
		return p
	}
	const fallback = "/home/debian/go/bin/sqlc"
	if fileExists(fallback) {
		return fallback
	}
	return ""
}

// Regenerate runs a clean `sqlc generate` for cfg WITHOUT touching the committed
// tree: it copies just the config and its query/schema inputs into a temp dir,
// generates there, and returns the generated files keyed by path relative to the
// repo root (so the keys line up with the committed tree for diffing). It never
// writes into cfg.Repo.
func Regenerate(cfg Config) (map[string]string, error) {
	bin := sqlcBin()
	if bin == "" {
		return nil, fmt.Errorf("sqlc not found (set APHROLLO_SQLC_BIN or add sqlc to PATH)")
	}
	tmp, err := os.MkdirTemp("", "aphrollo-sqlc-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	// Copy the config under its base name (sqlc resolves paths relative to the
	// config's dir, which is the temp root here).
	if err := copyPath(cfg.Path, filepath.Join(tmp, cfg.Name)); err != nil {
		return nil, fmt.Errorf("copying config: %w", err)
	}
	// Copy each entry's query + schema inputs, preserving their repo-relative
	// paths. Dedup so two entries sharing a schema dir copy it once.
	copied := map[string]bool{}
	for _, e := range cfg.Entries {
		for _, rel := range []string{e.Queries, e.Schema} {
			if rel == "" || copied[rel] {
				continue
			}
			copied[rel] = true
			if err := copyPath(filepath.Join(cfg.Repo, rel), filepath.Join(tmp, rel)); err != nil {
				return nil, fmt.Errorf("copying %s: %w", rel, err)
			}
		}
	}

	cmd := exec.Command(bin, "-f", cfg.Name, "generate")
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("sqlc generate (%s): %w\n%s", cfg.Name, err, out)
	}

	gen := map[string]string{}
	for _, e := range cfg.Entries {
		dir := filepath.Join(tmp, e.Out)
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(tmp, path)
			if err != nil {
				return err
			}
			gen[filepath.ToSlash(rel)] = string(data)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("reading generated %s: %w", e.Out, err)
		}
	}
	return gen, nil
}

// committedFiles reads the files currently on disk under each entry's out dir,
// keyed the same way as Regenerate's output (repo-relative, forward slashes), so
// the two maps diff directly.
func committedFiles(cfg Config) (map[string]string, error) {
	out := map[string]string{}
	for _, e := range cfg.Entries {
		dir := filepath.Join(cfg.Repo, e.Out)
		if !dirExists(dir) {
			continue // never generated yet — every regen file reads as "added"
		}
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(cfg.Repo, path)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = string(data)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// sortedKeys returns the union of map keys, sorted — for deterministic output.
func sortedKeys(maps ...map[string]string) []string {
	seen := map[string]bool{}
	for _, m := range maps {
		for k := range m {
			seen[k] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// copyPath copies a file or directory tree from src to dst, creating parent
// dirs. Symlinks are followed as their target kind.
func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyTree(src, dst)
	}
	return copyFile(src, dst)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// trimSpaceLines is a small helper used by renderers to avoid emitting a
// trailing blank line block; kept here so check/scoped share it.
func trimTrailingNewlines(s string) string {
	return strings.TrimRight(s, "\n")
}
