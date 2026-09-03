package tdd

import (
	"fmt"
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

// mutantsDiskPerJobGB is what one concurrent mutant needs on the build drive:
// a tree copy plus its build products, rounded up from the 135 MB copies and
// the multi-gigabyte target dirs measured beside them. Deliberately generous —
// the cost of refusing a run that would have fit is one message.
const mutantsDiskPerJobGB = 15

// doctorDiskWarnGB is where `gate doctor` starts saying the box is tight.
const doctorDiskWarnGB = 30

// mutantsTempDir is the temp directory a run's children write to: inside the
// run's own build dir, so a tree copy that escapes --in-place lands on the
// build drive rather than the system one.
func mutantsTempDir(j MutantsJob) string {
	if j.TargetDir == "" {
		return ""
	}
	return filepath.Join(j.TargetDir, "tmp")
}

// mutantsTempEnv is the three temp-dir variables, all pointing at the same
// directory. TMPDIR is what a POSIX tool reads, TMP and TEMP what a Windows
// one does, and Go's own os.TempDir reads TMP first on Windows: naming all
// three is the only way to be sure every child agrees.
func mutantsTempEnv(j MutantsJob) []string {
	dir := mutantsTempDir(j)
	if dir == "" {
		return nil
	}
	_ = os.MkdirAll(dir, 0o755)
	return []string{"TMPDIR=" + dir, "TMP=" + dir, "TEMP=" + dir}
}

// mutantsDiskOK reports whether the drive holding the run's build dir can fit
// it, and the line to print when it cannot. A drive whose free space cannot be
// read never refuses: the gate's own blind spot must not stop a run that would
// have been fine.
func mutantsDiskOK(targetDir string, jobs int) (bool, string) {
	free, ok := freeSpaceGBFn(nearestExistingDir(targetDir))
	if !ok {
		return true, ""
	}
	if jobs < 1 {
		jobs = 1
	}
	need := jobs * mutantsDiskPerJobGB
	if free >= need {
		return true, ""
	}
	return false, diskRefusalLine(free, need)
}

// diskRefusalLine is the whole refusal: what the drive has, what the run
// needs, and what it was about to do with it.
func diskRefusalLine(freeGB, needGB int) string {
	return fmt.Sprintf("gate: mutation run refused — %d GB free on the build drive, %d GB needed (%d GB per job); "+
		"a run that fills the drive dies mid-way and takes every verdict with it", freeGB, needGB, mutantsDiskPerJobGB)
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
