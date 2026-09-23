package tdd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPostEdit_SpawnFailureIsReportedNotDeferred pins a lie: when the spawn
// itself fails, nothing is running, so BUILDING is false — and the job record
// written with a zero Started expires instantly and stamps a timeout streak
// for a run that never happened. It reports InfraFailed, never RedBogus: the
// tooling failed to start, not the test's own setup (issue #350) — a session
// pattern-matching on "red-bogus" would otherwise go looking for a broken
// test that does not exist.
func TestPostEdit_SpawnFailureIsReportedNotDeferred(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := mkProject(t, "Cargo.toml")
	prev := spawnPhaseFn
	spawnPhaseFn = func(j DeferredJob) (DeferredJob, bool) { return j, false }
	t.Cleanup(func() { spawnPhaseFn = prev })
	EnableDeferredPhases(true)
	t.Cleanup(func() { EnableDeferredPhases(false) })

	got := PostEdit(postPayload("Edit", root+"/src/widget.rs"), fakeRun(true, "ok"))

	if strings.Contains(got, "BUILDING") {
		t.Fatalf("advisory = %q, want a failure — nothing was started", got)
	}
	if strings.Contains(got, "red-bogus") {
		t.Fatalf("advisory = %q, must not read as a broken TEST setup — nothing was ever tested", got)
	}
	if !strings.Contains(got, InfraFailed) {
		t.Fatalf("advisory = %q, want the run reported as %s", got, InfraFailed)
	}
	if _, ok := loadDeferredJob("sess-post", root); ok {
		t.Fatal("a failed spawn must leave no job record to expire and stamp a streak")
	}
}

// TestPromptHarvest_RejectsAResultTheWorktreeHasMovedPast pins two gaps at
// once. The prompt-side harvest checked only Dirty and HEAD, so a stale
// answer was reported as current; and the identity it would have checked was
// HEAD plus the ONE edited file, which cannot see a change made outside the
// hook (another session's edit, a script, a rebase). The identity is the
// whole worktree state.
func TestPromptHarvest_RejectsAResultTheWorktreeHasMovedPast(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "go.mod", "module x\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "init")

	j := DeferredJob{
		Project: root, Session: "s1", Phase: "run", Dir: root, Runner: []string{"go", "test", "./..."},
		HeadSHA: headSHAFor(root), FileHash: worktreeStateHash(root), Started: time.Now(),
	}
	saveDeferredJob(j)
	saved, _ := loadDeferredJob("s1", root)
	writePhaseResult(saved.Result, PhaseOutcome{ExitCode: 0, Seconds: 1})

	// A change nothing told the hook about.
	write(t, root, "widget.go", "package x\n")

	if got := promptHarvest("s1"); got != "" {
		t.Fatalf("reported %q for a result the worktree has moved past", got)
	}
}

// TestWriteFileAtomic_ReadersNeverSeeAPartialFile pins the race the harvest
// polls into every 200 ms: a result written IN PLACE is visible truncated,
// and a truncated read is indistinguishable from "not finished yet" — which
// turns a finished phase into a wait for the 600s ceiling. The payload is
// deliberately large: no filesystem writes a megabyte atomically.
func TestWriteFileAtomic_ReadersNeverSeeAPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.result.json")
	payload := bytes.Repeat([]byte("x"), 1<<18)

	done := make(chan struct{})
	bad := make(chan int, 1)
	go func() {
		defer close(done)
		for range 50 {
			if err := writeFileAtomic(path, payload); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for {
		select {
		case <-done:
			select {
			case n := <-bad:
				t.Fatalf("a reader saw %d bytes of a %d-byte file", n, len(payload))
			default:
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("left %d files, want just the result — the temp file must be renamed away", len(entries))
			}
			return
		default:
			if data, err := os.ReadFile(path); err == nil && len(data) != len(payload) {
				select {
				case bad <- len(data):
				default:
				}
			}
		}
	}
}

// TestWritePhaseResult_RoundTripsThroughTheAtomicWrite keeps the wrapper on
// that path: the one writer of the liveness signal is also the one the
// harvest races.
func TestWritePhaseResult_RoundTripsThroughTheAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.result.json")
	writePhaseResult(path, PhaseOutcome{ExitCode: 3, Seconds: 2})

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("left %d files, want just the result", len(entries))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out PhaseOutcome
	if err := json.Unmarshal(data, &out); err != nil || out.ExitCode != 3 {
		t.Fatalf("result = %+v (%v), want the exit code round-tripped", out, err)
	}
}

// TestPostEditDeferred_RecordsTheWorktreeIdentity pins what a job stores: a
// single file's hash cannot see an edit to any OTHER file, so a job started
// before an unrelated change still looked current at harvest time.
func TestPostEditDeferred_RecordsTheWorktreeIdentity(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "Cargo.toml", "[package]\nname = \"x\"\n")
	write(t, root, "src/widget.rs", "fn main() {}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "init")
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "0")
	fakePhases(t) // no outcome: the phase stays running, so the record survives

	PostEdit(postPayload("Edit", filepath.Join(root, "src", "widget.rs")), fakeRun(true, "ok"))

	j, ok := loadDeferredJob("sess-post", root)
	if !ok {
		t.Fatal("no job recorded")
	}
	if want := worktreeStateHash(root); j.FileHash != want {
		t.Fatalf("job identity = %q, want the worktree state hash %q", j.FileHash, want)
	}
}
