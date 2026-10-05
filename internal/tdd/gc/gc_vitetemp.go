package gc

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Category (p): what a vitest or vite-node run leaves in the OS temp dir. Vite
// writes its SSR transform output (`__vite_ssr_exportName__`) to a directory
// named by a 21-character nanoid, holding client/ and ssr/ folders of
// hash-named files, and removes it never: one box held 3,140 of them, 37.9 GB,
// at about 248 new a day, from the test runs of the consumer repos the gate
// judges.
//
// The shape is the whole identity: a 21-character name from the nanoid
// alphabet, a directory holding nothing but client/ and ssr/, and a hash-named
// file inside. A directory that fits the name only is somebody's. As with the
// scratch sweep, a live process holding it, or any write in the last day, keeps
// it, and a directory of another user is never proposed.

// viteTempMinAge is how long a run's output must have sat untouched.
const viteTempMinAge = 24 * time.Hour

var (
	viteTempNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{21}$`)
	viteHashFileRe = regexp.MustCompile(`^[A-Za-z0-9_-]{12,}$`)
)

func viteTempName(name string) bool { return viteTempNameRe.MatchString(name) }

// viteOutputShaped reports whether dir holds only client/ and ssr/ folders, at
// least one of them, with a hash-named file somewhere inside.
func viteOutputShaped(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return false
	}
	hashed := false
	for _, e := range entries {
		if !e.IsDir() || (e.Name() != "client" && e.Name() != "ssr") {
			return false
		}
		files, err := os.ReadDir(filepath.Join(dir, e.Name()))
		if err != nil {
			return false
		}
		for _, f := range files {
			stem := strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))
			if !f.IsDir() && viteHashFileRe.MatchString(stem) {
				hashed = true
			}
		}
	}
	return hashed
}

// gcViteTemp proposes the vite output directories directly inside dir that no
// live run holds and nothing has written to for a day.
func gcViteTemp(dir string, now time.Time) []GCCandidate {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []GCCandidate
	for _, e := range entries {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 || !viteTempName(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil || !ownedByCurrentUser(info) || !viteOutputShaped(path) {
			continue
		}
		newest, size := dirNewestAndSize(path)
		if newest.IsZero() {
			newest = info.ModTime()
		}
		if held, _ := scratchHeldFn(path); held || now.Sub(newest) < viteTempMinAge {
			continue
		}
		out = append(out, GCCandidate{Path: path, Size: size, Kind: GCKindTempLitter,
			Reason: "vite SSR output of a test run that is over, idle " + formatDays(now.Sub(newest))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
