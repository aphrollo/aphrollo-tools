package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/store"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// sweptRepo is statsRepo with the retention's marker saying every month
// before 2026-06 was swept.
func sweptRepo(t *testing.T) (repo string, denySeq int64) {
	t.Helper()
	repo = statsRepo(t, map[time.Duration]tdd.Event{time.Hour: denyEvent("lane/a", "r1")})
	dir := core.EventLogDir(repo)
	if err := os.WriteFile(filepath.Join(dir, store.SweptThroughFile), []byte("2026-06-01T00:00:00Z"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, e := range tdd.ReadEvents(repo) {
		denySeq = e.Seq
	}
	return repo, denySeq
}

func TestStats_SaysWhereTheRetainedLogBegins(t *testing.T) {
	repo, _ := sweptRepo(t)
	code, out, errOut := runStatsCmd(t, "--repo", repo)
	if code != 0 || !strings.Contains(out, "events since 2026-06") {
		t.Errorf("stats exit %d, stderr %q, output:\n%s\nwant the horizon line \"events since 2026-06\"", code, errOut, out)
	}
	code, out, errOut = runStatsCmd(t, "--repo", repo, "--json")
	if code != 0 || strings.Contains(out, "events since") || !strings.Contains(errOut, "events since 2026-06") {
		t.Errorf("stats --json exit %d: the horizon belongs on stderr, not in the JSON\nstdout:\n%s\nstderr: %q", code, out, errOut)
	}
}

func TestStats_SaysNothingOfAHorizonWhenNothingWasSwept(t *testing.T) {
	repo := statsRepo(t, map[time.Duration]tdd.Event{time.Hour: denyEvent("lane/a", "r1")})
	if _, out, errOut := runStatsCmd(t, "--repo", repo); strings.Contains(out+errOut, "events since") {
		t.Errorf("a repo nothing was swept from printed a horizon:\n%s%s", out, errOut)
	}
}

func TestWhy_SaysWhereTheRetainedLogBegins(t *testing.T) {
	repo, seq := sweptRepo(t)
	if seq == 0 {
		t.Fatal("no seeded event")
	}
	_, out, _ := runWhyCmd(t, strconv.FormatInt(seq, 10), "--repo", repo)
	if !strings.Contains(out, "events since 2026-06") {
		t.Errorf("why output lacks the horizon:\n%s", out)
	}
	code, _, errOut := runWhyCmd(t, "999999", "--repo", repo)
	if code != 1 || !strings.Contains(errOut, "events since 2026-06") {
		t.Errorf("why of a seq not in the log: exit %d, stderr %q; want the horizon named, an older event may have been swept", code, errOut)
	}
}

func TestRunTDDGC_DryLeavesNoLanesDirectoryBehind(t *testing.T) {
	f := newStoreFixture(t)
	dir := core.EventLogDir(f.repo)
	if err := os.RemoveAll(filepath.Join(dir, "lanes")); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runGCCmd(t, "--repo", f.repo, "--dry")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "lanes")); !os.IsNotExist(err) {
		t.Errorf("a dry run created the lanes directory: %v", err)
	}
}
