package store

import (
	"bytes"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// runWriters starts two writer processes (this test binary, see writerMain) on
// dir at once and waits for both. crashAt names, per writer id, the write after
// which that writer dies; it returns each writer's exit error.
func runWriters(t *testing.T, dir string, crashAt map[string]int) map[string]error {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type child struct {
		c   *run.Child
		out *bytes.Buffer
	}
	children := map[string]child{}
	for _, id := range []string{"1", "2"} {
		out := &bytes.Buffer{}
		env := append(os.Environ(), writerDirEnv+"="+dir, writerIDEnv+"="+id, writerCrashEnv+"="+strconv.Itoa(crashAt[id]))
		c, err := run.StartLight(run.Spec{Name: exe, Env: env, Stdout: out, Stderr: out, Timeout: 2 * time.Minute})
		if err != nil {
			t.Fatalf("start writer %s: %v", id, err)
		}
		children[id] = child{c, out}
		t.Cleanup(c.Close)
	}
	results := map[string]error{}
	for id, ch := range children {
		results[id] = ch.c.Wait()
		if results[id] != nil && crashAt[id] == 0 {
			t.Errorf("writer %s failed: %v\n%s", id, results[id], ch.out)
		}
	}
	return results
}

func TestCommit_twoProcessesLoseNoUpdate(t *testing.T) {
	dir := t.TempDir()
	runWriters(t, dir, nil)

	s := open(t, dir)
	rec, ver, err := s.Load(bounded(t), "fix")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	const want = 2 * writesPerProc
	if events := len(s.Events("fix")); len(rec.Lane.Actors) != want || ver != want || events != want {
		t.Errorf("%d actors at version %d with %d events, want %d of each: a commit of one process overwrote the other's", len(rec.Lane.Actors), ver, events, want)
	}
	if ck := readCk(t, s, "fix"); ck.Ver != want || len(ck.Rec.Lane.Actors) != want {
		t.Errorf("checkpoint holds %d actors at version %d, want %d at %d", len(ck.Rec.Lane.Actors), ck.Ver, want, want)
	}
}

func TestCommit_aProcessKilledBetweenLogAndCheckpointLosesNothing(t *testing.T) {
	dir := t.TempDir()
	// Writer 1 dies after logging its 10th write; writer 2 finishes its own.
	res := runWriters(t, dir, map[string]int{"1": 10})
	if res["1"] == nil {
		t.Fatal("writer 1 was meant to be killed mid-commit but exited cleanly")
	}

	s := open(t, dir)
	rec, _, err := s.Load(bounded(t), "fix")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := rec.Lane.Actors["p1/9"]; !ok {
		t.Errorf("p1/9, killed after its log append, is missing from %v", actors(rec))
	}
	if want := 10 + writesPerProc; len(rec.Lane.Actors) != want {
		t.Errorf("%d actors after the kill, want %d: writer 1's 10 writes and all of writer 2's", len(rec.Lane.Actors), want)
	}
}
