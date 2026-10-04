package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// storeFixture is a repo (no git: its state files under the no-repo key) with
// a stale event month and a ratchet cache file.
type storeFixture struct {
	repo, events, cache string
}

func newStoreFixture(t *testing.T) storeFixture {
	t.Helper()
	gateConfigDir(t)
	repo := t.TempDir()
	f := storeFixture{repo: repo}
	f.events = filepath.Join(core.EventLogDir(repo), "events-2020-01.jsonl")
	mkAgedFile(t, f.events, "{}\n", time.Hour)
	f.cache = filepath.Join(tdd.StateDir(), "ratchet-cache", "scan.json")
	mkAgedFile(t, f.cache, "0123456789", 30*24*time.Hour)
	return f
}

func TestRunTDDGC_DryPrintsTheStateSizesAndRemovesNothing(t *testing.T) {
	f := newStoreFixture(t)
	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", f.repo, "--dry"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"events", "lanes", "verdicts", "cache", "200.0 MB", "events-2020-01.jsonl"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry output lacks %q:\n%s", want, out)
		}
	}
	for _, p := range []string{f.events, f.cache} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("a dry run removed %s", p)
		}
	}
}

func TestRunTDDGC_SweepsTheStateDirectory(t *testing.T) {
	f := newStoreFixture(t)
	var stdout, stderr bytes.Buffer
	if code := runGateGC([]string{"--repo", f.repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for p, want := range map[string]bool{f.events: false, f.cache: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s present = %v, want %v\n%s", filepath.Base(p), err == nil, want, stdout.String())
		}
	}
}
