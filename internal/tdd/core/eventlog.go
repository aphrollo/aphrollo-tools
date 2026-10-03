package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// eventLockWait bounds the wait for the log's lock: a writer that cannot
	// get it in this time appends without a sequence number rather than lose
	// the event or stall a hook. The holder keeps the lock for one small
	// append, so a wait this long means something is wrong, not busy.
	eventLockWait = 100 * time.Millisecond
	// eventTimeFormat is the record's "at": UTC, millisecond precision.
	eventTimeFormat = "2006-01-02T15:04:05.000Z07:00"
)

// eventLogFile is the monthly file of dir that holds events made at at.
func eventLogFile(dir string, at time.Time) string {
	return filepath.Join(dir, "events-"+at.UTC().Format("2006-01")+".jsonl")
}

// eventSeq numbers the record that will start at byte offset in the log of the
// month at: the month as YYYYMM in the high bits and the offset below them, so
// the number grows with the file and across month files, and no two records of
// a repo share one. It orders events; it is not a gap-free counter.
func eventSeq(at time.Time, offset int64) int64 {
	at = at.UTC()
	return int64(at.Year()*100+int(at.Month()))<<eventSeqOffsetBits | offset
}

// eventSeqOffsetBits leaves room for a 1 TiB month file.
const eventSeqOffsetBits = 40

// eventLockBroken is set once this process failed to open an event lock file:
// every later event of the process writes unnumbered without trying, so a lock
// that cannot work costs one failed open, not one per event.
var eventLockBroken atomic.Bool

func resetEventLockLatchForTest() { eventLockBroken.Store(false) }

// acquireEventLock takes the lock beside the log at path, waiting up to
// eventLockWait for another writer. A lock file that cannot be opened latches
// the process unlocked and says nothing: the build lock's complaint about it
// is about builds, and the event is still written.
func acquireEventLock(path string) (release func(), ok bool) {
	if eventLockBroken.Load() {
		return func() {}, false
	}
	start := time.Now()
	for {
		f, err := openLockFile(path + ".lock")
		if err != nil {
			eventLockBroken.Store(true)
			return func() {}, false
		}
		if tryLockExclusive(f) {
			return func() {
				unlockFile(f)
				_ = f.Close()
			}, true
		}
		_ = f.Close()
		if time.Since(start) >= eventLockWait {
			return func() {}, false
		}
		time.Sleep(pathLockPollInterval)
	}
}

// appendEventRecord writes e to path in one write. Under the log's lock the
// record is numbered from the file's size, so the sequence grows with the file;
// without the lock it still writes, unnumbered, and the reader numbers it. The log is never read here: on
// Windows a read of a file that was just written costs milliseconds, and the
// whole record budget is two. The record is written with a newline before it
// as well as after, so a line a crash tore never swallows the next record; the
// reader skips the torn line and the blank ones.
func appendEventRecord(path string, e Event) error {
	return appendEventLine(path, monthOf(e.At), func(seq int64) ([]byte, error) {
		e.Seq = seq
		return json.Marshal(e)
	})
}

// AppendEventLine appends the record encode builds to the month file of dir
// that holds at, the way AppendEvent appends its own: under the log's lock,
// in one write, with the torn-line guard. encode gets the record's sequence
// number, 0 when the lock was not taken; the record must be one JSON line with
// "v":1 so every reader of the log reads it. It is how the store writes the
// events it folds checkpoints from into the same files the gate reads.
func AppendEventLine(dir string, at time.Time, encode func(seq int64) ([]byte, error)) error {
	return appendEventLine(eventLogFile(dir, at), at, encode)
}

func appendEventLine(path string, month time.Time, encode func(seq int64) ([]byte, error)) error {
	release, locked := acquireEventLock(path)
	defer release()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	var seq int64
	if fi, err := f.Stat(); locked && err == nil {
		seq = eventSeq(month, fi.Size()+1)
	}
	line, err := encode(seq)
	if err != nil {
		return err
	}
	_, err = f.Write(append(append([]byte{'\n'}, line...), '\n'))
	return err
}

// ReadEvents returns every record this binary understands for the repository
// root belongs to, oldest first: the records of the older single-file log that
// name the repo, then each month file of the repo's own directory. A line of
// another version, or one that does not parse, is skipped: the files are
// append-only and shared with other binaries.
func ReadEvents(root string) []Event {
	return readEventsSince(root, time.Time{})
}

// readEventsRecent is ReadEvents limited to the month files of the month
// before this one and later, and to the older single-file log only while it
// was last written within that window: what a "was this already recorded"
// question needs, without reading a log's whole history on every call.
func readEventsRecent(root string) []Event {
	now := time.Now().UTC()
	return readEventsSince(root, time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC))
}

// readEventsSince is ReadEvents over the files from the month of since on; the
// zero time reads everything.
func readEventsSince(root string, since time.Time) []Event {
	repo, _, common := repoIdentity(root)
	var out []Event
	if dir := StateDir(); dir != "" {
		legacy := filepath.Join(dir, "events.jsonl")
		if fi, err := os.Stat(legacy); err == nil && !fi.ModTime().Before(since) {
			for _, e := range readEventFile(legacy, time.Time{}) {
				if e.Repo == repo {
					out = append(out, legacyEvent(e))
				}
			}
		}
	}
	dir := RepoStateDir(common)
	if dir == "" {
		return out
	}
	names, _ := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	sort.Strings(names)
	for _, name := range names {
		if !since.IsZero() && eventFileMonth(name).Before(since) {
			continue
		}
		out = append(out, readEventFile(name, eventFileMonth(name))...)
	}
	return out
}

// eventFileMonth is the first instant of the month a file named
// events-YYYY-MM.jsonl holds, the zero time for a name that is not one (which
// reads as older than any window and is skipped).
func eventFileMonth(name string) time.Time {
	base := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(name), "events-"), ".jsonl")
	t, err := time.Parse("2006-01", base)
	if err != nil {
		return time.Time{}
	}
	return t
}

// legacyEvent maps a record of the older log onto the current vocabulary: its
// "edit" kind was every post-edit stage line, which is now "stage.timing".
func legacyEvent(e Event) Event {
	if e.Kind == "edit" {
		e.Kind = "stage.timing"
	}
	return e
}

// readEventFile reads the records of one log file. A month is the month the
// file holds: every record is numbered from the byte offset its line starts
// at, which is what its writer gave it when nothing else wrote meanwhile. Two
// writers can: one that gave up on the lock writes unnumbered, and one that
// holds it read the size before that write landed, so its number names a place
// the record is not at. The place in the file is the one number no two records
// share, and it grows with the file. The zero month leaves numbers as written,
// for the older single-file log.
func readEventFile(path string, month time.Time) []Event {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Event
	r := bufio.NewReader(f)
	var offset int64
	for {
		line, err := r.ReadBytes('\n')
		var e Event
		if json.Unmarshal(bytes.TrimSpace(line), &e) == nil && e.V == EventSchema {
			if !month.IsZero() {
				e.Seq = eventSeq(month, offset)
			}
			out = append(out, e)
		}
		offset += int64(len(line))
		if err != nil {
			return out
		}
	}
}

// monthOf is the time a record's "at" names, now when it does not parse.
func monthOf(at string) time.Time {
	if t, err := time.Parse(time.RFC3339, at); err == nil {
		return t
	}
	return time.Now()
}

// EventLogDir is the directory that holds the event log of the repository root
// belongs to: every worktree of a repo shares it. "" when there is no state
// root.
func EventLogDir(root string) string {
	_, _, common := repoIdentity(root)
	return RepoStateDir(common)
}
