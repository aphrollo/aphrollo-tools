package tdd

import (
	"strings"
	"testing"
	"time"
)

// withFakeProcessLiveness makes every pid in alive report as running (so
// liveJobsAt's liveness filter never drops the fixture jobs these tests
// build), while paths hands back each pid's live executable path — the
// exact seam staleHolderNotice already uses (#311), reused here for the
// deploy-path half (#338).
func withFakeProcessLiveness(t *testing.T, paths map[int]string) {
	t.Helper()
	prevPID, prevExe := pidRunningFn, processExePathFn
	pidRunningFn = func(pid int) bool { _, ok := paths[pid]; return ok }
	processExePathFn = func(pid int) (string, bool) { p, ok := paths[pid]; return p, ok }
	t.Cleanup(func() { pidRunningFn, processExePathFn = prevPID, prevExe })
}

const replacedStalePath = `C:\bin\aphrollo.stale-1788569851.exe`

// A job whose process still resolves to the exact file self-install just
// renamed aside is exactly what #338 asks to name: it holds the box-wide
// mutation-run lock and its results predate the binary now installed.
func TestJobsRunningReplacedBinary_FindsAJobStillOnTheStalePath(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withFakeProcessLiveness(t, map[int]string{4242: replacedStalePath})
	saveMutantsJob(MutantsJob{Repo: "borld", Branch: "lane/object-reflect", PID: 4242, Started: time.Now()})

	jobs := JobsRunningReplacedBinary(replacedStalePath)
	if len(jobs) != 1 || jobs[0].PID != 4242 {
		t.Fatalf("jobs = %+v, want the one job on the stale path", jobs)
	}
}

// A job running ANY OTHER binary — including the freshly installed one, or
// an unrelated older copy from a previous deploy — must not be reported:
// only the file THIS swap just renamed aside is what "replaced" means here.
func TestJobsRunningReplacedBinary_IgnoresAJobOnADifferentPath(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withFakeProcessLiveness(t, map[int]string{9999: `C:\bin\aphrollo.exe`})
	saveMutantsJob(MutantsJob{Repo: "borld", Branch: "lane/x", PID: 9999, Started: time.Now()})

	if jobs := JobsRunningReplacedBinary(replacedStalePath); len(jobs) != 0 {
		t.Fatalf("jobs = %+v, want none — that job is not on the stale path", jobs)
	}
}

// self-install replaces the ONE machine-wide binary: a job it must warn
// about can belong to ANY repo on the box, not just --repo's own, so the
// scan has to cover every repo's registry.
func TestJobsRunningReplacedBinary_CoversEveryRepoNotJustOne(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withFakeProcessLiveness(t, map[int]string{
		111: replacedStalePath,
		222: replacedStalePath,
	})
	saveMutantsJob(MutantsJob{Repo: "borld", Branch: "lane/object-reflect", PID: 111, Started: time.Now()})
	saveMutantsJob(MutantsJob{Repo: "aphrollo-tools", Branch: "lane/foo", PID: 222, Started: time.Now()})

	jobs := JobsRunningReplacedBinary(replacedStalePath)
	if len(jobs) != 2 {
		t.Fatalf("jobs = %+v, want one from each repo's registry", jobs)
	}
}

// "" stalePath means nothing was renamed (no binary existed at bin yet) —
// there is nothing to have been replaced, so this must not scan at all.
func TestJobsRunningReplacedBinary_EmptyStalePathReportsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withFakeProcessLiveness(t, map[int]string{4242: replacedStalePath})
	saveMutantsJob(MutantsJob{Repo: "borld", Branch: "lane/x", PID: 4242, Started: time.Now()})

	if jobs := JobsRunningReplacedBinary(""); jobs != nil {
		t.Fatalf("jobs = %+v, want nil for an empty stale path", jobs)
	}
}

// The rendered line is the one an operator reads: the count, each job's
// lane and pid, and why it matters — its results predate this install.
func TestReplacedBinaryJobsLine_NamesEachJobsBranchAndPID(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	withFakeProcessLiveness(t, map[int]string{
		35296: replacedStalePath,
		24028: replacedStalePath,
	})
	saveMutantsJob(MutantsJob{Repo: "borld", Branch: "lane/object-reflect", PID: 35296, Started: time.Now()})
	saveMutantsJob(MutantsJob{Repo: "aphrollo-tools", Branch: "lane/foo", PID: 24028, Started: time.Now()})

	got := ReplacedBinaryJobsLine(replacedStalePath)
	if !strings.HasPrefix(got, "gate: 2 mutation run(s) are still executing the binary just replaced (") {
		t.Fatalf("line = %q, want the count and the give-up-is-a-fact prefix", got)
	}
	if !strings.HasSuffix(got, ") — their results predate this install") {
		t.Fatalf("line = %q, want the predate-this-install suffix", got)
	}
	for _, want := range []string{"lane/object-reflect pid 35296", "lane/foo pid 24028"} {
		if !strings.Contains(got, want) {
			t.Fatalf("line %q does not name %q", got, want)
		}
	}
}

// Nothing running the replaced binary means nothing to print — the common
// case, and it must stay silent rather than announce a healthy install.
func TestReplacedBinaryJobsLine_EmptyWhenNothingIsRunningTheReplacedBinary(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if got := ReplacedBinaryJobsLine(replacedStalePath); got != "" {
		t.Fatalf("line = %q, want \"\" with no matching job", got)
	}
}
