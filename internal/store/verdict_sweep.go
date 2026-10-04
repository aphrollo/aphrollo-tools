package store

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SweepOptions configure SweepVerdicts. The zero value is the architecture's
// retention: 14 days, 2,000 files, the clock, and no lane ever closed.
type SweepOptions struct {
	// Now is the time ages are counted to; zero is the clock.
	Now time.Time
	// MaxAge is how long an unused file is kept; zero is VerdictMaxAge.
	MaxAge time.Duration
	// Cap is the most files kept; zero is VerdictCap.
	Cap int
	// Closed says a lane is closed. A file that names lanes, all closed, goes
	// before its time; a file naming none, or an unreadable one, never does.
	Closed func(lane string) bool
	// Dry plans the sweep and removes nothing: the result counts what it would.
	Dry bool
	// Removed, when set, is told of each file removed, after it was (or, when
	// Dry, to be removed) with its size and the reason.
	Removed func(path string, size int64, reason string)
}

// SweepResult counts what a sweep did: Expired files past MaxAge, Closed files
// of closed lanes, Evicted files over the cap (least recently used first), and
// the Kept files that remain.
type SweepResult struct {
	Expired, Closed, Evicted, Kept int
}

// SweepVerdicts applies the retention rules under the verdicts lock, so it never
// removes a file a writer is replacing. It also clears the temp files an
// interrupted write left more than an hour ago. A file it cannot remove stays
// (and counts as kept) and is reported in the error after the rest are done.
func (s *Store) SweepVerdicts(opts SweepOptions) (SweepResult, error) {
	now, maxAge, limit := opts.Now, opts.MaxAge, opts.Cap
	if now.IsZero() {
		now = time.Now()
	}
	if maxAge <= 0 {
		maxAge = VerdictMaxAge
	}
	if limit <= 0 {
		limit = VerdictCap
	}
	var res SweepResult
	entries, err := os.ReadDir(s.verdictDir())
	if errors.Is(err, fs.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	if !opts.Dry { // a plan takes no lock: taking it would create the lock file
		release, err := s.lockAt(context.Background(), s.verdictLockPath())
		if err != nil {
			return res, err
		}
		defer release()
	}

	type file struct {
		key  string
		used time.Time
		size int64
	}
	var live []file
	var errs []error
	remove := func(name string, size int64, reason string) bool {
		path := filepath.Join(s.verdictDir(), name)
		if !opts.Dry {
			if err := removeFile(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
				return false
			}
		}
		if opts.Removed != nil {
			opts.Removed(path, size, reason)
		}
		return true
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		name := e.Name()
		age := now.Sub(info.ModTime())
		if key, ok := strings.CutSuffix(name, ".json"); ok && validKey(key) {
			switch {
			case age > maxAge:
				if remove(name, info.Size(), "verdict unused for "+age.Truncate(time.Hour).String()) {
					res.Expired++
					continue
				}
			case s.allLanesClosed(key, opts.Closed):
				if remove(name, info.Size(), "verdict of closed lanes only") {
					res.Closed++
					continue
				}
			}
			live = append(live, file{key, info.ModTime(), info.Size()})
		} else if strings.Contains(name, ".json.tmp") && age > tempMaxAge {
			remove(name, info.Size(), "abandoned temp file of an interrupted write")
		}
	}
	slices.SortFunc(live, func(a, b file) int {
		if c := a.used.Compare(b.used); c != 0 {
			return c
		}
		return strings.Compare(a.key, b.key)
	})
	for len(live) > limit {
		if remove(live[0].key+".json", live[0].size, "verdict over the cap of "+strconv.Itoa(limit)+" files, least recently used") {
			res.Evicted++
		} else {
			break
		}
		live = live[1:]
	}
	res.Kept = len(live)
	return res, errors.Join(errs...)
}

// allLanesClosed reports a readable verdict that names lanes, all of which are closed.
func (s *Store) allLanesClosed(key string, closed func(string) bool) bool {
	if closed == nil {
		return false
	}
	v, ok, err := s.readVerdict(key, false)
	if err != nil || !ok || len(v.Lanes) == 0 {
		return false
	}
	return !slices.ContainsFunc(v.Lanes, func(l string) bool { return !closed(l) })
}
