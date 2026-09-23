package mutation

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func withFreeSpace(t *testing.T, gb int) {
	t.Helper()
	t.Cleanup(SetFreeSpaceForTest(gb, true))
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

// Nobody has ever measured what a shard's persistent build directory holds.
// The disk budget falls back to a documented 15 GiB estimate for a shard that
// has never built, and the question of whether those directories could be
// cloned from one warm build instead of built N times is unanswerable without
// the real number. So the run reports it, per shard and in total, in its own
// vocabulary beside the shard count and the build width.
func TestMeasure_ReportsWhatEachShardsBuildDirHoldsAfterTheRun(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(2, "pinned"))
	// 3 KiB in shard 0's build dir, 1 KiB in shard 1's: 4 KiB between them.
	bytesFor := map[int]int{0: 3072, 1: 1024}
	stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		shard := shardIndexOf(c.Argv)
		mustWrite(t, filepath.Join(envValueOf(c.Env, "CARGO_TARGET_DIR"), "debug", "deps", "a.rlib"),
			strings.Repeat("x", bytesFor[shard]))
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 1, Col: 30 + shard, Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})
	var log bytes.Buffer

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: &log}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"shard 0 3.0 KB", "shard 1 1.0 KB", "4.0 KB"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("run log =\n%s\nwant %q in it: the size of a build dir is the number the disk budget "+
				"guesses at today", log.String(), want)
		}
	}
}

// Tests above mutation stub the target-dir owner probe only through this
// setter, so it must install the stub and restore the real probe.
func TestSetTargetDirOwnerForTest_StubIsSeenAndRestored(t *testing.T) {
	restore := SetTargetDirOwnerForTest(func(string) (int, bool) { return 4242, true })
	if pid, live := targetDirOwnerFn("/nowhere"); pid != 4242 || !live {
		restore()
		t.Fatalf("stub not installed: probe said (%d, %v)", pid, live)
	}
	restore()
	if reflect.ValueOf(targetDirOwnerFn).Pointer() != reflect.ValueOf(targetDirOwner).Pointer() {
		t.Error("restore left the stub in place")
	}
}
