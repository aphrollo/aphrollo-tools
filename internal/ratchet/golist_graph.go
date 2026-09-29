package ratchet

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// goOverlayOf keeps the Go sources of a proposed-content map: `go list`
// reads nothing else of an edit, and a go.mod cannot be overlaid.
func goOverlayOf(proposed map[string]string) map[string]string {
	var out map[string]string
	for rel, text := range proposed {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[rel] = text
	}
	return out
}

// goListEnv names the environment variables that change what `go list`
// resolves; the graph cache key carries their values.
var goListEnv = []string{
	"GOFLAGS", "GOOS", "GOARCH", "CGO_ENABLED", "GOEXPERIMENT", "GOTOOLCHAIN",
	"GOMODCACHE", "GOPATH", "GOENV",
}

// goListStream is `go list -deps -json ./...` over root, as the caller's
// proposed sources would leave it: each overlay file is written to a temp
// dir and handed to go through -overlay, so the disk is never touched.
func goListStream(root string, overlay map[string]string) ([]byte, error) {
	args := []string{"list", "-deps", "-json"}
	if len(overlay) > 0 {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		tmp, err := os.MkdirTemp("", "go-list-overlay-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		replace := map[string]string{}
		for i, rel := range sortedKeys(overlay) {
			src := filepath.Join(tmp, fmt.Sprintf("%d.go", i))
			if err := os.WriteFile(src, []byte(overlay[rel]), 0o600); err != nil {
				return nil, err
			}
			replace[filepath.Join(abs, filepath.FromSlash(rel))] = src
		}
		doc, err := json.Marshal(map[string]any{"Replace": replace})
		if err != nil {
			return nil, err
		}
		file := filepath.Join(tmp, "overlay.json")
		if err := os.WriteFile(file, doc, 0o600); err != nil {
			return nil, err
		}
		args = append(args, "-overlay", file)
	}
	cmd := exec.Command("go", append(args, "./...")...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		said := ""
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			said = ": " + strings.TrimSpace(string(exit.Stderr))
		}
		return nil, fmt.Errorf("go list -deps -json ./... in %s: %w%s", root, err, said)
	}
	return out, nil
}

// goListCached is goListStream answered from the tree-state cache when the
// tree has not moved since the last run. A tree whose graph reads files
// outside root is never cached: the fingerprint could not see them move.
func goListCached(root string, overlay map[string]string, cacheDir string) ([]byte, error) {
	if cacheDir == "" {
		return goListStream(root, overlay)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return goListStream(root, overlay)
	}
	fingerprint, ok := goGraphFingerprint(abs, overlay)
	if !ok {
		return goListStream(root, overlay)
	}
	path := filepath.Join(cacheDir, "ratchet-cache", GraphCachePrefix+cacheKey(root)+".json")
	// The header names the root too, so a sweep can tell whose entry it is
	// (GraphCacheRoot): the file name carries only a hash of the path.
	header := fingerprint + " " + abs
	if data, err := os.ReadFile(path); err == nil {
		if head, body, found := bytes.Cut(data, []byte("\n")); found && string(head) == header {
			return body, nil
		}
	}
	out, err := goListStream(root, overlay)
	if err != nil {
		return nil, err
	}
	_ = writeAtomic(path, append([]byte(header+"\n"), out...))
	return out, nil
}

// GraphCachePrefix starts the name of every cached dependency graph.
const GraphCachePrefix = "golist-"

// GraphCacheRoot reads the checkout a cached dependency graph was made for,
// from the first line of the file at path. ok is false for a file that is
// unreadable or carries no root (a header from before roots were recorded).
func GraphCacheRoot(path string) (root string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		// absence-ok: an unreadable or headerless cache names no root
		return "", false
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil {
		// absence-ok: an unreadable or headerless cache names no root
		return "", false
	}
	_, root, found := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
	return root, found && root != ""
}

// writeAtomic replaces path with data through a uniquely named temp file, so
// two runs writing at once never leave a torn entry for a third to read.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "golist-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

var localReplace = regexp.MustCompile(`=>\s*["]?(\.|/)`)

// goGraphFingerprint hashes every input `go list` reads for the module at
// root: each .go source, go.mod, go.sum, vendor/modules.txt (by
// content, never by mtime), the overlay, the go binary and the environment
// that steers resolution. ok is false when an input lies outside root — a
// go.work in root or above it, or a local replace directive.
func goGraphFingerprint(abs string, overlay map[string]string) (string, bool) {
	if w := os.Getenv("GOWORK"); w != "" && w != "off" {
		return "", false
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return "", false
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if mod, err := os.ReadFile(filepath.Join(abs, "go.mod")); err == nil && localReplace.Match(mod) {
		return "", false
	}
	h := sha256.New()
	if goBin, err := exec.LookPath("go"); err == nil {
		if info, err := os.Stat(goBin); err == nil {
			fmt.Fprintf(h, "go|%s|%d|%d\n", goBin, info.Size(), info.ModTime().UnixNano())
		}
	}
	for _, name := range goListEnv {
		fmt.Fprintf(h, "env|%s=%s\n", name, os.Getenv(name))
	}
	walkErr := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != abs && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(abs, p)
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(name, ".go") && name != "go.mod" && name != "go.sum" &&
			rel != "vendor/modules.txt" {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Fprintf(h, "file|%s\n", rel)
		_, err = io.Copy(h, f)
		return err
	})
	if walkErr != nil {
		return "", false
	}
	for _, rel := range sortedKeys(overlay) {
		fmt.Fprintf(h, "overlay|%s|%d\n%s\n", rel, len(overlay[rel]), overlay[rel])
	}
	return hex.EncodeToString(h.Sum(nil)), true
}
