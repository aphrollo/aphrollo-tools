package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// # Retention
//
// Retain is the one sweep of a repo's state directory (architecture §3
// "Storage"), per kind of file:
//
//	events-YYYY-MM.jsonl   gone 16 weeks after its month ended, unless it
//	                       holds an event of a lane that is not removed
//	lanes/<lane>.json      gone 7 days after the lane was removed
//	verdicts/<key>.json    SweepVerdicts: 14 days unused or lane closed; LRU 2,000
//	jobs, out, cache       DirRule: files past an age, then the least recently
//	                       used past a byte cap
//
// It never removes what something live holds: only a removed lane's checkpoint
// goes, and only under that lane's lock (a lane another process is committing
// to is left for the next sweep); a verdict of a lane not known to be closed
// stays until its age; a DirRule's Keep (no caller sets one today) names files
// a running job still holds. Everything is best effort and repeatable: a file that cannot be
// removed stays and is reported, and the next sweep tries again.
//
// Lock files (lanes/<lane>.lock, verdicts/.lock) are empty and never removed:
// unlinking one a second process is about to open would give two holders two
// files under one name, and doing it safely needs the lock protocol to verify
// the file it locked is still the one at its path. StateSizes counts them.
//
// jobs/, out/ and cache/ are not under the store yet (a job record lives in
// <state>/deferred, the ratchet cache in <state>/ratchet-cache); the caller
// names the directories as DirRules and moving them is a later lane's.

const (
	// EventsMaxAge is how long an event month is kept after it ended: 16 weeks.
	EventsMaxAge = 16 * 7 * 24 * time.Hour
	// RemovedLaneMaxAge is how long a removed lane's checkpoint is kept.
	RemovedLaneMaxAge = 7 * 24 * time.Hour
	// JobMaxAge is how long a job's files, and a run's raw output, are kept
	// after the job finished.
	JobMaxAge = 24 * time.Hour
	// OutMaxBytes and CacheMaxBytes are the LRU byte caps of out/ and cache/.
	OutMaxBytes   = 200 << 20
	CacheMaxBytes = 200 << 20
)

// DirRule is the retention of one directory of loose files, walked recursively.
type DirRule struct {
	Name string
	Dir  string
	// MaxAge removes a file not modified for longer; zero keeps files by age.
	MaxAge time.Duration
	// MaxBytes evicts the least recently modified files until the directory
	// fits; zero is no cap. A file Keep holds counts toward the size.
	MaxBytes int64
	// Keep says a file is held by something live and is never removed.
	Keep func(path string) bool
}

// RetentionOptions configure Retain.
type RetentionOptions struct {
	// Now is the time ages are counted to; zero is the clock.
	Now time.Time
	// Dry lists what would be removed and removes nothing.
	Dry   bool
	Rules []DirRule
}

// Removal is one file removed, or with Dry, to be removed.
type Removal struct {
	Path   string
	Size   int64
	Reason string
}

// RetentionReport is what a sweep did. Errors are the files that could not be removed.
type RetentionReport struct {
	Removed []Removal
	Errors  []error
}

// Bytes is the total size of the removed files.
func (r RetentionReport) Bytes() int64 {
	var n int64
	for _, x := range r.Removed {
		n += x.Size
	}
	return n
}

// Retain applies every retention rule to the store's directory and to the
// directories rules name. The verdicts are swept before the lanes, so a
// verdict still finds its lane closed, and the lanes before the event months,
// so a month is judged by the lanes that are left. Files that could not be
// removed are in the report's Errors, once each; the error is only a canceled
// context.
func (s *Store) Retain(ctx context.Context, opts RetentionOptions) (RetentionReport, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	var rep RetentionReport
	note := func(path string, size int64, reason string) {
		rep.Removed = append(rep.Removed, Removal{path, size, reason})
	}
	closed := map[string]bool{}
	_, err := s.SweepVerdicts(SweepOptions{
		Now: opts.Now, Dry: opts.Dry, Removed: note,
		Closed: func(lane string) bool {
			c, seen := closed[lane]
			if !seen {
				c = s.laneRemoved(lane)
				closed[lane] = c
			}
			return c
		},
	})
	if err != nil {
		rep.Errors = append(rep.Errors, err)
	}
	if err := ctx.Err(); err != nil {
		return rep, err
	}
	s.sweepLanes(ctx, opts, note, &rep)
	s.sweepEvents(opts, note, &rep)
	rep.merge(RetainDirs(opts))
	return rep, nil
}

// RetainDirs applies opts.Rules alone, for directories that belong to no one
// repo's store (the ratchet cache is shared by every repo).
func RetainDirs(opts RetentionOptions) RetentionReport {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	var rep RetentionReport
	note := func(path string, size int64, reason string) {
		rep.Removed = append(rep.Removed, Removal{path, size, reason})
	}
	for _, rule := range opts.Rules {
		applyRule(rule, opts, note, &rep)
	}
	return rep
}

func (r *RetentionReport) merge(o RetentionReport) {
	r.Removed = append(r.Removed, o.Removed...)
	r.Errors = append(r.Errors, o.Errors...)
}

// remove deletes path unless the sweep is dry, counting a failure.
func remove(path string, dry bool, rep *RetentionReport) bool {
	if dry {
		return true
	}
	if err := removeFile(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		rep.Errors = append(rep.Errors, err)
		return false
	}
	return true
}

// removeFile is os.Remove, a seam so a test can state what a file that will
// not go (a sharing violation on Windows) does to the report.
var removeFile = os.Remove

// SweptThroughFile names the marker Retain leaves in the state directory: the
// end of the newest event month it removed, RFC 3339. The log is complete from
// that moment on and holds only some of what came before it.
const SweptThroughFile = "events-swept-through"

// SweptThrough is the moment before which the event log in dir may have lost
// events to retention: zero when no month was ever removed. A reader that must
// not read a missing event as "never happened" (the outside-merge scan) treats
// anything before it as unknown.
func SweptThrough(dir string) time.Time {
	data, err := core.ReadFileShared(filepath.Join(dir, SweptThroughFile))
	if err != nil {
		return time.Time{} // absence-ok: no marker means no month was ever removed
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}
	}
	return at
}

// writeMarker is the marker's write, a seam so a test can state what a failed
// write does to the month it was raised for.
var writeMarker = core.WriteFileAtomic

// markSweptThrough raises the marker to at; it never moves back.
func (s *Store) markSweptThrough(at time.Time) error {
	if !at.After(SweptThrough(s.dir)) {
		return nil
	}
	return writeMarker(filepath.Join(s.dir, SweptThroughFile), []byte(at.UTC().Format(time.RFC3339)))
}

// sweepEvents removes the event months that ended more than EventsMaxAge ago,
// except a month that holds an event of a lane that is not removed: the log is
// what a lane is refolded from (a lost checkpoint, a new fold version), and a
// refold from a log missing its first months would rebuild a partial lane
// without saying so. A name that is not events-YYYY-MM.jsonl is never touched.
// The SweptThrough marker is raised to a month's end BEFORE the month is
// removed, and a month whose marker cannot be written stays: a crash between
// the two steps must not leave events gone with nothing saying so.
func (s *Store) sweepEvents(opts RetentionOptions, note func(string, int64, string), rep *RetentionReport) {
	names, _ := filepath.Glob(filepath.Join(s.dir, "events-*.jsonl"))
	live := map[string]bool{} // lane -> not removed, judged once per sweep
	for _, name := range names {
		month, err := time.Parse("events-2006-01.jsonl", filepath.Base(name))
		if err != nil {
			continue
		}
		ended := month.AddDate(0, 1, 0)
		info, err := os.Stat(name)
		if err != nil || opts.Now.Sub(ended) <= EventsMaxAge || s.holdsLiveLane(name, live) {
			continue
		}
		if !opts.Dry {
			if err := s.markSweptThrough(ended); err != nil {
				rep.Errors = append(rep.Errors, err)
				continue
			}
		}
		if remove(name, opts.Dry, rep) {
			note(name, info.Size(), "event log month ended "+ended.Format("2006-01-02")+", past 16 weeks")
		}
	}
}

// holdsLiveLane reports an event file with an event of a lane that is not
// removed. A lane whose state cannot be told is live. live caches the answers.
func (s *Store) holdsLiveLane(name string, live map[string]bool) bool {
	data, err := os.ReadFile(name)
	if err != nil {
		return true // a file that cannot be read is not judged unneeded
	}
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		if !bytes.Contains(raw, []byte(`"ev":`)) {
			continue
		}
		var l logLine
		if json.Unmarshal(raw, &l) != nil || l.Ev == nil || l.Lane == "" {
			continue
		}
		alive, seen := live[l.Lane]
		if !seen {
			v, err := s.current(l.Lane, false)
			alive = err != nil || v.rec.Lane.Life != kernel.LifeRemoved
			live[l.Lane] = alive
		}
		if alive {
			return true
		}
	}
	return false
}

// laneRemoved reports a lane whose checkpoint says it was removed.
func (s *Store) laneRemoved(lane string) bool {
	ck, ok := readCheckpointFile(s.checkpointPath(lane))
	return ok && ck.Rec.Lane.Life == kernel.LifeRemoved
}

// readCheckpointFile reads a checkpoint without folding the log; a file that
// cannot be read or is of another format is not one.
func readCheckpointFile(path string) (checkpoint, bool) {
	data, err := core.ReadFileShared(path)
	if err != nil {
		return checkpoint{}, false // absence-ok: no readable checkpoint is no removed lane
	}
	var ck checkpoint
	if json.Unmarshal(data, &ck) != nil || ck.F != FormatVersion {
		return checkpoint{}, false
	}
	return ck, true
}

// sweepLanes removes the checkpoints of lanes removed more than
// RemovedLaneMaxAge ago, each under its own lane's lock and judged again under
// it, and the temp files an interrupted save left.
func (s *Store) sweepLanes(ctx context.Context, opts RetentionOptions, note func(string, int64, string), rep *RetentionReport) {
	dir := filepath.Join(s.dir, "lanes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path, age := filepath.Join(dir, e.Name()), opts.Now.Sub(info.ModTime())
		switch {
		case strings.Contains(e.Name(), ".json.tmp") && age > tempMaxAge:
			if remove(path, opts.Dry, rep) {
				note(path, info.Size(), "abandoned temp file of an interrupted save")
			}
		case strings.HasSuffix(e.Name(), ".json") && age > RemovedLaneMaxAge:
			s.sweepLane(ctx, path, info.Size(), opts, note, rep)
		}
	}
}

func (s *Store) sweepLane(ctx context.Context, path string, size int64, opts RetentionOptions, note func(string, int64, string), rep *RetentionReport) {
	ck, ok := readCheckpointFile(path)
	if !ok || ck.Rec.Lane.Life != kernel.LifeRemoved || ck.Lane == "" || s.checkpointPath(ck.Lane) != path {
		return
	}
	reason := "lane " + ck.Lane + " removed more than 7 days ago"
	if opts.Dry {
		note(path, size, reason)
		return
	}
	release, err := s.lock(ctx, ck.Lane)
	if err != nil {
		return // a commit holds it: the lane is live, the next sweep looks again
	}
	// Judged again under the lock: a commit may have revived the lane.
	if again, ok := readCheckpointFile(path); ok && again.Rec.Lane.Life == kernel.LifeRemoved && opts.Now.Sub(modTime(path)) > RemovedLaneMaxAge {
		if remove(path, false, rep) {
			note(path, size, reason)
		}
	}
	release()
	// The lock file stays: unlinking it while another process opens it would
	// give a second holder a different file under the same name.
}

func modTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Now()
	}
	return info.ModTime()
}

// ruleFile is a file a DirRule judges.
type ruleFile struct {
	path string
	size int64
	used time.Time
	keep bool
}

// walkFiles calls fn for each regular file under dir whose name want accepts.
func walkFiles(dir string, want func(name string) bool, fn func(path string, info fs.FileInfo)) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || !want(d.Name()) {
			return nil
		}
		if info, err := d.Info(); err == nil {
			fn(path, info)
		}
		return nil
	})
}

// ruleFiles lists the regular files under the rule's directory, oldest use first.
func ruleFiles(rule DirRule) []ruleFile {
	var files []ruleFile
	walkFiles(rule.Dir, func(string) bool { return true }, func(path string, info fs.FileInfo) {
		files = append(files, ruleFile{path, info.Size(), info.ModTime(), rule.Keep != nil && rule.Keep(path)})
	})
	slices.SortFunc(files, func(a, b ruleFile) int {
		if c := a.used.Compare(b.used); c != 0 {
			return c
		}
		return strings.Compare(a.path, b.path)
	})
	return files
}

// applyRule removes what is past the rule's age, then the least recently used
// files until the rest fits its byte cap. A kept file is never removed.
func applyRule(rule DirRule, opts RetentionOptions, note func(string, int64, string), rep *RetentionReport) {
	var live []ruleFile
	var total int64
	for _, f := range ruleFiles(rule) {
		if !f.keep && rule.MaxAge > 0 && opts.Now.Sub(f.used) > rule.MaxAge {
			if remove(f.path, opts.Dry, rep) {
				note(f.path, f.size, rule.Name+": unused for "+opts.Now.Sub(f.used).Truncate(time.Hour).String())
				continue
			}
		}
		live = append(live, f)
		total += f.size
	}
	for _, f := range live {
		if rule.MaxBytes <= 0 || total <= rule.MaxBytes {
			break
		}
		if f.keep {
			continue
		}
		if remove(f.path, opts.Dry, rep) {
			note(f.path, f.size, fmt.Sprintf("%s: over the cap of %d bytes, least recently used", rule.Name, rule.MaxBytes))
			total -= f.size
		}
	}
}

// SizeLine is what one kind of state holds. Cap is 0 where the kind has no byte cap.
type SizeLine struct {
	Name  string
	Files int
	Bytes int64
	Cap   int64
}

func measure(name, dir string, cap int64, want func(string) bool) SizeLine {
	line := SizeLine{Name: name, Cap: cap}
	walkFiles(dir, want, func(_ string, info fs.FileInfo) {
		line.Files++
		line.Bytes += info.Size()
	})
	return line
}

// DirSizes measures the directories rules name, one line each.
func DirSizes(rules []DirRule) []SizeLine {
	var out []SizeLine
	for _, r := range rules {
		out = append(out, measure(r.Name, r.Dir, r.MaxBytes, func(string) bool { return true }))
	}
	return out
}

// StateSizes measures each kind of file of the store's directory and of the
// directories rules name, for `gate gc --dry` and for the caps' tests. It
// creates nothing.
func (s *Store) StateSizes(rules []DirRule) []SizeLine {
	isJSON := func(n string) bool { return strings.HasSuffix(n, ".json") }
	out := []SizeLine{
		measure("events", s.dir, 0, func(n string) bool { return strings.HasPrefix(n, "events-") && strings.HasSuffix(n, ".jsonl") }),
		measure("lanes", filepath.Join(s.dir, "lanes"), 0, isJSON),
		measure("verdicts", s.verdictDir(), 0, isJSON),
	}
	locks := measure("locks", filepath.Join(s.dir, "lanes"), 0, func(n string) bool { return strings.HasSuffix(n, ".lock") })
	if _, err := os.Stat(s.verdictLockPath()); err == nil {
		locks.Files++ // the verdict lock is empty, like a lane's
	}
	out = append(out, locks)
	return append(out, DirSizes(rules)...)
}
