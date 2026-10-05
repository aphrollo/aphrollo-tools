package gc

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// Category (o): the Go build cache. `go` trims entries it has not used for
// five days and puts no cap on the total, and the cache of a box that builds
// many lanes reached 218 GB on one machine and 53 GB on another. The sweep
// bounds it: while the entries hold more than the cap, the oldest-used ones
// go, none used within the age bar.
//
// It is NOT a candidate list. A candidate is a directory the sweep removes
// whole, and the cache directory must never be one; this trim removes
// individual files only, two levels down (<cache>/<xx>/<hash>-a and -d),
// leaving every directory and the cache's own README and trim.txt where they
// are. A build running at the same time is safe: the go tool re-creates a
// missing entry, and a file it holds open is one this trim fails to remove
// and moves past. Go stamps an entry's mtime when it uses it, at most once an
// hour, so a file touched inside the last hour is in use whatever the age
// setting says.

const (
	// DefaultGoCacheCap is the size the cache is trimmed down to.
	DefaultGoCacheCap int64 = 20 << 30
	// DefaultGoCacheAge is how long an entry must have gone unused to be trimmed.
	DefaultGoCacheAge = 12 * time.Hour

	goCacheCapKey = "gocache-cap"
	goCacheAgeKey = "gocache-age"
	// goCacheMinAge is the floor of the age bar: Go refreshes an entry's mtime
	// only once it is an hour old, so anything younger may be in use.
	goCacheMinAge = time.Hour
	// goCacheTrimEvery is the least time between two trims.
	goCacheTrimEvery = 6 * time.Hour
	goCacheStampFile = "gocache-trim-last"
)

// GoCacheSettings are the repo's two knobs for the trim.
type GoCacheSettings struct {
	Cap int64
	Age time.Duration
}

// GoCacheTrim is what one trim did, or in a dry run would do.
type GoCacheTrim struct {
	Dir   string
	Total int64 // bytes the cache entries hold before the trim
	Files int   // entries removed (or proposed)
	Freed int64 // bytes of those
}

// ParseGoCacheCap reads a size such as "20GB", "512 MB" or "1TB".
func ParseGoCacheCap(s string) (int64, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	t = strings.TrimSuffix(t, "B")
	mult := int64(1)
	for suffix, m := range map[string]int64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40} {
		if num, ok := strings.CutSuffix(t, suffix); ok {
			t, mult = num, m
			break
		}
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid size %q (want a size above zero such as 20GB)", s)
	}
	return int64(n * float64(mult)), nil
}

// ReadGoCacheSettings reads gocache-cap and gocache-age from root's
// aphrollo.toml. A malformed value is refused naming its key: a typo taken as
// the default would trim a cache the repo meant to keep, or keep one it meant
// to bound.
func ReadGoCacheSettings(root string) (GoCacheSettings, error) {
	s := GoCacheSettings{Cap: DefaultGoCacheCap, Age: DefaultGoCacheAge}
	if v, set := aphrolloTomlString(root, goCacheCapKey); set {
		n, err := ParseGoCacheCap(v)
		if err != nil {
			return s, fmt.Errorf("%s = %q: %v", goCacheCapKey, v, err)
		}
		s.Cap = n
	}
	if v, set := aphrolloTomlString(root, goCacheAgeKey); set {
		d, err := ParseGCAge(v)
		if err != nil {
			return s, fmt.Errorf("%s = %q: %v", goCacheAgeKey, v, err)
		}
		s.Age = d
	}
	return s, nil
}

// goCacheDirFn answers the cache path; a seam so a test names a fake one.
var goCacheDirFn = goEnvGoCache

func goEnvGoCache() string {
	out, err := gcLightOutput(run.Spec{Name: "go", Args: []string{"env", "GOCACHE"}})
	if err != nil {
		return ""
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" || dir == "off" || !filepath.IsAbs(dir) {
		return ""
	}
	return dir
}

// TrimGoCache trims the cache `go env GOCACHE` names, asked once. apply false
// reports what it would remove.
func TrimGoCache(s GoCacheSettings, apply bool) GoCacheTrim {
	dir := goCacheDirFn()
	if dir == "" {
		return GoCacheTrim{}
	}
	return trimGoCache(dir, s.Cap, s.Age, time.Now(), apply)
}

func isGoCacheShard(name string) bool {
	if len(name) != 2 {
		return false
	}
	_, err := strconv.ParseUint(name, 16, 8)
	return err == nil
}

type goCacheFile struct {
	path string
	size int64
	mod  time.Time
}

func trimGoCache(dir string, capBytes int64, age time.Duration, now time.Time, apply bool) GoCacheTrim {
	res := GoCacheTrim{Dir: dir}
	age = max(age, goCacheMinAge)
	shards, err := os.ReadDir(dir)
	if err != nil {
		return res
	}
	var old []goCacheFile
	for _, sh := range shards {
		if !sh.IsDir() || !isGoCacheShard(sh.Name()) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(dir, sh.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			res.Total += info.Size()
			if now.Sub(info.ModTime()) >= age {
				old = append(old, goCacheFile{filepath.Join(dir, sh.Name(), e.Name()), info.Size(), info.ModTime()})
			}
		}
	}
	if res.Total <= capBytes {
		return res
	}
	sort.Slice(old, func(i, j int) bool {
		if !old[i].mod.Equal(old[j].mod) {
			return old[i].mod.Before(old[j].mod)
		}
		return old[i].path < old[j].path
	})
	left := res.Total
	for _, f := range old {
		if left <= capBytes {
			break
		}
		if apply && os.Remove(f.path) != nil {
			continue // held by a build, or already gone: not ours to force
		}
		left -= f.size
		res.Files++
		res.Freed += f.size
	}
	return res
}

// gocacheTrimDue reports whether six hours have passed since the last trim
// started; an absent stamp is due.
func gocacheTrimDue(now time.Time) bool {
	path := gcStatePath(goCacheStampFile)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err != nil || now.Sub(info.ModTime()) >= goCacheTrimEvery
}

// stampGoCacheTrim records that a trim started, so a trim that dies halfway
// does not re-run at every sweep.
func stampGoCacheTrim(now time.Time) {
	path := gcStatePath(goCacheStampFile)
	if path == "" {
		return
	}
	_ = os.WriteFile(path, []byte(now.UTC().Format(time.RFC3339)), 0o600)
	_ = os.Chtimes(path, now, now)
}

// GoCacheTrimDue and StampGoCacheTrim are the rate limit the detached sweep
// applies before it trims.
func GoCacheTrimDue() bool { return gocacheTrimDue(time.Now()) }

func StampGoCacheTrim() { stampGoCacheTrim(time.Now()) }

// RenderGoCacheTrim is the line a sweep prints for the trim.
func RenderGoCacheTrim(t GoCacheTrim, applied bool) string {
	if t.Dir == "" {
		return ""
	}
	verb := "would remove"
	if applied {
		verb = "removed"
	}
	return fmt.Sprintf("go build cache %s: %s held, %s %d files (%s)\n", t.Dir, formatBytes(t.Total), verb, t.Files, formatBytes(t.Freed))
}

// SetGoCacheDirForTest names a fake cache in place of `go env GOCACHE` and
// returns the restore. A setter, so a test above gc (the verb's) reaches it.
func SetGoCacheDirForTest(dir string) (restore func()) {
	prev := goCacheDirFn
	goCacheDirFn = func() string { return dir }
	return func() { goCacheDirFn = prev }
}
