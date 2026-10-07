// Package userbin is the user-space install of the aphrollo binary: a
// versioned directory per release under a root the account owns, a `current`
// pointer naming the one hooks run, and the shell text that resolves it.
//
// Nothing here needs root and nothing here downloads. The root-owned install
// path stays what it was, a read-only fallback a hook tries second. State
// (the event log, gate state, caches) never lives under the root: a prune of
// old versions removes binaries and nothing else.
package userbin

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
)

// BinName is the file name of the binary inside a version directory.
const BinName = "aphrollo"

// pointerName is the file under the root that holds the current version. A
// pointer file rather than a symlink on every platform: Windows cannot always
// make a symlink without privilege, and one swap path is one to test.
const pointerName = "current"

// DefaultKeep is how many versions an update leaves on disk.
const DefaultKeep = 3

// Sources Resolve reports.
const (
	SourceUser     = "user-space"
	SourceFallback = "fallback"
	SourceNone     = ""
)

// removeAllFn indirects os.RemoveAll so a test can say the platform refuses to
// delete a running image.
var removeAllFn = os.RemoveAll

// Root is the account's user-space bin directory.
func Root() (string, error) { return userRoot() }

// BinaryPath is the binary of one version under root.
func BinaryPath(root, version string) string {
	return filepath.Join(root, version, BinName+ExeSuffix)
}

// Install places staged at <root>/<version>/aphrollo, beside every other
// version and never over a running one: a new version is a new directory. The
// same version again replaces its own file, or moves the old copy aside where
// the platform will not let a running image be replaced. It does not move the
// pointer; SetCurrent does, after the caller has proven the binary.
func Install(root, version, staged string) (string, error) {
	if version == "" || strings.ContainsAny(version, `/\`) || version == "." || version == ".." {
		return "", fmt.Errorf("%q is not a version directory name", version)
	}
	dst := BinaryPath(root, version)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(dst); err == nil {
		if err := os.Remove(dst); err != nil {
			aside := dst + ".old"
			_ = os.Remove(aside)
			if err := os.Rename(dst, aside); err != nil {
				return "", fmt.Errorf("%s is in use and cannot be moved aside: %w", dst, err)
			}
		}
	}
	if err := os.Rename(staged, dst); err != nil {
		// A different volume: copy, then drop the source.
		if cerr := copyExecutable(staged, dst); cerr != nil {
			return "", fmt.Errorf("moving %s into place: %w", staged, errors.Join(err, cerr))
		}
		_ = os.Remove(staged)
	}
	return dst, nil
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// SetCurrent points root's `current` at version by writing a temp file and
// renaming it over the pointer: a reader sees the old version or the new one,
// never a torn file, and no binary is touched.
func SetCurrent(root, version string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(root, pointerName+".new")
	if err := os.WriteFile(tmp, []byte(version+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(root, pointerName)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Current is the version the pointer names.
func Current(root string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(root, pointerName))
	if err != nil {
		return "", false
	}
	v := strings.TrimSpace(string(b))
	return v, v != ""
}

// Versions lists the installed versions, oldest first by version number. A
// directory that is not a release number sorts before them, by name.
func Versions(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(BinaryPath(root, e.Name())); err != nil {
			continue
		}
		out = append(out, e.Name())
	}
	slices.SortFunc(out, compareVersionNames)
	return out
}

func compareVersionNames(a, b string) int {
	va, ea := compat.ParseVersion(a)
	vb, eb := compat.ParseVersion(b)
	switch {
	case ea != nil && eb != nil:
		return cmp.Compare(a, b)
	case ea != nil:
		return -1
	case eb != nil:
		return 1
	case va.Less(vb):
		return -1
	case vb.Less(va):
		return 1
	}
	return 0
}

// Prune removes every version but the newest keep, and always keeps the
// current one, so a switch back with --to never loses the version it runs. A
// version the platform will not let go of (a running image on Windows) is
// reported held, never an error: the next update reclaims it.
func Prune(root string, keep int) (removed, held []string) {
	versions := Versions(root)
	current, _ := Current(root)
	for i, v := range versions {
		if i >= len(versions)-keep || v == current {
			continue
		}
		if err := removeAllFn(filepath.Join(root, v)); err != nil {
			held = append(held, v)
			continue
		}
		removed = append(removed, v)
	}
	return removed, held
}

// Resolve is the binary a hook would run: the user-space current when its
// file exists, else fallback when that exists, else none.
func Resolve(root, fallback string) (path, source string) {
	if v, ok := Current(root); ok {
		if p := BinaryPath(root, v); isFile(p) {
			return p, SourceUser
		}
	}
	if fallback != "" && isFile(fallback) {
		return fallback, SourceFallback
	}
	return "", SourceNone
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// Under reports whether path lies inside root.
func Under(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}
