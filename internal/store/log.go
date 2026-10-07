package store

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// logLine is one committed event as it sits in events-YYYY-MM.jsonl: the v1
// record the gate's reader already understands, plus the two fields only the
// store reads. Ev is the whole kernel event (the log is what a checkpoint is
// folded from, so nothing of it may be lost) and Ver the lane version the
// commit that logged it produced. A record without Ev is another writer's, and
// the fold skips it.
type logLine struct {
	V      int           `json:"v"`
	Seq    int64         `json:"seq,omitempty"`
	At     string        `json:"at"`
	Lane   string        `json:"lane,omitempty"`
	Actor  string        `json:"actor,omitempty"`
	Kind   string        `json:"kind"`
	Ver    uint64        `json:"ver,omitempty"`
	BinVer string        `json:"binver,omitempty"`
	Ev     *kernel.Event `json:"ev,omitempty"`
}

// logPos is a place in the log: the file and the byte offset the next unread
// record starts at. The zero value is the start of the log.
type logPos struct {
	File string `json:"file"`
	Off  int64  `json:"off"`
}

// logged is an event read back with the version of the commit that wrote it.
type logged struct {
	ver uint64
	ev  kernel.Event
}

// appendEvent writes ev, the fact of the commit that produced version ver, to
// the month file of the writer's clock: file order is then commit order, which
// the event's own time (when it was observed) is not.
func (s *Store) appendEvent(lane string, ver uint64, ev kernel.Event) error {
	now := time.Now().UTC()
	return core.AppendEventLine(s.dir, now, func(seq int64) ([]byte, error) {
		return json.Marshal(logLine{
			V: core.EventSchema, Seq: seq, At: now.Format("2006-01-02T15:04:05.000Z07:00"),
			Lane: lane, Actor: ev.Actor, Kind: string(ev.Kind), Ver: ver, BinVer: buildinfo.Version(), Ev: &ev,
		})
	})
}

// scan reads the lane's events from pos to the end of the log, oldest first,
// and answers where it stopped. A line that is torn, of another writer, or of
// another lane is skipped, and a last line still being written (no newline yet)
// is left for the next scan, so end never moves past a record not fully read.
func (s *Store) scan(lane string, pos logPos) ([]logged, logPos) {
	names, _ := filepath.Glob(filepath.Join(s.dir, "events-*.jsonl"))
	sort.Strings(names)
	var out []logged
	end := pos
	for _, name := range names {
		base := filepath.Base(name)
		var off int64
		switch {
		case base < pos.File:
			continue
		case base == pos.File:
			off = pos.Off
		}
		data, err := readFrom(name, off)
		if err != nil {
			continue
		}
		if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
			data = data[:i+1]
		} else {
			data = nil
		}
		end = logPos{File: base, Off: off + int64(len(data))}
		out = append(out, linesOf(lane, data)...)
	}
	return out, end
}

func linesOf(lane string, data []byte) []logged {
	var out []logged
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		if !bytes.Contains(raw, []byte(`"ev":`)) {
			continue
		}
		var l logLine
		if json.Unmarshal(raw, &l) != nil || l.V != core.EventSchema || l.Ev == nil || l.Lane != lane {
			continue
		}
		out = append(out, logged{ver: l.Ver, ev: *l.Ev})
	}
	return out
}

func readFrom(path string, off int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

// Events returns the lane's logged events, oldest first: what a rebuild folds.
func (s *Store) Events(lane string) []kernel.Event {
	logs, _ := s.scan(lane, logPos{})
	out := make([]kernel.Event, len(logs))
	for i, l := range logs {
		out[i] = l.ev
	}
	return out
}
