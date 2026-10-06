package ghworkflow

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A run's scratch directory is removed by Run's own defer, which a process
// ended by a signal never reaches. RemoveLiveScratch is what the signal path
// calls so the directory goes anyway.

func newScratch(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "aphrollo-ci-run-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	if err := os.WriteFile(filepath.Join(d, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRemoveLiveScratch_RemovesTheScratchOfARunStillInProgressOnce(t *testing.T) {
	d := newScratch(t)
	registerScratch(d)
	RemoveLiveScratch(0)
	if _, err := os.Stat(d); !os.IsNotExist(err) {
		t.Errorf("the registered scratch %s outlived RemoveLiveScratch (stat err %v)", d, err)
	}
	if got := liveScratchDirs(); len(got) != 0 {
		t.Errorf("live scratch after removal = %q, want none", got)
	}
	RemoveLiveScratch(0) // nothing left: must not fail or hang
}

func TestRemoveLiveScratch_LeavesAFinishedRunsDirectoryToItsOwnRemoval(t *testing.T) {
	d := newScratch(t)
	registerScratch(d)
	unregisterScratch(d)
	RemoveLiveScratch(0)
	if _, err := os.Stat(d); err != nil {
		t.Errorf("a run that already unregistered owns its removal, but RemoveLiveScratch removed %s: %v", d, err)
	}
}

func TestRemoveLiveScratch_WaitsForARunThatIsFinishingBeforeItRemovesAnything(t *testing.T) {
	d := newScratch(t)
	registerScratch(d)
	prev := pollSleep
	pollSleep = func(wait time.Duration) {
		if wait <= 0 || wait > time.Second {
			t.Errorf("the wait between looks at the live runs was %v, want a short positive one", wait)
		}
		unregisterScratch(d)
	}
	t.Cleanup(func() { pollSleep = prev })
	start := time.Now()
	RemoveLiveScratch(30 * time.Second)
	if took := time.Since(start); took > 20*time.Second {
		t.Errorf("RemoveLiveScratch took %v: it must return as soon as the runs have finished", took)
	}
	if _, err := os.Stat(d); err != nil {
		t.Errorf("the finishing run owns its removal, but %s was removed: %v", d, err)
	}
}

func TestRun_ItsScratchIsLiveWhileItRunsAndGoneWhenItEnds(t *testing.T) {
	src := `
on: pull_request
jobs:
  hold:
    steps:
      - run: |
          echo "$GOPATH" > gopath.txt
          sleep 60
`
	wf, _, err := Parse("ci.yml", src)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = Run(ctx, []*Workflow{wf}, Options{Dir: dir, Out: &out, StepTimeout: time.Minute})
	}()
	var scratch string
	started, stopWaiting := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopWaiting()
	for scratch == "" {
		select {
		case <-started.Done():
			t.Fatalf("the step never started:\n%s", out.String())
		case <-time.After(50 * time.Millisecond):
		}
		if b, err := os.ReadFile(filepath.Join(dir, "gopath.txt")); err == nil && strings.Contains(string(b), "isolation") {
			scratch = filepath.Dir(filepath.Dir(strings.TrimSpace(string(b)))) // scratch/isolation/gopath
		}
	}
	live := false
	for _, d := range liveScratchDirs() {
		live = live || under(scratch, d)
	}
	if !live {
		t.Errorf("the running run's scratch %s is not among the live scratch %q", scratch, liveScratchDirs())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not end after its context was cancelled")
	}
	for _, d := range liveScratchDirs() {
		if under(scratch, d) {
			t.Errorf("the finished run's scratch %s is still live", d)
		}
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("the finished run's scratch %s was not removed (stat err %v)", scratch, err)
	}
}

// A sweep that scans where a run makes its scratch would, in a test binary on
// Windows, scan the real root of the system drive. Under test the base is the
// test's own temp dir, whatever the test did or did not override.
func TestScratchBase_UnderATestIsTheTestsOwnTempDir(t *testing.T) {
	if got, want := ScratchBase(), os.TempDir(); got != want {
		t.Errorf("ScratchBase() = %q under test, want the test's temp dir %q", got, want)
	}
}
