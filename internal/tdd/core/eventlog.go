package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// eventLockWait bounds the wait for the log's lock: a writer that cannot
	// get it in this time appends without a sequence number rather than lose
	// the event or stall a hook.
	eventLockWait = 2 * time.Second
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

// appendEventRecord writes e to path in one write. Under the log's lock the
// record is numbered from the file's size, so the sequence grows with the file;
// without the lock it still writes, unnumbered. The log is never read here: on
// Windows a read of a file that was just written costs milliseconds, and the
// whole record budget is two. A line a crash tore is skipped by the reader.
func appendEventRecord(path string, e Event) error {
	release, locked := acquirePathLockWithDeadline(path, eventLockWait)
	defer release()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err := f.Stat(); locked && err == nil {
		e.Seq = eventSeq(monthOf(e.At), fi.Size())
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// ReadEvents returns every record this binary understands for the repository
// root belongs to, oldest first: the records of the older single-file log that
// name the repo, then each month file of the repo's own directory. A line of
// another version, or one that does not parse, is skipped: the files are
// append-only and shared with other binaries.
func ReadEvents(root string) []Event {
	repo, _, common := repoIdentity(root)
	var out []Event
	if dir := StateDir(); dir != "" {
		for _, e := range readEventFile(filepath.Join(dir, "events.jsonl")) {
			if e.Repo == repo {
				out = append(out, legacyEvent(e))
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
		out = append(out, readEventFile(name)...)
	}
	return out
}

// legacyEvent maps a record of the older log onto the current vocabulary: its
// "edit" kind was every post-edit stage line, which is now "stage.timing".
func legacyEvent(e Event) Event {
	if e.Kind == "edit" {
		e.Kind = "stage.timing"
	}
	return e
}

func readEventFile(path string) []Event {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Event
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var e Event
		if json.Unmarshal(bytes.TrimSpace(line), &e) == nil && e.V == EventSchema {
			out = append(out, e)
		}
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
