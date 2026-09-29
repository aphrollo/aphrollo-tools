package ratchet

import (
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
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return nil, fmt.Errorf("go list -deps -json ./... in %s: %w: %s", root, err, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("go list -deps -json ./... in %s: %w", root, err)
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
	fingerprint, ok := goGraphFingerprint(root, overlay)
	if !ok {
		return goListStream(root, overlay)
	}
	path := filepath.Join(cacheDir, "ratchet-cache", "golist-"+cacheKey(root)+".json")
	if data, err := os.ReadFile(path); err == nil {
		if head, body, found := bytes.Cut(data, []byte("\n")); found && string(head) == fingerprint {
			return body, nil
		}
	}
	out, err := goListStream(root, overlay)
	if err != nil {
		return nil, err
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		if tmp, err := os.CreateTemp(filepath.Dir(path), "golist-*.tmp"); err == nil {
			_, werr := tmp.Write(append([]byte(fingerprint+"\n"), out...))
			cerr := tmp.Close()
			if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
				os.Remove(tmp.Name())
			}
		}
	}
	return out, nil
}

var localReplace = regexp.MustCompile(`=>\s*["]?(\.|/)`)

// goGraphFingerprint hashes every input `go list` reads for the module at
// root: each .go source, go.mod, go.sum, go.work, vendor/modules.txt (by
// content, never by mtime), the overlay, the go binary and the environment
// that steers resolution. ok is false when an input lies outside root — a
// go.work in root or above it, or a local replace directive.
func goGraphFingerprint(root string, overlay map[string]string) (string, bool) {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
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
			name != "go.work.sum" && rel != "vendor/modules.txt" {
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
