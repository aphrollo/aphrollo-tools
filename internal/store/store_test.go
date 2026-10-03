package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/render"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

var t0 = time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)

func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func open(t *testing.T, dir string, mods ...func(*Options)) *Store {
	t.Helper()
	opts := Options{LockWait: 2 * time.Second}
	for _, m := range mods {
		m(&opts)
	}
	s, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open(%q): %v", dir, err)
	}
	return s
}

func entered(lane, actor string) kernel.Event {
	return kernel.Event{Kind: kernel.KindLaneEntered, Lane: lane, Actor: actor, At: t0}
}

// decideCommit decides ev the way the engine does and commits it against the
// version the store held when it was asked.
func decideCommit(ctx context.Context, s *Store, ev kernel.Event) (uint64, error) {
	rec, ver, err := s.Load(ctx, ev.Lane)
	if err != nil {
		return 0, err
	}
	d := kernel.Decide(rec.Lane, rec.DecideUnits(ev.Unit), ev, s.cfg)
	next := Record{Lane: d.Lane, Units: d.Units, Guided: rec.Settled(ev.Unit), Delivered: rec.Delivered}
	return s.Commit(ctx, ev.Lane, ver, next, []kernel.Event{ev})
}

func fact(t *testing.T, s *Store, ev kernel.Event) (uint64, error) {
	t.Helper()
	return decideCommit(bounded(t), s, ev)
}

func mustFact(t *testing.T, s *Store, ev kernel.Event) uint64 {
	t.Helper()
	ver, err := fact(t, s, ev)
	if err != nil {
		t.Fatalf("commit of %s by %s: %v", ev.Kind, ev.Actor, err)
	}
	return ver
}

func actors(r Record) []string {
	var out []string
	for a := range r.Lane.Actors {
		out = append(out, a)
	}
	return out
}

func readCk(t *testing.T, s *Store, lane string) checkpoint {
	t.Helper()
	data, err := os.ReadFile(s.checkpointPath(lane))
	if err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	var ck checkpoint
	if err := json.Unmarshal(data, &ck); err != nil {
		t.Fatalf("checkpoint is not JSON: %v\n%s", err, data)
	}
	return ck
}

func writeCk(t *testing.T, s *Store, lane string, ck checkpoint) {
	t.Helper()
	data, err := json.Marshal(ck)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.checkpointPath(lane), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_aLaneNeverSavedIsZeroAtVersionZero(t *testing.T) {
	s := open(t, t.TempDir())
	rec, ver, err := s.Load(bounded(t), "fix")
	if err != nil || ver != 0 || !reflect.DeepEqual(rec, Record{}) {
		t.Errorf("Load = %+v, %d, %v; want the zero record at version 0", rec, ver, err)
	}
}

func TestCommit_savedRecordComesBackExactly(t *testing.T) {
	s := open(t, t.TempDir())
	want := Record{
		Lane: kernel.State{
			Branch: "lane/fix", Life: kernel.LifePR, Head: "h1",
			Actors:     map[string]time.Time{"s1/a": t0.Add(90 * time.Millisecond)},
			CI:         map[string]kernel.Conclusion{"linux": kernel.CIGreen},
			CIRequired: []string{"linux", "windows"},
			LastActive: t0,
		},
		Units:     kernel.Units{"pkg/a": {Phase: kernel.PhaseOpen, Test: "TestA", Earns: true, Tree: "t1", Pair: kernel.Pair{Test: "TestA", Red: "t1"}}},
		Delivered: []render.Delivery{{ID: "line-1", Hook: render.HookPostToolBatch, Actor: "s1/a"}},
	}
	ver, err := s.Commit(bounded(t), "lane/fix", 0, want, nil)
	if err != nil || ver != 1 {
		t.Fatalf("Commit = %d, %v; want 1, nil", ver, err)
	}
	got, gotVer, err := open(t, s.dir).Load(bounded(t), "lane/fix")
	if err != nil || gotVer != 1 || !reflect.DeepEqual(got, want) {
		t.Errorf("Load from a second store = %+v, %d, %v\nwant %+v at 1", got, gotVer, err, want)
	}
}

func TestCommit_staleVersionIsRefusedAndAppendsNothing(t *testing.T) {
	s := open(t, t.TempDir())
	mustFact(t, s, entered("fix", "s1/a"))
	_, err := s.Commit(bounded(t), "fix", 0, Record{}, []kernel.Event{entered("fix", "s2/a")})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Commit err = %v, want ErrConflict", err)
	}
	if got := s.Events("fix"); len(got) != 1 {
		t.Errorf("log has %d events, want 1: a refused commit appends nothing", len(got))
	}
	if rec, ver, _ := s.Load(bounded(t), "fix"); ver != 1 || len(rec.Lane.Actors) != 1 {
		t.Errorf("record = %v at %d, want the first commit's alone", actors(rec), ver)
	}
}

func TestCommit_eventsGoToTheLogInCommitOrder(t *testing.T) {
	s := open(t, t.TempDir())
	for _, a := range []string{"s1/a", "s2/a", "s3/a"} {
		mustFact(t, s, entered("fix", a))
	}
	mustFact(t, s, entered("other", "s9/a"))
	var got []string
	for _, e := range s.Events("fix") {
		got = append(got, e.Actor)
	}
	if want := []string{"s1/a", "s2/a", "s3/a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fix log actors = %v, want %v: the other lane's event is not this lane's", got, want)
	}
}

func TestCommit_logLinesAreV1RecordsTheOldReaderUnderstands(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	mustFact(t, s, entered("fix", "s1/a"))
	names, _ := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	if len(names) != 1 {
		t.Fatalf("log files = %v, want one monthly file", names)
	}
	data, err := os.ReadFile(names[0])
	if err != nil {
		t.Fatal(err)
	}
	var e core.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &e); err != nil {
		t.Fatalf("line does not parse as a core.Event: %v\n%s", err, data)
	}
	if e.V != core.EventSchema || e.Kind != "lane.entered" || e.Lane != "fix" || e.Actor != "s1/a" || e.Seq == 0 {
		t.Errorf("core.Event = %+v, want v1 lane.entered for fix by s1/a with a seq", e)
	}
}

func TestLoad_checkpointBehindTheLogAfterACrashIsRefolded(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	mustFact(t, s, entered("fix", "s1/a"))

	crashing := open(t, dir)
	errCrash := errors.New("killed between the append and the checkpoint")
	crashing.afterAppend = func() error { return errCrash }
	if _, err := fact(t, crashing, entered("fix", "s2/a")); !errors.Is(err, errCrash) {
		t.Fatalf("crashing Commit err = %v, want the crash", err)
	}
	if ck := readCk(t, crashing, "fix"); ck.Ver != 1 {
		t.Fatalf("checkpoint version = %d, want 1: the crash came before the replace", ck.Ver)
	}

	fresh := open(t, dir)
	rec, ver, err := fresh.Load(bounded(t), "fix")
	if err != nil || ver != 2 || len(rec.Lane.Actors) != 2 {
		t.Fatalf("Load after the crash = %v at %d, %v; want both actors at version 2", actors(rec), ver, err)
	}
	if got := mustFact(t, fresh, entered("fix", "s3/a")); got != 3 {
		t.Errorf("next commit version = %d, want 3", got)
	}
	if ck := readCk(t, fresh, "fix"); ck.Ver != 3 || len(ck.Rec.Lane.Actors) != 3 {
		t.Errorf("checkpoint = v%d with %d actors, want 3 and 3: the next commit heals it", ck.Ver, len(ck.Rec.Lane.Actors))
	}
}

func TestLoad_olderFoldVersionIsRebuiltFromTheLogNotAnError(t *testing.T) {
	s := open(t, t.TempDir())
	mustFact(t, s, entered("fix", "s1/a"))
	ck := readCk(t, s, "fix")
	ck.Fold = FoldVersion - 1
	ck.Rec.Lane.Actors = map[string]time.Time{"stale/x": t0}
	writeCk(t, s, "fix", ck)

	rec, ver, err := s.Load(bounded(t), "fix")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := rec.Lane.Actors["s1/a"]; !ok || len(rec.Lane.Actors) != 1 || ver != 1 {
		t.Errorf("Load = %v at %d, want the log's s1/a alone at version 1: an old fold's record is not trusted", actors(rec), ver)
	}
}

func TestCommit_newerFoldVersionAppendsEventsButNeverWritesTheCheckpoint(t *testing.T) {
	s := open(t, t.TempDir())
	mustFact(t, s, entered("fix", "s1/a"))
	ck := readCk(t, s, "fix")
	ck.Fold = FoldVersion + 1
	writeCk(t, s, "fix", ck)
	before, _ := os.ReadFile(s.checkpointPath("fix"))

	if ver := mustFact(t, s, entered("fix", "s2/a")); ver != 2 {
		t.Fatalf("commit version = %d, want 2", ver)
	}
	after, _ := os.ReadFile(s.checkpointPath("fix"))
	if string(before) != string(after) {
		t.Errorf("a binary with a lower fold version rewrote a newer checkpoint:\n%s\n%s", before, after)
	}
	if rec, ver, _ := s.Load(bounded(t), "fix"); ver != 2 || len(rec.Lane.Actors) != 2 {
		t.Errorf("Load = %v at %d, want both actors at 2: the log carries what the checkpoint was not given", actors(rec), ver)
	}
}

func TestLoad_unreadableCheckpointRebuildsAndSaysSoOnce(t *testing.T) {
	var said []string
	s := open(t, t.TempDir(), func(o *Options) { o.Warn = func(m string) { said = append(said, m) } })
	mustFact(t, s, entered("fix", "s1/a"))
	if err := os.WriteFile(s.checkpointPath("fix"), []byte(`{"f":1,"fold":1,"ver":`), 0o600); err != nil {
		t.Fatal(err)
	}

	rec, ver, err := s.Load(bounded(t), "fix")
	if err != nil || len(rec.Lane.Actors) != 1 {
		t.Fatalf("Load = %v, %v; want the log's record, no error", actors(rec), err)
	}
	if ver <= 1 {
		t.Errorf("version = %d, want past every version handed out before the loss (the lost checkpoint may have been ahead of the log)", ver)
	}
	if _, _, err := s.Load(bounded(t), "fix"); err != nil {
		t.Fatal(err)
	}
	if len(said) != 1 || !strings.Contains(said[0], "fix") {
		t.Errorf("warnings = %q, want exactly one naming the lane", said)
	}
	if _, err := s.Commit(bounded(t), "fix", ver, rec, nil); err != nil {
		t.Fatalf("Commit on the rebuilt version: %v", err)
	}
	if ck := readCk(t, s, "fix"); ck.Ver != ver+1 {
		t.Errorf("checkpoint version = %d, want %d: the next commit repairs the file", ck.Ver, ver+1)
	}
}

func TestLoad_newerStateFormatIsRefusedByName(t *testing.T) {
	s := open(t, t.TempDir())
	mustFact(t, s, entered("fix", "s1/a"))
	ck := readCk(t, s, "fix")
	ck.F = FormatVersion + 1
	writeCk(t, s, "fix", ck)
	_, _, err := s.Load(bounded(t), "fix")
	if err == nil || !strings.Contains(err.Error(), "state format 2") {
		t.Errorf("Load err = %v, want one naming state format 2", err)
	}
}

func TestRebuild_foldsTheLogIntoACheckpoint(t *testing.T) {
	s := open(t, t.TempDir())
	mustFact(t, s, entered("fix", "s1/a"))
	mustFact(t, s, entered("fix", "s2/a"))
	if err := os.Remove(s.checkpointPath("fix")); err != nil {
		t.Fatal(err)
	}
	rec, ver, err := s.Rebuild(bounded(t), "fix")
	if err != nil || ver != 2 || len(rec.Lane.Actors) != 2 {
		t.Fatalf("Rebuild = %v at %d, %v; want both actors at 2", actors(rec), ver, err)
	}
	if ck := readCk(t, s, "fix"); ck.Ver != 2 || ck.Fold != FoldVersion || ck.F != FormatVersion || !reflect.DeepEqual(ck.Rec, rec) {
		t.Errorf("checkpoint = %+v, want the rebuilt record at version 2 stamped f%d fold%d", ck, FormatVersion, FoldVersion)
	}
}

func TestCommit_heldLaneLockIsRefusedWithinTheBoundNotWaitedOn(t *testing.T) {
	s := open(t, t.TempDir(), func(o *Options) { o.LockWait = 100 * time.Millisecond })
	f, err := core.OpenLockFile(s.lockPath("fix"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !core.TryLockExclusive(f) {
		t.Fatal("could not take the lane lock to hold it")
	}
	start := time.Now()
	_, err = s.Commit(bounded(t), "fix", 0, Record{}, []kernel.Event{entered("fix", "s1/a")})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Commit under a held lock err = %v, want ErrConflict", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("Commit waited %v for the lock, want it bounded near 100ms", took)
	}
	if got := s.Events("fix"); len(got) != 0 {
		t.Errorf("log has %d events, want none: nothing is appended without the lock", len(got))
	}
}

func TestLaneFileName_isFilesystemSafeAndNeverCollides(t *testing.T) {
	lanes := []string{"fix", "Fix", "FIX", "lane/fix", `lane\fix`, "lane%2Ffix", "@trunk", "con", "nul.x", "a:b", "x.", "ü", strings.Repeat("long/", 60), strings.Repeat("long/", 60) + "2"}
	seen := map[string]string{}
	for _, lane := range lanes {
		name := laneFileName(lane)
		if strings.ContainsAny(name, `/\:*?"<>|`) || name != strings.ToLower(name) || strings.HasSuffix(name, ".") || len(name) > 100 {
			t.Errorf("laneFileName(%q) = %q: not safe on a case-insensitive, path-limited filesystem", lane, name)
		}
		switch strings.SplitN(name, ".", 2)[0] {
		case "con", "nul", "prn", "aux":
			t.Errorf("laneFileName(%q) = %q is a Windows device name", lane, name)
		}
		if other, dup := seen[name]; dup {
			t.Errorf("lanes %q and %q share the file name %q", lane, other, name)
		}
		seen[name] = lane
	}
}

// told is what the agent has been told: deliveries append no event, so the
// checkpoint is the only place they live.
var told = []render.Delivery{{ID: "line-1", Hook: render.HookPostToolBatch, Actor: "s1/a"}}

func TestCommit_whatTheAgentWasToldSurvivesLaterFactsAndTheCrashRefold(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	mustFact(t, s, entered("fix", "s1/a"))
	rec, ver, _ := s.Load(bounded(t), "fix")
	rec.Delivered = told
	if _, err := s.Commit(bounded(t), "fix", ver, rec, nil); err != nil {
		t.Fatal(err)
	}

	crashing := open(t, dir)
	errCrash := errors.New("killed")
	crashing.afterAppend = func() error { return errCrash }
	if _, err := fact(t, crashing, entered("fix", "s2/a")); !errors.Is(err, errCrash) {
		t.Fatalf("err = %v, want the crash", err)
	}
	got, _, err := open(t, dir).Load(bounded(t), "fix")
	if err != nil || len(got.Lane.Actors) != 2 || !reflect.DeepEqual(got.Delivered, told) {
		t.Errorf("Load after the crash = %v actors, delivered %v, %v; want both actors and %v: a refold keeps deliveries", actors(got), got.Delivered, err, told)
	}
}

func TestRebuild_keepsWhatTheAgentWasToldWhenTheCheckpointIsReadable(t *testing.T) {
	s := open(t, t.TempDir())
	mustFact(t, s, entered("fix", "s1/a"))
	rec, ver, _ := s.Load(bounded(t), "fix")
	rec.Delivered = told
	if _, err := s.Commit(bounded(t), "fix", ver, rec, nil); err != nil {
		t.Fatal(err)
	}
	ck := readCk(t, s, "fix")
	ck.Fold = FoldVersion - 1
	writeCk(t, s, "fix", ck)
	got, _, err := s.Rebuild(bounded(t), "fix")
	if err != nil || !reflect.DeepEqual(got.Delivered, told) {
		t.Errorf("Rebuild = delivered %v, %v; want %v: the log cannot give deliveries back, the old checkpoint can", got.Delivered, err, told)
	}
}

func TestLoad_aLostCheckpointMakesLinesDueAgainNotLost(t *testing.T) {
	s := open(t, t.TempDir())
	mustFact(t, s, entered("fix", "s1/a"))
	rec, ver, _ := s.Load(bounded(t), "fix")
	rec.Delivered = told
	if _, err := s.Commit(bounded(t), "fix", ver, rec, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.checkpointPath("fix")); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Load(bounded(t), "fix")
	if len(got.Delivered) != 0 || len(got.Lane.Actors) != 1 {
		t.Errorf("Load = delivered %v, actors %v; want the lane back from the log and no deliveries (they are due again)", got.Delivered, actors(got))
	}
}
