package tdd

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Category (i): cargo-mutants' own tree COPIES in the OS temp dir. Run bare,
// cargo-mutants copies the whole repository into %TEMP%/cargo-mutants-<name>-
// <rand>.tmp per job and builds it cold; nothing ever deletes them. Eleven
// copies of one repo were measured on one box at roughly 135 MB each — about
// 1.5 GB of trees belonging to runs that ended days earlier.
//
// The gated job (mutants_job.go) makes no copies at all, so this category is
// about the litter left by bare runs — which the cargo shim now refuses.

// mutantsCopyPrefixes are the directory-name shapes these tools write. The
// trailing dash matters: a bare `cargo-mutants` directory is somebody's
// checkout of the tool, not a copy of a tree. gremlins is here for the same
// reason as cargo-mutants — it copies the module into a working dir per mutant
// and leaves them behind on Windows, where its own cleanup cannot unlink a
// file another process still holds.
var mutantsCopyPrefixes = []string{"cargo-mutants-", "gremlins-"}

// isMutantsCopyName reports whether a directory name is one of those copies.
func isMutantsCopyName(name string) bool {
	for _, p := range mutantsCopyPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// gcMutantsTempCopies proposes the tree copies in dirs whose run is over.
// Ownership decides, not age: a copy a run is using is the tree it is
// mutating RIGHT NOW, and one whose run is gone is garbage the moment the
// process exits.
func gcMutantsTempCopies(dirs []string, now time.Time) []GCCandidate {
	var out []GCCandidate
	seen := map[string]bool{}
	for _, dir := range dirs {
		for _, e := range readDir(dir) {
			path := filepath.Join(dir, e.Name())
			if !e.IsDir() || !isMutantsCopyName(e.Name()) || seen[pathKey(path)] {
				continue
			}
			seen[pathKey(path)] = true
			if _, live := mutantsCopyOwnerFn(path); live {
				continue
			}
			newest, size := dirNewestAndSize(path)
			if newest.IsZero() {
				continue
			}
			out = append(out, GCCandidate{Path: path, Size: size, Kind: GCKindMutantsTemp,
				Reason: "cargo-mutants tree copy whose run is gone, idle " + formatDays(now.Sub(newest))})
		}
	}
	return out
}

// MutantsCopiesInUse names the copies a live run still owns, one line each,
// so the report accounts for every copy on the box rather than silently
// omitting the ones it will not touch.
func MutantsCopiesInUse(dirs []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		for _, e := range readDir(dir) {
			path := filepath.Join(dir, e.Name())
			if !e.IsDir() || !isMutantsCopyName(e.Name()) || seen[pathKey(path)] {
				continue
			}
			seen[pathKey(path)] = true
			if pid, live := mutantsCopyOwnerFn(path); live {
				out = append(out, fmt.Sprintf("%s: in use by cargo-mutants pid %d — left alone", path, pid))
			}
		}
	}
	return out
}

// MutantsTempDirs are the directories a bare run leaves its copies in.
func MutantsTempDirs() []string { return lockLitterDirs() }

// mutantsCopyOwnerFn is the ownership probe, a seam so the sweep's rules can
// be tested without a live mutation run.
var mutantsCopyOwnerFn = mutantsCopyOwner

// mutantsCopyOwner reports the live cargo-mutants process a copy belongs to.
//
// There is no pid inside a copy and no portable way to ask which process holds
// a directory open, so ownership is inferred from the two things that CAN be
// known: whether any cargo-mutants is running at all, and whether this copy
// has been written to recently enough to be the one it is running in. Both
// unknowns fail SAFE — a probe that cannot answer reports the copy live, and
// deletes nothing — because deleting a tree a run is mutating destroys hours
// of work while keeping one costs a line in a report.
func mutantsCopyOwner(dir string) (int, bool) {
	pids, ok := cargoMutantsPids()
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

// cargoMutantsPids lists the live cargo-mutants processes. false means the
// question could not be asked, which every caller treats as "assume live".
func cargoMutantsPids() ([]int, bool) {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq cargo-mutants.exe", "/NH", "/FO", "CSV").Output()
		if err != nil {
			return nil, false
		}
		return csvPids(string(out)), true
	}
	out, err := exec.Command("pgrep", "-x", "cargo-mutants").Output()
	if err != nil {
		// pgrep exits 1 with no output when nothing matched, which IS an
		// answer; any other failure (no pgrep on the box) is not.
		if ee, isExit := err.(*exec.ExitError); isExit && ee.ExitCode() == 1 {
			return nil, true
		}
		return nil, false
	}
	var pids []int
	for line := range strings.SplitSeq(string(out), "\n") {
		if n, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			pids = append(pids, n)
		}
	}
	return pids, true
}
