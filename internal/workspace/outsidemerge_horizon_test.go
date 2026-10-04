package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/store"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// plantSweptThrough says retention removed the event months before at.
func plantSweptThrough(t *testing.T, repo string, at time.Time) {
	t.Helper()
	dir := core.EventLogDir(repo)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, store.SweptThroughFile), []byte(at.UTC().Format(time.RFC3339)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// With the old event months swept, a merge from before the retained log has no
// record to be matched against: the verb may well have recorded it. It is
// unknown, never an outside merge, and no escape is emitted for it.
func TestSyncSince_AMergeFromBeforeTheRetainedLogIsUnknownNotOutside(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	seed := revOf(t, clone, "HEAD")
	landOnOrigin(t, clone, "Old verb merge (#1089)")
	pullFastForward(t, clone)
	plantSweptThrough(t, clone, time.Now().Add(24*time.Hour))

	var out bytes.Buffer
	if err := SyncSince(clone, seed, false, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("SyncSince: %v", err)
	}
	if evs := emitted(t); len(evs) != 0 {
		t.Errorf("events = %+v, want none: a merge older than the retained log is unknown, not outside", evs)
	}
	if strings.Contains(out.String(), "recorded") {
		t.Errorf("output = %q, want nothing recorded", out.String())
	}
}

func TestSyncSince_AMergeAfterTheSweptMonthsIsStillRecorded(t *testing.T) {
	gateState(t)
	clone := repoWithOrigin(t)
	seed := revOf(t, clone, "HEAD")
	landOnOrigin(t, clone, "New outside merge (#1089)")
	pullFastForward(t, clone)
	plantSweptThrough(t, clone, time.Now().Add(-24*time.Hour))

	if err := SyncSince(clone, seed, false, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("SyncSince: %v", err)
	}
	if merges := outsideMerges(t); len(merges) != 1 {
		t.Errorf("outside merges = %+v, want the one inside the retained log recorded", merges)
	}
}
