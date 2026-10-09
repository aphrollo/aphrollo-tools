package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// foreignTail appends n bytes (at least) of other writers' lines to the month log, the way
// every other lane's hooks grow the one log a lane's checkpoint reads the tail of.
func foreignTail(t *testing.T, dir string, n int) int64 {
	t.Helper()
	names, _ := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	if len(names) != 1 {
		t.Fatalf("want one month log, got %v", names)
	}
	line := `{"v":1,"at":"2026-10-03T09:00:00.000Z","kind":"stage.timing","lane":"other"}` + "\n"
	f, err := os.OpenFile(names[0], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Repeat(line, n/len(line)+1)); err != nil {
		t.Fatal(err)
	}
	fi, _ := f.Stat()
	return fi.Size()
}

// A lane that does not commit while the log grows would otherwise re-read all of the growth
// on every question (a PreToolUse hook's budget is 50 ms; the tail of a busy repo is
// megabytes): the first Load that sees a long tail moves the checkpoint past it.
func TestLoad_aLongUnfoldedTailMovesTheCheckpointPastIt(t *testing.T) {
	s := open(t, t.TempDir())
	mustFact(t, s, entered("fix", "s1/a"))
	before := readCk(t, s, "fix")
	size := foreignTail(t, s.dir, 2*refreshLag)
	want, wantVer, err := s.Load(bounded(t), "fix")
	if err != nil {
		t.Fatal(err)
	}
	got := readCk(t, s, "fix")
	if got.Log.Off != size || got.Ver != before.Ver {
		t.Errorf("checkpoint after Load: off %d ver %d, want off %d (the log's end) at ver %d", got.Log.Off, got.Ver, size, before.Ver)
	}
	again, againVer, _ := s.Load(bounded(t), "fix")
	if againVer != wantVer || again.Lane.Branch != want.Lane.Branch || len(again.Lane.Actors) != len(want.Lane.Actors) {
		t.Errorf("the record changed across the checkpoint move: %+v@%d vs %+v@%d", again, againVer, want, wantVer)
	}
}

// A short tail is cheap to read: a write for it would cost more than it saves.
func TestLoad_aShortTailLeavesTheCheckpointAlone(t *testing.T) {
	s := open(t, t.TempDir())
	mustFact(t, s, entered("fix", "s1/a"))
	before := readCk(t, s, "fix")
	foreignTail(t, s.dir, refreshLag/2)
	if _, _, err := s.Load(bounded(t), "fix"); err != nil {
		t.Fatal(err)
	}
	if got := readCk(t, s, "fix"); got.Log.Off != before.Log.Off {
		t.Errorf("off moved to %d on a short tail, want it left at %d", got.Log.Off, before.Log.Off)
	}
}

// Load never waits for a writer: a lane whose lock is held is read as it is.
func TestLoad_aHeldLockIsNotWaitedFor(t *testing.T) {
	s := open(t, t.TempDir(), func(o *Options) { o.LockWait = time.Hour })
	mustFact(t, s, entered("fix", "s1/a"))
	before := readCk(t, s, "fix")
	foreignTail(t, s.dir, 2*refreshLag)
	f, err := core.OpenLockFile(s.lockPath("fix"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !core.TryLockExclusive(f) {
		t.Fatal("could not take the lane lock")
	}
	start := time.Now()
	if _, _, err := s.Load(bounded(t), "fix"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("Load waited %v on a held lock", took)
	}
	if got := readCk(t, s, "fix"); got.Log.Off != before.Log.Off {
		t.Errorf("checkpoint moved to %d while another writer held the lock", got.Log.Off)
	}
}

// The move is only of the checkpoint the read started from: one a writer has replaced
// since is that writer's, newer than the view, and stays.
func TestAdvance_NeverReplacesACheckpointAWriterMovedSinceTheRead(t *testing.T) {
	s := open(t, t.TempDir())
	mustFact(t, s, entered("fix", "s1/a"))
	foreignTail(t, s.dir, 2*refreshLag)
	stale, err := s.current("fix", false)
	if err != nil {
		t.Fatal(err)
	}
	mustFact(t, s, entered("fix", "s2/a")) // a writer commits after the read
	moved := readCk(t, s, "fix")
	s.advance("fix", stale)
	if got := readCk(t, s, "fix"); got.Ver != moved.Ver || got.Log != moved.Log || len(got.Rec.Lane.Actors) != 2 {
		t.Errorf("checkpoint = ver %d %+v with %d actors, want the writer's: ver %d %+v with 2", got.Ver, got.Log, len(got.Rec.Lane.Actors), moved.Ver, moved.Log)
	}
}

func TestLagOf_IsTheBytesBetweenTwoPlacesAndAWholeMonthPastANewerFile(t *testing.T) {
	for _, c := range []struct {
		name     string
		from, to logPos
		want     int64
	}{
		{"from the start of the log", logPos{}, logPos{File: "events-2026-10.jsonl", Off: 900}, 900},
		{"within one month file", logPos{File: "events-2026-10.jsonl", Off: 100}, logPos{File: "events-2026-10.jsonl", Off: 900}, 800},
		{"into a newer month file", logPos{File: "events-2026-09.jsonl", Off: 100}, logPos{File: "events-2026-10.jsonl", Off: 5}, refreshLag},
	} {
		if got := lagOf(c.from, c.to); got != c.want {
			t.Errorf("%s: lagOf = %d, want %d", c.name, got, c.want)
		}
	}
}

// padTail appends one foreign line that brings the month log to exactly size bytes.
func padTail(t *testing.T, dir string, size int64) {
	t.Helper()
	names, _ := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	fi, err := os.Stat(names[0])
	if err != nil {
		t.Fatal(err)
	}
	const open, shut = `{"v":1,"kind":"stage.timing","pad":"`, "\"}\n"
	room := size - fi.Size() - int64(len(open)+len(shut))
	if room < 0 {
		t.Fatalf("log is already %d bytes, past %d", fi.Size(), size)
	}
	f, err := os.OpenFile(names[0], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(open + strings.Repeat("x", int(room)) + shut); err != nil {
		t.Fatal(err)
	}
}

// The bound is inclusive: a tail of exactly refreshLag bytes moves the checkpoint, one byte
// less does not. (The checkpoint of a lane's last commit sits before that commit's own
// event, at the start of an empty log, so the tail is the whole file.)
func TestLoad_TheLagBoundIsInclusive(t *testing.T) {
	for _, c := range []struct {
		name  string
		size  int64
		moved bool
	}{
		{"one byte short", refreshLag - 1, false},
		{"exactly the bound", refreshLag, true},
	} {
		s := open(t, t.TempDir())
		mustFact(t, s, entered("fix", "s1/a"))
		padTail(t, s.dir, c.size)
		if _, _, err := s.Load(bounded(t), "fix"); err != nil {
			t.Fatal(err)
		}
		if got := readCk(t, s, "fix").Log.Off; (got == c.size) != c.moved {
			t.Errorf("%s: checkpoint off = %d after a %d byte tail, moved = %v, want moved = %v", c.name, got, c.size, got == c.size, c.moved)
		}
	}
}
