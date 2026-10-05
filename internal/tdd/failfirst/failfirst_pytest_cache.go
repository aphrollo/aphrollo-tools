package failfirst

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Every edit of a pytest root resolves its interpreter, and each resolution
// used to start `python -c "import pytest"` (about a second on a cold box).
// The answer only changes when an interpreter or its virtualenv does, so it is
// remembered per project root, stamped with every candidate interpreter and
// its venv directory's modification time: a venv built, rebuilt or removed
// changes the stamp and the next resolution probes again.

// interpCacheMax bounds the cache file; past it, entries are dropped.
const interpCacheMax = 200

type interpEntry struct {
	Python string `json:"python"`
	Stamp  string `json:"stamp"`
}

type interpCacheFile struct {
	Entries map[string]interpEntry `json:"entries"`
}

func interpCachePath(stateDir string) string {
	if stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, "pytest-interpreters.json")
}

// candidateStamp identifies the candidates' state: for each its path, size and
// modification time, and its venv directory's modification time.
func candidateStamp(candidates []string) string {
	var b strings.Builder
	for _, p := range candidates {
		b.WriteString(p)
		if fi, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "|%d|%d", fi.Size(), fi.ModTime().UnixNano())
		}
		if fi, err := os.Stat(filepath.Dir(filepath.Dir(p))); err == nil {
			fmt.Fprintf(&b, "|%d", fi.ModTime().UnixNano())
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func loadInterpCache(path string) interpCacheFile {
	c := interpCacheFile{Entries: map[string]interpEntry{}}
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &c) != nil || c.Entries == nil {
			c = interpCacheFile{Entries: map[string]interpEntry{}}
		}
	}
	return c
}

// storeInterp records python as the answer for key, by rename so a reader
// never sees a half-written file. A failure only means the next edit probes.
func storeInterp(path, key string, e interpEntry) {
	c := loadInterpCache(path)
	c.Entries[key] = e
	for k := range c.Entries {
		if len(c.Entries) <= interpCacheMax {
			break
		}
		if k != key {
			delete(c.Entries, k)
		}
	}
	data, err := json.Marshal(c)
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "pytest-interpreters-*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// pytestCachedRunner is pytestProofRunner that skips the import probe when
// stateDir holds the answer for the same root and the same candidate state. A
// refusal is never cached: the user is about to fix it.
func pytestCachedRunner(stateDir string, search pytestSearch, r Runner, look func(string) (string, error), importable func(string) error) (Runner, string) {
	path := interpCachePath(stateDir)
	if r.Cmd != "pytest" || path == "" {
		return pytestProofRunner(search, r, look, importable)
	}
	candidates := pytestCandidates(search, look)
	stamp := candidateStamp(candidates)
	if e, ok := loadInterpCache(path).Entries[search.root]; ok && e.Stamp == stamp && slices.Contains(candidates, e.Python) {
		return Runner{Cmd: e.Python, Args: append([]string{"-m", "pytest"}, r.Args...), Dir: r.Dir}, ""
	}
	got, why := pytestProofRunner(search, r, look, importable)
	if why == "" {
		storeInterp(path, search.root, interpEntry{Python: got.Cmd, Stamp: stamp})
	}
	return got, why
}
