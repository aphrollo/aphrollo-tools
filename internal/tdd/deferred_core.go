package tdd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// normalizeProjectPath is the identity two mentions of the same project
// directory reduce to before either naming a file (projectKey) or comparing
// against another mention of it (sameProject): absolute, cleaned, and
// case-folded on Windows, where one project routinely appears under two
// drive-letter or slash-direction spellings.
func normalizeProjectPath(root string) string {
	clean := filepath.Clean(root)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	return clean
}

// projectKey names a project's files in the deferred dir. Hashed because a
// path is not a filename.
func projectKey(root string) string {
	sum := sha256.Sum256([]byte(normalizeProjectPath(root)))
	return hex.EncodeToString(sum[:8])
}

const (
	renameAttempts   = 100
	renameRetryEvery = 5 * time.Millisecond
)

// writeFileAtomic publishes a file by rename, so a reader polling for it
// sees either the old content or the whole new content — never the middle of
// a write. The harvest polls the result file every 200 ms; a truncated read
// there reads as "still running".
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	// Windows refuses to replace a file another process has open, and the
	// harvest polls this very path — so retry briefly before giving up.
	var rerr error
	for range renameAttempts {
		if rerr = os.Rename(name, path); rerr == nil {
			return nil
		}
		time.Sleep(renameRetryEvery)
	}
	os.Remove(name)
	return rerr
}
