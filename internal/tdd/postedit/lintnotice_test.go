package postedit

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const lintNoticeSession = "lint-notice-sess"

// finishedGreenRun records a run of session that finished green and has not been
// harvested, with run id id, and answers the job as the lint will see it.
func finishedGreenRun(t *testing.T, id string) (DeferredJob, heldRun) {
	t.Helper()
	root := mkProject(t, "go.mod")
	target := filepath.Join(root, "x.go")
	mustWrite(t, target, "package x\n")
	saveDeferredJob(DeferredJob{
		Project: root, Session: lintNoticeSession, Phase: "run", Dir: root, PID: 4242,
		Started: time.Now(), File: target,
		HeadSHA: headSHAFor(root), FileHash: sourceIdentity(root, target),
		Runner: []string{"go", "test", "./..."},
	})
	j, _ := loadDeferredJob(lintNoticeSession, root)
	mustWrite(t, j.Log, "ok  \tx\t0.01s\n")
	writePhaseResult(j.Result, PhaseOutcome{ExitCode: 0, Seconds: 1, RunID: id})
	markLatestRun(j, id)
	return j, heldRun{ran: true, id: id, key: "tree1"}
}

func lintLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.Contains(l, "lint") {
			out = append(out, l)
		}
	}
	return out
}

func armLint(t *testing.T, argv []string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withIsolatedBuildLock(t)
	notLoaded(t, false)
	stubPhaseLint(t, argv)
}

// A lint that finishes after its run was reported is delivered at the next
// hook as a line of its own, once, and never as a deny.
func TestHarvestLintNotices_ALintFinishingAfterItsRunIsDeliveredOnce(t *testing.T) {
	armLint(t, lintFindingCmd("x.go:3:1: unused thing (unused)"))
	j, held := finishedGreenRun(t, "run1")
	if first := harvestSessionJobs(lintNoticeSession); len(lintLines(first)) != 0 || len(first) != 1 {
		t.Fatalf("setup: the run's own harvest = %q, want its green line alone", first)
	}

	runPhaseLint(j, held)
	got := harvestSessionJobs(lintNoticeSession)

	if len(got) != 1 || !strings.HasPrefix(got[0], "gate: lint ") ||
		!strings.Contains(got[0], "→ 1 finding a commit would refuse: x.go:3:1: unused thing (unused)") || !strings.Contains(got[0], j.Project) {
		t.Fatalf("lines = %q, want one gate: lint line naming the finding and the tree", got)
	}
	if again := harvestSessionJobs(lintNoticeSession); len(again) != 0 {
		t.Fatalf("second harvest = %q, want nothing: the notice is deleted once delivered", again)
	}
}

// A newer run of the unit has started since: its own lint will report on the
// code as it is now, so the older lint's line is dropped, and so is its notice.
func TestHarvestLintNotices_ASupersededLintIsDropped(t *testing.T) {
	armLint(t, lintFindingCmd("x.go:3:1: unused thing (unused)"))
	j, held := finishedGreenRun(t, "run1")
	harvestSessionJobs(lintNoticeSession)
	runPhaseLint(j, held)
	markLatestRun(j, "run2")

	if got := harvestSessionJobs(lintNoticeSession); len(got) != 0 {
		t.Fatalf("lines = %q, want none: run2 started after run1", got)
	}
	if again := harvestLintNotices(lintNoticeSession); len(again) != 0 {
		t.Fatalf("notices = %q, want the dropped one deleted", again)
	}
}

// A clean lint, and a lint that could not judge, print nothing.
func TestHarvestLintNotices_ACleanLintPrintsNothing(t *testing.T) {
	for name, argv := range map[string][]string{
		"clean":      lintExitCmd(0),
		"contention": lintExitCmd(3),
	} {
		t.Run(name, func(t *testing.T) {
			armLint(t, argv)
			j, held := finishedGreenRun(t, "run1")
			harvestSessionJobs(lintNoticeSession)

			runPhaseLint(j, held)

			if got := harvestSessionJobs(lintNoticeSession); len(got) != 0 {
				t.Fatalf("lines = %q, want none", got)
			}
		})
	}
}

// A lint that finished before its run was consumed rides the run's line and is
// not said again as a line of its own.
func TestHarvestLintNotices_ALintThatRodeTheRunsLineIsNotSaidTwice(t *testing.T) {
	armLint(t, lintFindingCmd("x.go:3:1: unused thing (unused)"))
	j, held := finishedGreenRun(t, "run1")
	runPhaseLint(j, held)

	got := harvestSessionJobs(lintNoticeSession)

	if len(got) != 1 || !strings.Contains(got[0], "→ green") || !strings.Contains(got[0], "x.go:3:1: unused thing (unused)") {
		t.Fatalf("lines = %q, want the finding once, on the green run's own line", got)
	}
	if again := harvestSessionJobs(lintNoticeSession); len(again) != 0 {
		t.Fatalf("second harvest = %q, want nothing", again)
	}
}

// lintExitCmd is a stand-in linter that prints nothing and exits n.
func lintExitCmd(n int) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "exit /b " + strconv.Itoa(n)}
	}
	return []string{"sh", "-c", "exit " + strconv.Itoa(n)}
}

// A notice and a latest-run mark nobody collected are swept with the rest of
// the lane's state once old, and a fresh one is kept.
func TestSweepLaneState_DropsOldLintNoticesAndMarks(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	oldNotice := filepath.Join(lintNoticeDir(), "s-old.json")
	oldMark := filepath.Join(lintLatestDir(), "s-old")
	freshNotice := filepath.Join(lintNoticeDir(), "s-fresh.json")
	for _, p := range []string{oldNotice, oldMark, freshNotice} {
		mustWrite(t, p, "{}")
	}
	old := time.Now().Add(-2 * seenKeep)
	for _, p := range []string{oldNotice, oldMark} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	sweepLaneState(time.Now())

	for _, p := range []string{oldNotice, oldMark} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s survived the sweep", p)
		}
	}
	if _, err := os.Stat(freshNotice); err != nil {
		t.Error("a fresh notice was swept")
	}
}
