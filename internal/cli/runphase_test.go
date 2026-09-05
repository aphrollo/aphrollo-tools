package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestRunPhase_RunsTheJobAndRecordsItsOutcome pins the wrapper the whole
// deferral rests on: a detached phase is `aphrollo gate runphase --job <file>`,
// and when its command exits the wrapper writes the RESULT file that tells the
// next hook the phase is over. Without it a hook cannot tell "still building"
// from "finished while nobody was looking".
func TestRunPhase_RunsTheJobAndRecordsItsOutcome(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "phase.log")
	result := filepath.Join(dir, "phase.result.json")
	job := map[string]any{
		"project": dir, "phase": "run", "dir": dir,
		"runner": []string{"go", "version"},
		"log":    log, "result": result,
	}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	jobPath := filepath.Join(dir, "job.json")
	if err := os.WriteFile(jobPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if code := runGate([]string{"runphase", "--job", jobPath}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("runphase exit = %d, want 0 (the wrapper reports through its result file, never its exit code)", code)
	}

	raw, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("no result file: %v — the next hook would wait forever", err)
	}
	var got tdd.PhaseOutcome
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.ExitCode != 0 {
		t.Fatalf("exit code = %d, want the command's own 0", got.ExitCode)
	}
	logged, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(logged), "go version") {
		t.Fatalf("log = %q (err %v), want the phase's own output captured for the harvest", logged, err)
	}
}

// TestRunPhase_MissingJobFlagExitsTwo pins the usage-error contract: a
// flag-parse failure or a missing --job is a caller mistake, not a phase
// outcome, and the CLI's usage-error convention is exit 2 — 0 would tell a
// script the (nonexistent) phase succeeded.
func TestRunPhase_MissingJobFlagExitsTwo(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := runGate([]string{"runphase"}, strings.NewReader(""), &out, &errBuf); code != 2 {
		t.Fatalf("runphase with no --job exit = %d, want 2 (usage error)", code)
	}
}

// TestRunPhase_UnknownFlagExitsTwo covers the sibling case: flag.Parse itself
// failing (an unknown flag), not just an empty --job.
func TestRunPhase_UnknownFlagExitsTwo(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := runGate([]string{"runphase", "--nope"}, strings.NewReader(""), &out, &errBuf); code != 2 {
		t.Fatalf("runphase with an unknown flag exit = %d, want 2 (usage error)", code)
	}
}

// TestPostToolUse_EnablesDeferredPhases pins the wiring: the real edit hook is
// the ONE caller that may leave work running past its budget, so it turns
// deferral on. Nothing else does — a unit test's injected runner must never be
// replaced by a real process spawn.
func TestPostToolUse_EnablesDeferredPhases(t *testing.T) {
	t.Cleanup(func() { tdd.EnableDeferredPhases(false) })
	if tdd.DeferredPhasesEnabled() {
		t.Fatal("deferral must be off until a hook asks for it")
	}
	var out, errBuf bytes.Buffer
	runGate([]string{"posttooluse"}, strings.NewReader(`{"tool_name":"Read"}`), &out, &errBuf)
	if !tdd.DeferredPhasesEnabled() {
		t.Fatal("the edit hook must run its phases deferrable")
	}
}
