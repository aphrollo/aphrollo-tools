package tdd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A deferred job is recorded under the tree the EDIT touched — a lane
// worktree — while the harness resets the session's shell cwd to the primary
// checkout between calls. A prompt-time harvest keyed on that cwd looked the
// job up under the primary, found nothing, and the verdict the BUILDING line
// promised never arrived at any later hook (issue #732). The session's own
// jobs are found by the session, wherever its shell was left standing.
func TestHandlePrompt_ReportsTheSessionsLaneJobWhenTheCwdIsThePrimary(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, lane := goPrimaryWithLane(t)

	saveDeferredJob(DeferredJob{
		Project: lane, Session: "s732", Phase: "run", Dir: lane,
		Started: time.Now().Add(-time.Minute), HeadSHA: headSHAFor(lane),
		FileHash: sourceIdentity(lane, ""), Runner: []string{"go", "test", "./..."},
	})
	job, _ := loadDeferredJob("s732", lane)
	writePhaseResult(job.Result, PhaseOutcome{ExitCode: 0, Seconds: 3})

	raw, err := json.Marshal(map[string]string{"prompt": "what now?", "session_id": "s732", "cwd": primary})
	if err != nil {
		t.Fatal(err)
	}
	res := HandlePrompt(raw)

	if !strings.Contains(res.Message, "gate: deferred") || !strings.Contains(res.Message, lane) {
		t.Fatalf("prompt context = %q, want the finished lane job's verdict for %s", res.Message, lane)
	}
	if _, ok := loadDeferredJob("s732", lane); ok {
		t.Fatal("a reported job must be cleared")
	}
}

// The BUILDING line's escape was a bare `aphrollo gate status --wait`, which
// resolves the checkout from the shell cwd — the primary, after a reset —
// and answered "no deferred edit job recorded for this checkout" while the
// lane's build was still running (issue #732). The command the line offers
// has to name the tree its own job was recorded under.
func TestBuildingLine_WaitCommandNamesTheTreeTheJobWasRecordedFor(t *testing.T) {
	crate := filepath.Join(t.TempDir(), "lane", "crates", "forge_powertrain")

	for _, line := range []string{buildingLine(crate, "build", 0), buildingLine(crate, "build", 118*time.Second)} {
		want := "aphrollo gate status --wait " + filepath.ToSlash(crate)
		if !strings.Contains(line, want) {
			t.Errorf("line = %q, want it to offer %q", line, want)
		}
	}
}
