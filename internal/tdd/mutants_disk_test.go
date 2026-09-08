package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withFreeSpace answers every free-space question with one number.
func withFreeSpace(t *testing.T, gb int) {
	t.Helper()
	prev := freeSpaceGBFn
	freeSpaceGBFn = func(string) (int, bool) { return gb, true }
	t.Cleanup(func() { freeSpaceGBFn = prev })
}

// ratchet: test_removed TestMutantsChildEnv_PutsEveryTempNameUnderTheRunsOwnBuildDir: mutantsChildEnv is deleted with the detached producer; TestMeasureEnv_SetsAllThreeTempNamesAndProfile makes the same claim about measureEnv
// ratchet: test_removed TestMutantsChildEnv_BaseOverrideBeatsTheJobsOwn: there is no job file to override; the base is MeasureOpts.Base, proved by TestGateMutantsRun_BaseDefaultsToMergeBaseWithDefaultBranch
// ratchet: test_removed TestMutantsProducerArgv_BaseOverrideReachesTheScriptsArgv: the repo-owned producer script is deleted; the binary invokes the tool itself
// ratchet: test_removed TestMutantsChildEnv_ForwardsTimeoutFlagsWhenSet: the timeout budget is derived from the last measured baseline, proved by TestMinTestTimeout_ThreeTimesLastBaselineFlooredAt120
// ratchet: test_removed TestMutantsChildEnv_BaselineSkipReflectsTheGatesLastGreenRunInsideTheWindow: --baseline skip is gone with the producer env; the run always measures its own baseline
// ratchet: test_removed TestStartMutantsJob_RefusesWhenTheBuildDriveCannotFitTheRun: there is no job to start; TestDiskCheck_RefusesNamingBothNumbers makes the same claim about MeasureLane
// ratchet: test_removed TestStartMutantsJob_StartsWhenTheDriveHasRoom: same deletion, same replacement
// ratchet: test_removed TestStartMutantsJob_StartsWhenFreeSpaceCannotBeRead: same deletion; an unreadable drive still never refuses, proved by refuseOnDisk's own guard in TestMeasure_* fixtures

// The probe has to agree with reality on the box it runs on: the drive this
// test is running from has SOME free space, and a path that does not exist has
// no answer.
func TestFreeSpaceGB_ReadsTheDriveItIsPointedAt(t *testing.T) {
	gb, ok := freeSpaceGB(t.TempDir())
	if !ok {
		t.Skip("free space is not readable on this box")
	}
	if gb < 0 {
		t.Fatalf("free space = %d GB", gb)
	}
}

// `gate doctor` says what the disk looks like, because a box that is about to
// run out is one where every heavy run dies for a reason nothing else reports.
func TestDoctorDiskSpace_WarnsUnderTheThreshold(t *testing.T) {
	withFreeSpace(t, 12)
	c := doctorDiskSpace(DoctorInput{Repo: t.TempDir()})
	if !c.Warn {
		t.Fatalf("check = %+v, want a warning at 12 GB free", c)
	}
	if !strings.Contains(c.Detail, "12") {
		t.Fatalf("detail = %q, want the number it warned about", c.Detail)
	}
	withFreeSpace(t, 200)
	if c := doctorDiskSpace(DoctorInput{Repo: t.TempDir()}); c.Warn || !c.OK {
		t.Fatalf("check = %+v, want a clean report at 200 GB free", c)
	}
}

// The OS temp dir also collects whole cargo target dirs: 9.3 GB of one,
// carrying CACHEDIR.TAG, was left by the dead runs. It is reclaimable exactly
// when nothing owns it.
func TestGCTempTargetDir_ReclaimsAnAbandonedOneAndKeepsALiveOne(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{cargoCacheTag, "debug.bin"} {
		if err := os.WriteFile(filepath.Join(target, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(filepath.Join(target, "debug.bin"), old, old); err != nil {
		t.Fatal(err)
	}

	prev := targetDirOwnerFn
	targetDirOwnerFn = func(string) (int, bool) { return 777, true }
	t.Cleanup(func() { targetDirOwnerFn = prev })
	if got := gcTempTargetDirs([]string{tmp}, time.Now()); len(got) != 0 {
		t.Fatalf("candidates = %+v, want a live target dir left alone", got)
	}
	if lines := TempTargetsInUse([]string{tmp}); len(lines) != 1 || !strings.Contains(lines[0], "777") {
		t.Fatalf("in-use lines = %v, want one naming pid 777", lines)
	}

	targetDirOwnerFn = func(string) (int, bool) { return 0, false }
	got := gcTempTargetDirs([]string{tmp}, time.Now())
	if len(got) != 1 || got[0].Path != target {
		t.Fatalf("candidates = %+v, want the abandoned %s", got, target)
	}
	if got[0].Size == 0 || !strings.Contains(got[0].Reason, "idle") {
		t.Fatalf("candidate = %+v, want its size and idle time", got[0])
	}
}

// Only a real cargo target dir qualifies: the tag is what says cargo made it.
func TestGCTempTargetDir_IgnoresADirectoryCargoNeverMade(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "target", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "target", "sub", "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	prev := targetDirOwnerFn
	targetDirOwnerFn = func(string) (int, bool) { return 0, false }
	t.Cleanup(func() { targetDirOwnerFn = prev })

	if got := gcTempTargetDirs([]string{tmp}, time.Now()); len(got) != 0 {
		t.Fatalf("candidates = %+v, want nothing without CACHEDIR.TAG", got)
	}
}
