package tdd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Cargo never deletes a SUPERSEDED artifact: every worktree path and every
// profile change mints a new metadata hash and the old outputs stay forever.
// Measured on borld 2026-09-02: target/debug/deps at 207 GB / 24,260 files,
// with 234 distinct server-<hash> fingerprints and 106 GB of exe/pdb
// untouched for over three days. Cargo rebuilds whatever is missing, which is
// what makes deleting old artifacts safe; the only question is what a rebuild
// COSTS, which is why there are two tiers.
const (
	// DefaultMemberArtifactAge is the bar for workspace members: they relink
	// in seconds.
	DefaultMemberArtifactAge = 3 * 24 * time.Hour
	// DefaultDepArtifactAge is the bar for third-party artifacts, which cost
	// a real compile to recreate.
	DefaultDepArtifactAge = 14 * 24 * time.Hour
	// DefaultMutantsAge is the bar for cargo-mutants tree copies.
	DefaultMutantsAge = 24 * time.Hour
)

// cargoArtifactRe matches the one file shape cargo writes into deps/:
// "<crate>-<16 hex>" plus an extension. Anything else is something this sweep
// does not understand, and deleting what you do not understand is how a
// sweep eats a build.
var cargoArtifactRe = regexp.MustCompile(`^(?:lib)?([A-Za-z0-9_]+)-([0-9a-f]{16})(?:\.|$)`)

// cargoUnitRe matches a .fingerprint / build / incremental UNIT directory:
// the same stem without an extension.
var cargoUnitRe = regexp.MustCompile(`^([A-Za-z0-9_]+)-([0-9a-zA-Z]{8,})$`)

// gcDepsArtifacts proposes stale build artifacts under target, in two tiers:
// workspace members at memberAge, everything else at depAge. It walks each
// profile directory's deps/, .fingerprint/, build/ and incremental/.
func gcDepsArtifacts(target string, members map[string]bool, memberAge, depAge time.Duration, now time.Time) []GCCandidate {
	var out []GCCandidate
	for _, profile := range profileDirs(target) {
		out = append(out, gcProfileArtifacts(profile, members, memberAge, depAge, now)...)
	}
	return out
}

// profileDirs lists the build-profile directories inside a target dir
// (debug, release, and any custom profile), skipping cargo's own bookkeeping.
func profileDirs(target string) []string {
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "mutants" || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(target, e.Name(), "deps")); err != nil {
			continue
		}
		out = append(out, filepath.Join(target, e.Name()))
	}
	return out
}

func gcProfileArtifacts(profile string, members map[string]bool, memberAge, depAge time.Duration, now time.Time) []GCCandidate {
	var out []GCCandidate
	add := func(path, crate string, size int64, age time.Duration, dir bool) {
		kind, bar := GCKindDepsThirdParty, depAge
		if members[crate] {
			kind, bar = GCKindDepsMember, memberAge
		}
		if age < bar {
			return
		}
		what := "artifact"
		if dir {
			what = "unit dir"
		}
		out = append(out, GCCandidate{Path: path, Size: size, Kind: kind,
			Reason: crate + " " + what + ", idle " + formatDays(age)})
	}

	for _, e := range readDir(filepath.Join(profile, "deps")) {
		m := cargoArtifactRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		path := filepath.Join(profile, "deps", e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		add(path, m[1], info.Size(), now.Sub(info.ModTime()), e.IsDir())
	}

	for _, sub := range []string{".fingerprint", "build", "incremental"} {
		for _, e := range readDir(filepath.Join(profile, sub)) {
			if !e.IsDir() {
				continue
			}
			m := cargoUnitRe.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			path := filepath.Join(profile, sub, e.Name())
			newest, size := dirNewestAndSize(path)
			if newest.IsZero() {
				continue
			}
			add(path, m[1], size, now.Sub(newest), true)
		}
	}
	return out
}

func readDir(dir string) []os.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	return entries
}

// gcMutantsTrees proposes the tree copies cargo-mutants leaves under the
// measurement's temp dir (measureTempDir: beside the worktree, under
// .mutants/<name>). A LIVE cargo-mutants vetoes the whole category: those
// copies are the trees it is testing right now.
func gcMutantsTrees(base string, minAge time.Duration, now time.Time) []GCCandidate {
	if mutantsRunningFn() {
		return nil
	}
	var out []GCCandidate
	for _, e := range readDir(base) {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(base, e.Name())
		newest, size := dirNewestAndSize(path)
		if newest.IsZero() || now.Sub(newest) < minAge {
			continue
		}
		out = append(out, GCCandidate{Path: path, Size: size, Kind: GCKindMutants,
			Reason: "cargo-mutants tree copy, idle " + formatDays(now.Sub(newest))})
	}
	return out
}

// mutantsRunningFn reports whether a cargo-mutants run is alive on this box.
// A seam, so the veto can be tested without a multi-hour mutation run.
var mutantsRunningFn = cargoMutantsRunning

func cargoMutantsRunning() bool {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq cargo-mutants.exe", "/FO", "CSV").Output()
		if err != nil {
			return true // cannot tell: assume it IS running, and delete nothing
		}
		return strings.Contains(strings.ToLower(string(out)), "cargo-mutants")
	}
	if err := exec.Command("pgrep", "-x", "cargo-mutants").Run(); err == nil {
		return true
	}
	return false
}

// workspaceMemberCrates names the crates the workspace itself owns, as they
// appear in artifact file names (cargo replaces '-' with '_'). An empty
// result means "treat everything as third-party", the conservative tier.
func workspaceMemberCrates(repo string) map[string]bool {
	cmd := exec.Command("cargo", "metadata", "--no-deps", "--format-version", "1")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var meta struct {
		Packages []struct {
			Name string `json:"name"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(out, &meta); err != nil {
		return nil
	}
	members := map[string]bool{}
	for _, p := range meta.Packages {
		members[strings.ReplaceAll(p.Name, "-", "_")] = true
	}
	return members
}
