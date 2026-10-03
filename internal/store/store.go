// Package store is the per-repo store of docs/trellis-architecture.md (§3
// "Storage", §8): it keeps each lane's checkpoint on disk and implements the
// engine's Store interface over it. Nothing wires it into a hook yet.
//
// # On disk
//
// A store lives in one directory, <state root>/state/<repo>/ (core.RepoStateDir):
//
//	events-YYYY-MM.jsonl         the one log, shared with the gate's v1 events
//	lanes/<lane>.json            the lane's checkpoint, replaced by rename
//	lanes/<lane>.lock            the lane's lock
//
// <lane> is the lane key written by laneFileName. A checkpoint is
// {"f":1,"fold":1,"ver":n,"lane":key,"log":{file,off},"rec":{...}}: f is the
// file format, fold the version of the kernel fold that wrote rec, ver the
// compare-and-swap token Load hands out and Commit checks, log how far into the
// event log rec already reflects, and rec the engine's Record.
//
// # Crash safety
//
// Commit takes the lane's lock (bounded: a holder that does not let go in
// Options.LockWait fails the commit with ErrConflict, which the engine retries,
// so a hook never stalls on it), appends its events to the log, then replaces
// the checkpoint. A crash between the two leaves a checkpoint behind the log;
// the next Load or Commit folds the logged events after the checkpoint's
// position back onto it. A checkpoint that cannot be read, or that an older or
// newer fold wrote, is refolded from the whole log instead of failing the call.
// Only a checkpoint of a newer file format is refused, by name.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/render"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

const (
	// FormatVersion is the checkpoint file format this binary reads and writes.
	FormatVersion = 1
	// FoldVersion is the version of the kernel fold this binary runs. A
	// checkpoint written by another fold is not trusted: it is refolded from
	// the log. A binary never replaces a checkpoint of a newer fold (§3
	// "Versioning"): it appends its events and leaves the file for the newer one.
	FoldVersion = 1

	// DefaultLockWait bounds the wait for a lane's lock.
	DefaultLockWait = 100 * time.Millisecond
	lockPoll        = 5 * time.Millisecond

	// rebuildGap is how far past the log's last version a rebuild from an
	// unreadable checkpoint puts the lane. The lost file may have been ahead of
	// the log (a commit of no events logs nothing), and a holder of one of those
	// versions must not find its token valid again.
	rebuildGap = 1 << 20
)

// ErrConflict is what Commit answers when the lane's version is no longer the
// one expected, or its lock could not be taken in time: nothing was changed.
var ErrConflict = errors.New("store: lane record changed since it was loaded")

// Record is one lane's checkpoint as far as the kernel is concerned: the lane
// machine's state and the TDD machine's units (architecture §3 "Lane record").
//
// Delivered is what the agent has been told (render.Delivery, bounded by the
// engine). Deliveries append no event, so the log cannot give them back: a
// rebuild keeps them from a checkpoint it could read, and a lost checkpoint
// loses them, which only makes lines due again, never lost.
//
// Guided holds the guided-once flags of units no fact has named (see Flags).
// Like Delivered it is checkpoint-only: a question is never logged, so a
// rebuild keeps Guided from a checkpoint it could read, and a lost checkpoint
// loses it, which at worst shows a guide once more, never blocks.
type Record struct {
	Lane      kernel.State
	Units     kernel.Units
	Guided    map[string]Flags `json:",omitempty"`
	Delivered []render.Delivery
}

// Options configures Open.
type Options struct {
	// Config is the repo's config as the engine decides with it: a rebuild
	// folds the log with it, so it must be the engine's.
	Config kernel.Config
	// LockWait bounds the wait for a lane's lock; zero is DefaultLockWait.
	LockWait time.Duration
	// Warn receives the one line said when a checkpoint is rebuilt because it
	// could not be read; nil writes it to standard error.
	Warn func(string)
}

// Store is the on-disk store of one repo. It is safe for concurrent use, by
// goroutines and by processes.
type Store struct {
	dir      string
	cfg      kernel.Config
	lockWait time.Duration
	warn     func(string)

	mu     sync.Mutex
	warned map[string]bool

	afterAppend func() error // test seam: runs between the log append and the checkpoint replace
}

// Open returns the store kept in dir, which it creates.
func Open(dir string, opts Options) (*Store, error) {
	if dir == "" {
		return nil, errors.New("store: no directory: there is no state root to keep lanes in")
	}
	if err := os.MkdirAll(filepath.Join(dir, "lanes"), 0o700); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	s := &Store{dir: dir, cfg: opts.Config, lockWait: opts.LockWait, warn: opts.Warn, warned: map[string]bool{}}
	if s.lockWait <= 0 {
		s.lockWait = DefaultLockWait
	}
	if s.warn == nil {
		s.warn = func(msg string) { fmt.Fprintln(os.Stderr, msg) }
	}
	return s, nil
}

func (s *Store) checkpointPath(lane string) string {
	return filepath.Join(s.dir, "lanes", laneFileName(lane)+".json")
}

func (s *Store) lockPath(lane string) string {
	return filepath.Join(s.dir, "lanes", laneFileName(lane)+".lock")
}

// checkpoint is the lane file.
type checkpoint struct {
	F    int    `json:"f"`
	Fold int    `json:"fold"`
	Ver  uint64 `json:"ver"`
	Lane string `json:"lane"`
	Log  logPos `json:"log"`
	Rec  Record `json:"rec"`
}

// view is the lane as it stands now: the checkpoint with the log's later events
// folded in, or the whole log's fold.
type view struct {
	rec Record
	ver uint64
	pos logPos // how far into the log rec reflects
	// replace is whether Commit may replace the checkpoint: not when a newer
	// fold wrote it.
	replace bool
}

// Load implements the engine's Store. It takes no lock: a checkpoint is
// replaced whole, and a commit in flight shows as the log's tail.
func (s *Store) Load(ctx context.Context, lane string) (Record, uint64, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, 0, err
	}
	v, err := s.current(lane, false)
	return v.rec, v.ver, err
}

// Commit implements the engine's Store: under the lane's lock, if the lane is
// still at version expect, it appends events to the log and then replaces the
// checkpoint with rec at expect+1.
func (s *Store) Commit(ctx context.Context, lane string, expect uint64, rec Record, events []kernel.Event) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if lane == "" {
		return 0, errors.New("store: no lane to commit to")
	}
	release, err := s.lock(ctx, lane)
	if err != nil {
		return 0, err
	}
	defer release()
	cur, err := s.current(lane, false)
	if err != nil {
		return 0, err
	}
	if cur.ver != expect {
		return 0, ErrConflict
	}
	ver := expect + 1
	for _, ev := range events {
		if err := s.appendEvent(lane, ver, ev); err != nil {
			return 0, fmt.Errorf("store: log lane %q: %w", lane, err)
		}
	}
	if s.afterAppend != nil {
		if err := s.afterAppend(); err != nil {
			return 0, err
		}
	}
	if !cur.replace {
		return ver, nil
	}
	return ver, s.save(lane, ver, cur.pos, rec)
}

// Rebuild folds the lane's whole log into a checkpoint and returns it, whatever
// the checkpoint on disk says.
func (s *Store) Rebuild(ctx context.Context, lane string) (Record, uint64, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, 0, err
	}
	release, err := s.lock(ctx, lane)
	if err != nil {
		return Record{}, 0, err
	}
	defer release()
	v, err := s.current(lane, true)
	if err != nil {
		return Record{}, 0, err
	}
	if v.replace {
		err = s.save(lane, v.ver, v.pos, v.rec)
	}
	return v.rec, v.ver, err
}

func (s *Store) save(lane string, ver uint64, pos logPos, rec Record) error {
	data, err := json.Marshal(checkpoint{F: FormatVersion, Fold: FoldVersion, Ver: ver, Lane: lane, Log: pos, Rec: rec})
	if err != nil {
		return err
	}
	if err := core.WriteFileAtomic(s.checkpointPath(lane), data); err != nil {
		return fmt.Errorf("store: save lane %q: %w", lane, err)
	}
	return nil
}

// current reads the lane. A usable checkpoint is brought up to the log's end;
// a missing one, one of another fold, an unreadable one, or force, refolds the
// whole log. Only a checkpoint of a newer format is an error.
func (s *Store) current(lane string, force bool) (view, error) {
	data, err := core.ReadFileShared(s.checkpointPath(lane))
	missing := errors.Is(err, fs.ErrNotExist)
	var ck checkpoint
	if err == nil {
		var head struct {
			F int `json:"f"`
		}
		if json.Unmarshal(data, &head) == nil && head.F > FormatVersion {
			return view{}, fmt.Errorf("store: lane %q: state format %d needs a newer binary: run update", lane, head.F)
		}
		err = json.Unmarshal(data, &ck)
		if err == nil && (ck.F != FormatVersion || ck.Lane != lane) {
			err = fmt.Errorf("not a format %d checkpoint of this lane", FormatVersion)
		}
	}
	unreadable := err != nil && !missing
	if !unreadable && !missing && ck.Fold == FoldVersion && !force {
		logs, end := s.scan(lane, ck.Log)
		v := view{rec: ck.Rec, ver: ck.Ver, pos: end, replace: true}
		for _, l := range logs {
			if l.ver > ck.Ver {
				v.rec, v.ver = s.fold(v.rec, l.ev), max(v.ver, l.ver)
			}
		}
		return v, nil
	}
	logs, end := s.scan(lane, logPos{})
	v := view{pos: end, replace: missing || unreadable || ck.Fold <= FoldVersion}
	if !missing && !unreadable {
		v.rec.Guided = ck.Rec.Guided
	}
	for _, l := range logs {
		v.rec, v.ver = s.fold(v.rec, l.ev), max(v.ver, l.ver)
	}
	switch {
	case unreadable:
		v.ver += rebuildGap
		s.say(lane, err)
	case !missing:
		v.ver = max(v.ver, ck.Ver)
		v.rec.Delivered = ck.Rec.Delivered
	}
	return v, nil
}

// fold applies a logged fact the way the engine decided it when it was
// committed. A question was never a fact and is never logged; one that is
// found moves nothing.
func (s *Store) fold(r Record, ev kernel.Event) Record {
	if ev.Kind.Question() {
		return r
	}
	d := kernel.Decide(r.Lane, r.DecideUnits(ev.Unit), ev, s.cfg)
	return Record{Lane: d.Lane, Units: d.Units, Guided: r.Settled(ev.Unit), Delivered: r.Delivered}
}

// say reports an unreadable checkpoint once per lane and process.
func (s *Store) say(lane string, why error) {
	s.mu.Lock()
	first := !s.warned[lane]
	s.warned[lane] = true
	s.mu.Unlock()
	if first {
		s.warn(fmt.Sprintf("store: the checkpoint of lane %q cannot be read (%v): rebuilding it from the event log", lane, why))
	}
}

// lock takes the lane's lock, waiting at most the store's bound for another
// holder. A lock that stays held is ErrConflict, which the engine retries; a
// lock file that cannot be opened is the error it is.
func (s *Store) lock(ctx context.Context, lane string) (func(), error) {
	deadline := time.Now().Add(s.lockWait)
	for {
		f, err := core.OpenLockFile(s.lockPath(lane))
		if err != nil {
			return nil, fmt.Errorf("store: lock lane %q: %w", lane, err)
		}
		if core.TryLockExclusive(f) {
			return func() { _ = f.Close() }, nil // closing drops the lock on both platforms
		}
		_ = f.Close() // a handle that never held it
		if !time.Now().Before(deadline) {
			return nil, ErrConflict
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}
