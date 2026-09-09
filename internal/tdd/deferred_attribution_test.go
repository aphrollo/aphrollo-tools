package tdd

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestInfraFailureLine_NamesTheRunTheVerdictIsAbout pins the half of issue
// #583 measured on this box: a lane's hook printed
//
//	gate: → infra-failed in <root> (aphrollo: no build slot came free
//	("cargo nextest run -p engine_audio" in borld) — the code was NOT tested)
//
// Every name in that line belongs to somebody ELSE's job — the build that
// held the slot — so the verdict reads as if it were about engine_audio in
// borld, a repo the session had not touched. The blocker must still be named
// (that is the only route to "the box was full, wait or retry"), but the line
// has to say which run the verdict is a verdict ABOUT first.
func TestInfraFailureLine_NamesTheRunTheVerdictIsAbout(t *testing.T) {
	j := DeferredJob{
		Project: filepath.FromSlash("D:/Projects/aphrollo-tools"),
		Phase:   "run",
		Runner:  []string{"go", "test", "./internal/tdd"},
	}
	res := SuiteResult{Output: "aphrollo: no build slot came free (\"cargo nextest run -p engine_audio\" in borld)\n"}

	line := infraFailureLine(j.Project, j, res)

	if !strings.Contains(line, "go test ./internal/tdd") {
		t.Errorf("line = %q, want it to name the run this verdict is about (%q)", line, "go test ./internal/tdd")
	}
	if !strings.Contains(line, "run phase") {
		t.Errorf("line = %q, want it to name the phase whose setup failed", line)
	}
	if !strings.Contains(line, "engine_audio") {
		t.Errorf("line = %q, want the blocking holder still named — the fix is attribution, never hiding", line)
	}
	if !strings.Contains(line, InfraFailed) || !strings.Contains(line, "NOT tested") {
		t.Errorf("line = %q, want the inconclusive verdict kept intact", line)
	}
}

// TestInfraFailureLine_StillSpeaksForAJobWithNoRunnerRecorded keeps the
// fallback honest: a record written before this field existed, or one whose
// setup failed before a runner was ever chosen, still gets the verdict and
// the reason — an unattributable line is better than no line.
func TestInfraFailureLine_StillSpeaksForAJobWithNoRunnerRecorded(t *testing.T) {
	res := SuiteResult{Output: "aphrollo: no build slot came free (holder unknown)\n"}

	line := infraFailureLine("/repo", DeferredJob{}, res)

	if !strings.Contains(line, InfraFailed) || !strings.Contains(line, "no build slot came free") {
		t.Errorf("line = %q, want the verdict and the reason even with nothing to attribute it to", line)
	}
}

// TestWaitDeferredEditJob_SaysWhichSessionStartedTheRun pins the other half
// of issue #583. `gate status --wait` is the one harvest that reads ACROSS
// sessions: it finds a job by project (it has no session id of its own to key
// on), harvests it and prints the verdict verbatim — with nothing in the line
// to say the run belonged to somebody else's edit. A verdict is answerable to
// the session that asked for it, so the line has to carry that session's id.
func TestWaitDeferredEditJob_SaysWhichSessionStartedTheRun(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir() // not a git repo -- headSHAFor(root) resolves to ""
	saveDeferredJob(DeferredJob{
		Project: root, Phase: "run", Dir: root, PID: 99,
		Runner: []string{"go", "test", "./..."}, Started: time.Now(),
		HeadSHA: "", FileHash: "hash1", Session: "sess-from-another-window",
	})
	loaded, ok := loadDeferredJob("sess-from-another-window", root)
	if !ok {
		t.Fatal("setup: expected the job to load back")
	}
	writePhaseResult(loaded.Result, PhaseOutcome{ExitCode: 0, Seconds: 1})

	advisory, ok := WaitDeferredEditJob(root)

	if !ok {
		t.Fatalf("advisory = %q, ok = false; want the finished job harvested", advisory)
	}
	if !strings.Contains(advisory, "sess-from-another-window") {
		t.Errorf("advisory = %q, want it to name the session that started the run", advisory)
	}
}
