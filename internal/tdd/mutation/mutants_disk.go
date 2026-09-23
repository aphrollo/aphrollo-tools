package mutation

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Three mutation runs died at mutant 101 of 131 on a full disk. The cause was
// one variable name: the runner exported TMPDIR, which a Windows binary
// ignores, so cargo-mutants wrote its tree copies to C:'s own temp dir and
// took it from 40 GB free to 12 GB — 98% — and every run after that failed for
// a reason none of them reported.
//
// Two rules, and a third that makes the condition visible before it bites:
//
//	all three names — TMPDIR, TMP and TEMP, on every platform, pointing under
//	   the run's OWN build dir. Setting one and inheriting the others is the
//	   bug: whichever the tool reads is the one that decides.
//	refuse early — a run that cannot fit its copies is refused BEFORE it
//	   starts, with the numbers, rather than dying two hours in.
//	report it — `gate doctor` says what the drive holding a target dir looks
//	   like, so "the box is nearly full" is a line somebody reads rather than a
//	   run that mysteriously exits 1.

// What one shard needs is no longer a constant: mutants_budget.go measures
// the tracked tree it copies and stats the persistent target dir it builds
// in, because the flat per-job guess that used to live here passed a run that
// then needed 375 GB for a single copy.

// doctorDiskWarnGB is where `gate doctor` starts saying the box is tight.
const doctorDiskWarnGB = 30

// reportShardBuildDirs says what the run's persistent build directories
// actually hold, per shard and in total, once the shards have finished.
//
// It is the number nobody has: the disk budget guesses mutantsColdTargetBytes
// for a shard that has never built, and every proposal to build one warm tree
// and clone it to the other shards stands or falls on how big that tree
// really is — a few gigabytes makes cloning obvious, thirty makes it a way to
// fill a drive. Reported rather than acted on, in the run's own vocabulary
// beside the shard count and the build width.
func reportShardBuildDirs(root string, shards int, log io.Writer) {
	parts := make([]string, 0, shards)
	var total int64
	for i := range shards {
		_, size := dirNewestAndSize(mutantsShardTargetDir(root, i))
		total += size
		parts = append(parts, fmt.Sprintf("shard %d %s", i, formatBytes(size)))
	}
	logf(log, "mutants: build dirs after the run: %s (%s total)", strings.Join(parts, ", "), formatBytes(total))
}

// nearestExistingDir walks up until it finds a directory that exists, so a
// build dir that has not been created yet is still measured on the right
// drive.
func nearestExistingDir(dir string) string {
	for dir != "" {
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
	return dir
}

// freeSpaceGBFn is the free-space probe, a seam so the refusal can be tested
// without filling a disk.
var freeSpaceGBFn = freeSpaceGB

// SetFreeSpaceForTest overrides the free-space probe for the duration of a
// test, restoring the real one after. Exported because internal/cli's
// postcommit tests exercise the real disk-space refusal on the way to a
// DIFFERENT failure they are testing for, and a runner genuinely low on disk
// must not let that refusal fire first and mask the scenario under test.
func SetFreeSpaceForTest(gb int, ok bool) (restore func()) {
	prev := freeSpaceGBFn
	freeSpaceGBFn = func(string) (int, bool) { return gb, ok }
	return func() { freeSpaceGBFn = prev }
}

// doctorDiskSpace reports the free space on the drive holding this project's
// target dir, warning under doctorDiskWarnGB. It is a fact about the box, not
// a broken install, so it warns and never fails.
func doctorDiskSpace(in DoctorInput) DoctorCheck {
	repo := in.Repo
	if repo == "" {
		repo = "."
	}
	// Keyed by DRIVE rather than by directory: on a box where the target dir
	// and the temp dir share a volume — every Linux box, and most Windows
	// ones — reporting per directory says the same number twice.
	byDrive := map[string]int{}
	for _, dir := range []string{ResolveCargoTargetDir(repo), os.TempDir()} {
		dir = nearestExistingDir(dir)
		free, ok := freeSpaceGBFn(dir)
		if !ok {
			continue
		}
		byDrive[driveOf(dir)] = free
	}
	var lines []string
	warn := false
	for drive, free := range byDrive {
		lines = append(lines, fmt.Sprintf("%s %d GB free", drive, free))
		if free < doctorDiskWarnGB {
			warn = true
		}
	}
	sortStrings(lines)
	detail := strings.Join(lines, ", ")
	if warn {
		return DoctorCheck{Name: "disk space", Warn: true,
			Detail: detail + fmt.Sprintf(" — under %d GB, heavy runs die mid-way", doctorDiskWarnGB)}
	}
	return DoctorCheck{Name: "disk space", OK: true, Detail: detail}
}

// driveOf names the volume a path is on, for a report a human reads.
func driveOf(path string) string {
	if vol := filepath.VolumeName(path); vol != "" {
		return vol
	}
	return "/"
}

// Category (j): a whole cargo target dir in the OS temp dir. 9.3 GB of one,
// carrying cargo's CACHEDIR.TAG and idle for hours, was left behind by the
// mutation runs that died on a full disk — the very space the next run needed.
func gcTempTargetDirs(dirs []string, now time.Time) []GCCandidate {
	var out []GCCandidate
	seen := map[string]bool{}
	for _, dir := range dirs {
		path := filepath.Join(dir, "target")
		if seen[pathKey(path)] || !isTempTargetDir(path) {
			continue
		}
		seen[pathKey(path)] = true
		if _, live := targetDirOwnerFn(path); live {
			continue
		}
		newest, size := dirNewestAndSize(path)
		if newest.IsZero() {
			continue
		}
		out = append(out, GCCandidate{Path: path, Size: size, Kind: GCKindMutantsTemp,
			Reason: "cargo target dir in the OS temp dir, idle " + formatDays(now.Sub(newest))})
	}
	return out
}

// TempTargetsInUse names the temp target dirs a live build still owns.
func TempTargetsInUse(dirs []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		path := filepath.Join(dir, "target")
		if seen[pathKey(path)] || !isTempTargetDir(path) {
			continue
		}
		seen[pathKey(path)] = true
		if pid, live := targetDirOwnerFn(path); live {
			out = append(out, fmt.Sprintf("%s: in use by pid %d — left alone", path, pid))
		}
	}
	return out
}

// isTempTargetDir reports whether path is a directory cargo itself made. The
// tag is the whole test: it is the file cargo writes at the top of a target
// dir, and nothing else in a temp dir has one.
func isTempTargetDir(path string) bool {
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return false
	}
	_, err := os.Stat(filepath.Join(path, cargoCacheTag))
	return err == nil
}

// targetDirOwnerFn reports the live build that owns a target dir, a seam so
// the sweep's rules can be tested without one.
var targetDirOwnerFn = targetDirOwner

// SetTargetDirOwnerForTest replaces the target-dir owner probe for a test
// and returns the restore. A setter rather than an assignment, so a test in
// a package above mutation (the gc sweep's) still reaches the probe.
func SetTargetDirOwnerForTest(fn func(path string) (int, bool)) (restore func()) {
	prev := targetDirOwnerFn
	targetDirOwnerFn = fn
	return func() { targetDirOwnerFn = prev }
}

// targetDirOwner reports whether a live cargo, rustc, nextest or cargo-mutants
// process is building into dir. Ownership is inferred the same way the tree
// copies' is, and fails the same way: a probe that cannot answer says LIVE, so
// a sweep never deletes a directory a build is writing to.
func targetDirOwner(dir string) (int, bool) {
	pids, ok := buildToolPids()
	if !ok {
		return 0, true
	}
	if len(pids) == 0 {
		return 0, false
	}
	newest, _ := dirNewestAndSize(dir)
	if newest.IsZero() || time.Since(newest) > mutantsCopyActiveWindow {
		return 0, false
	}
	return pids[0], true
}
