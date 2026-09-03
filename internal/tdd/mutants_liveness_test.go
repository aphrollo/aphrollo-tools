package tdd

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// A pid is not an identity. The job registry survives a reboot, and a pid is
// recycled within hours on any busy box, so "pid 4812 is alive" answered yes
// for a completely different process and the gate reported a mutation run that
// had been dead since Tuesday.
func TestRunningMutantsJobs_ARecycledPidIsNotTheJob(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	live := MutantsJob{Repo: "borld", PID: os.Getpid(), Started: time.Now(),
		PIDStart: processStartToken(os.Getpid())}
	saveMutantsJob(live)

	if got := RunningMutantsJobs("borld"); len(got) != 1 {
		t.Fatalf("the running job reads as %d live jobs, want 1", len(got))
	}

	// Same pid, different process: the token recorded at spawn no longer
	// matches the one the OS reports for that pid now.
	recycled := live
	recycled.PIDStart = "not-the-process-that-was-started"
	writeJobs(t, "borld", []MutantsJob{recycled})
	if got := RunningMutantsJobs("borld"); len(got) != 0 {
		t.Fatalf("a recycled pid reads as a running job: %+v", got)
	}
}

// A record written before this check existed carries no token. It is still
// judged by pid alone rather than discarded: the alternative is reporting
// every job of an older binary as finished, which is the wrong direction for
// a gate that refuses on a missing receipt.
func TestRunningMutantsJobs_AJobWithNoRecordedStartIsJudgedByPidAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	writeJobs(t, "borld", []MutantsJob{{Repo: "borld", PID: os.Getpid(), Started: time.Now()}})

	if got := RunningMutantsJobs("borld"); len(got) != 1 {
		t.Fatalf("an older record read as %d live jobs, want it kept", len(got))
	}
}

// The token has to describe THIS process and be stable across reads: one that
// moved between reads would retire every job at the next statusline.
func TestProcessStartToken_IsStableAndSpecificToTheProcess(t *testing.T) {
	mine := processStartToken(os.Getpid())
	if mine == "" {
		t.Fatal("this box reports no start time for its own live process, so the recycled-pid check has nothing to compare")
	}
	if again := processStartToken(os.Getpid()); again != mine {
		t.Fatalf("token moved between reads: %q then %q", mine, again)
	}
	// A pid that cannot be running has no token, and an absent token never
	// claims to match a recorded one.
	if got := processStartToken(0x7FFFFFF0); got == mine {
		t.Fatalf("a dead pid produced this process's token: %q", got)
	}
}

// writeJobs puts a job registry on disk exactly as saveMutantsJob would, so a
// test can describe a record that a live process did not write.
func writeJobs(t *testing.T, repo string, jobs []MutantsJob) {
	t.Helper()
	data, err := json.Marshal(jobs)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(mutantsJobsPath(repo), data); err != nil {
		t.Fatal(err)
	}
}
