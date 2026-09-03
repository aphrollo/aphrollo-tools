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

// Three tire-drag runs died at mutant 101 of 131 on a full disk: the script
// exported TMPDIR, which a Windows binary ignores, so cargo-mutants wrote its
// tree copies to C:'s temp dir and took it from 40 GB free to 12 GB (98%).
// All three names, on every platform, and all three under the run's own build
// dir.
func TestMutantsChildEnv_PutsEveryTempNameUnderTheRunsOwnBuildDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	target := filepath.Join(t.TempDir(), ".worktrees", "borld", "mutants", "target")
	j := MutantsJob{RepoRoot: t.TempDir(), Worktree: filepath.Dir(target), TargetDir: target, TipTree: laneTip}

	env := mutantsChildEnv(j, nil)
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		val, ok := childEnvValue(env, name)
		if !ok {
			t.Fatalf("%s is not in the child's environment: a Windows binary ignores TMPDIR, so all three are set", name)
		}
		if !strings.HasPrefix(filepath.Clean(val), filepath.Clean(target)) {
			t.Fatalf("%s = %q, want it under the run's own build dir %q", name, val, target)
		}
	}
	// One name set and another inherited is the bug itself: the inherited one
	// wins in whichever tool reads it.
	if a, _ := childEnvValue(env, "TMP"); a != mustEnvValue(t, env, "TEMP") {
		t.Fatalf("TMP and TEMP disagree (%q vs %q): one of them points at the box's temp dir", a, mustEnvValue(t, env, "TEMP"))
	}
}

// A run that cannot fit its copies is a run that dies at 98% full and takes
// every verdict with it. It is refused BEFORE it starts, with the numbers.
func TestStartMutantsJob_RefusesWhenTheBuildDriveCannotFitTheRun(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	var started []MutantsJob
	fakeSpawn(t, &started)
	root := optedInLane(t)
	// After the lane is built, not before: optedInLane pins a healthy disk for
	// the tests that are not about one, and this test is about the other case.
	withFreeSpace(t, 12)

	if _, ok := StartMutantsJob(root); ok {
		t.Fatal("a drive with 12 GB free must not start a run that needs 15 GB per job")
	}
	if len(started) != 0 {
		t.Fatal("nothing may be spawned once the disk check refuses")
	}
	requireLoggedVerdict(t, cfg, "mutants-refused:disk")
	line := diskRefusalLine(12, 15)
	for _, want := range []string{"12", "15"} {
		if !strings.Contains(line, want) {
			t.Fatalf("refusal line = %q, want it to name %s", line, want)
		}
	}
	if strings.Count(strings.TrimSpace(line), "\n") != 0 {
		t.Fatalf("refusal spans more than one line:\n%s", line)
	}
}

// With room, the same lane starts normally.
func TestStartMutantsJob_StartsWhenTheDriveHasRoom(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var started []MutantsJob
	fakeSpawn(t, &started)
	withFreeSpace(t, 200)
	root := optedInLane(t)

	if _, ok := StartMutantsJob(root); !ok {
		t.Fatal("a drive with 200 GB free must not block the run")
	}
}

// A drive whose free space cannot be read is not a refusal: the gate's own
// blind spot must not stop a run that would have been fine.
func TestStartMutantsJob_StartsWhenFreeSpaceCannotBeRead(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var started []MutantsJob
	fakeSpawn(t, &started)
	root := optedInLane(t)
	prev := freeSpaceGBFn
	freeSpaceGBFn = func(string) (int, bool) { return 0, false }
	t.Cleanup(func() { freeSpaceGBFn = prev })

	if _, ok := StartMutantsJob(root); !ok {
		t.Fatal("an unreadable drive must not refuse the run")
	}
}

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

// childEnvValue reads one variable out of a child process environment slice.
func childEnvValue(env []string, name string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

func mustEnvValue(t *testing.T, env []string, name string) string {
	t.Helper()
	v, ok := childEnvValue(env, name)
	if !ok {
		t.Fatalf("%s not set", name)
	}
	return v
}
