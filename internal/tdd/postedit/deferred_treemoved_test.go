package postedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A deferred `go test` run of four packages came back as
// "writing-test (0 tests ran, 261.4s — nothing was tested)" while a rebase was
// rewriting the tree under it. What classified it: classifyRunOutcome reads a
// passing Go run whose packages all say "no tests to run" as writing-test. A
// rebase leaves exactly that output (the files it ran against were swapped
// out), and the word then tells the session its own scaffolding is the cause.
// A run whose tree moved is not a verdict on anything: it says so.

// movedTreeEdit runs one deferred go edit whose run phase prints output and,
// when moveTree is set, rewrites the edited file while the phase is going.
func movedTreeEdit(t *testing.T, output string, moveTree bool) string {
	t.Helper()
	t.Setenv("TRELLIS_DATA", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeGoRepo(t)
	target := filepath.Join(root, "doc_test.go")
	if err := os.WriteFile(target, []byte("package m\n\nimport \"testing\"\n\nfunc TestEdited(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := spawnPhaseFn
	spawnPhaseFn = func(j DeferredJob) (DeferredJob, bool) {
		j.PID = 4200
		j.Started = time.Now()
		saveDeferredJob(j)
		j, _ = loadDeferredJob(j.Session, j.Project)
		if moveTree && j.Phase == "run" {
			if err := os.WriteFile(target, []byte("package m\n\nimport \"testing\"\n\nfunc TestRebased(t *testing.T) {}\n"), 0o644); err != nil {
				t.Error(err)
			}
		}
		if err := os.WriteFile(j.Log, []byte(output), 0o600); err != nil {
			t.Error(err)
		}
		writePhaseResult(j.Result, PhaseOutcome{ExitCode: 0, Seconds: 261.4})
		return j, true
	}
	t.Cleanup(func() { spawnPhaseFn = prev })
	EnableDeferredPhases(true)
	t.Cleanup(func() { EnableDeferredPhases(false) })

	return PostEdit(postPayload("Edit", target), nil)
}

const noTestsRan = "ok  \texample.com/m\t0.012s [no tests to run]\n"

func TestPostEditDeferred_ATreeThatMovedDuringTheRunIsNotWritingTest(t *testing.T) {
	got := movedTreeEdit(t, noTestsRan, true)

	if !strings.Contains(got, "tree moved during the run, not tested") {
		t.Fatalf("advisory = %q, want it to say the tree moved during the run and nothing was tested", got)
	}
	if strings.Contains(got, "writing-test") {
		t.Fatalf("advisory = %q, a run over a moving tree must not be called writing-test", got)
	}
}

// The control: the same output over a tree that held still IS writing-test.
func TestPostEditDeferred_ATreeThatHeldStillKeepsWritingTest(t *testing.T) {
	got := movedTreeEdit(t, noTestsRan, false)

	if !strings.Contains(got, "writing-test") {
		t.Fatalf("advisory = %q, want writing-test for a run that ran no test over an unmoved tree", got)
	}
	if strings.Contains(got, "tree moved") {
		t.Fatalf("advisory = %q, said the tree moved when it held still", got)
	}
}
