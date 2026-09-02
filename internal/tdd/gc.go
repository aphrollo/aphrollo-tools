package tdd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Disk hygiene for the build caches this binary's own gates create and use.
// Four kinds of leftover qualify, and nothing else ever does:
//
//	(a) idle incremental caches in the invoking workspace's target dir --
//	    deleting one costs a single recompile of that crate;
//	(b) the gate's per-repo fail-first worktree / warm target whose repo is
//	    gone (origin.txt, written at creation, is the only thing that knows);
//	(c) a directory beside a registered external worktree that holds nothing
//	    but target/ -- git dropped the worktree, the build dir survived.
//
// Everything else is somebody's work. In particular deps/, build/ and
// .fingerprint/ are NEVER reclaimable: they are what makes the next build
// incremental at all, so removing them turns a warm rebuild into a cold one.

// DefaultGCAge is how long an incremental cache must have gone untouched to
// be reclaimable. Three days: the cost of being wrong is one recompile of
// that one crate, and a crate nobody has touched in three days is not the
// crate whose rebuild anyone is waiting on.
const DefaultGCAge = 3 * 24 * time.Hour

// gcOriginFile records, beside a hash-named gate directory, which repo root
// it belongs to -- the only way to tell a live gate dir from the remains of
// a repo that was deleted months ago.
const gcOriginFile = "origin.txt"

// gcProtectedNames are the build-artifact directories no category may
// propose WHOLESALE: proposing one is proposing to cold-rebuild the world.
// The deps tiers are exempt because they name individual artifacts by
// cargo's own <crate>-<hash16> stem and an mtime bar — deps/ is reclaimable
// through the fingerprint shape, never by name.
var gcProtectedNames = map[string]bool{
	"deps":         true,
	"build":        true,
	".fingerprint": true,
}

// GCKind is which category proposed a candidate. It decides how the sweep
// may delete it, not whether: an incremental cache belongs to the target dir
// a build could be using RIGHT NOW, so it is swept only under a build slot.
// The zero value means "no special handling".
type GCKind int

const (
	GCKindOther GCKind = iota
	GCKindIncremental
	GCKindGateDir
	GCKindOrphanWorktree
	GCKindTempLitter
	GCKindMutants
	GCKindDepsMember
	GCKindDepsThirdParty
)

// tempLitterAge is category (d)'s OWN age bar, deliberately shorter than the
// build-dir default: a lock file older than a day whose lock nobody holds
// cannot belong to a running build, and these accumulate by the hundred
// (871 measured in one operator's %TEMP%).
const tempLitterAge = 24 * time.Hour

// GCCandidate is one reclaimable directory: what it is, how big, why it
// qualifies, and which category proposed it. Reason is written for a human
// reading the table, not parsed.
type GCCandidate struct {
	Path   string
	Size   int64
	Reason string
	Kind   GCKind
}

// GCScope selects which categories a scan considers, so the git-shim hook
// (worktree-tied deletions only) and the manual command (everything) share
// one implementation.
type GCScope struct {
	Incremental     bool
	GateDirs        bool
	OrphanWorktrees bool
	TempLitter      bool
	Mutants         bool
	DepsArtifacts   bool
	// LockAge overrides how old a lock file must be to count as litter.
	// Zero means tempLitterAge. An operator who knows the box is idle can
	// lower it; the unheld-lock probe is what makes that safe.
	LockAge time.Duration
}

// AllGCScopes is the manual command's scope: everything.
func AllGCScopes() GCScope {
	return GCScope{Incremental: true, GateDirs: true, OrphanWorktrees: true, TempLitter: true,
		Mutants: true, DepsArtifacts: true}
}

// ScanGC collects the reclaimable directories for the workspace containing
// repo, sorted biggest-first so the table's first line is the one worth
// reading. It only ever READS.
func ScanGC(repo string, olderThan time.Duration, scope GCScope) []GCCandidate {
	// Absolute from here on: the command defaults to --repo ".", and a
	// candidate named relatively means a different directory the moment the
	// table is read (or acted on) from anywhere else.
	if abs, err := filepath.Abs(repo); err == nil {
		repo = abs
	}
	var out []GCCandidate
	if scope.Incremental {
		out = append(out, gcIncremental(ResolveCargoTargetDir(repo), olderThan, time.Now())...)
	}
	if scope.GateDirs {
		if dir := stateDir(); dir != "" {
			out = append(out, gcStaleGateDirs(dir)...)
		}
	}
	if scope.Mutants {
		out = append(out, gcMutantsTrees(ResolveCargoTargetDir(repo), DefaultMutantsAge, time.Now())...)
	}
	if scope.DepsArtifacts {
		out = append(out, gcDepsArtifacts(ResolveCargoTargetDir(repo), workspaceMemberCrates(repo),
			DefaultMemberArtifactAge, DefaultDepArtifactAge, time.Now())...)
	}
	if scope.TempLitter {
		out = append(out, gcTempLitter(lockDir(), scope.lockAge(), time.Now())...)
	}
	if scope.OrphanWorktrees {
		if root := RepoRoot(repo); root != "" {
			out = append(out, gcOrphanWorktreeDirs(root)...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// gcIncremental finds incremental caches under <target>/<profile>/incremental
// whose NEWEST file is older than olderThan. Newest, not the directory's own
// mtime: a nested rewrite does not touch the parent, so judging by the
// directory would delete a cache in use right now.
func gcIncremental(targetDir string, olderThan time.Duration, now time.Time) []GCCandidate {
	profiles, err := os.ReadDir(targetDir)
	if err != nil {
		return nil
	}
	var out []GCCandidate
	for _, profile := range profiles {
		if !profile.IsDir() {
			continue
		}
		incDir := filepath.Join(targetDir, profile.Name(), "incremental")
		caches, err := os.ReadDir(incDir)
		if err != nil {
			continue
		}
		for _, cache := range caches {
			if !cache.IsDir() {
				continue
			}
			path := filepath.Join(incDir, cache.Name())
			if gcProtected(path) {
				continue
			}
			newest, size := dirNewestAndSize(path)
			idle := now.Sub(newest)
			if newest.IsZero() || idle < olderThan {
				continue
			}
			out = append(out, GCCandidate{
				Path:   path,
				Size:   size,
				Reason: fmt.Sprintf("incremental cache, idle %s", formatDays(idle)),
				Kind:   GCKindIncremental,
			})
		}
	}
	return out
}

// lockAge is the scope's litter bar, defaulting to a day.
func (s GCScope) lockAge() time.Duration {
	if s.LockAge > 0 {
		return s.LockAge
	}
	return tempLitterAge
}

// gcTempLitter finds aphrollo's own leavings in the lock dir: OWNER records
// whose lock nobody holds, and the compiled stub dirs a test binary builds.
// It never proposes a .lock file — the lock file IS the mutual exclusion, a
// holder keeps a handle to it, and on Windows a delete leaves the name in a
// delete-pending state whose failed open TryAcquireFileLock cannot tell from
// a free lock. Lock files are a few bytes each; what they exclude is a
// second cargo in one target dir.
func gcTempLitter(dir string, minAge time.Duration, now time.Time) []GCCandidate {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []GCCandidate
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "aphrollo-") {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < minAge {
			continue
		}
		if e.IsDir() {
			if !strings.Contains(name, "-stub-") && !strings.Contains(name, "-pkgtest-") {
				continue
			}
			_, size := dirNewestAndSize(path)
			out = append(out, GCCandidate{Path: path, Size: size,
				Reason: "test stub dir, idle " + formatDays(now.Sub(info.ModTime())), Kind: GCKindTempLitter})
			continue
		}
		lock, isOwner := strings.CutSuffix(path, ".owner")
		if !isOwner || !strings.HasSuffix(lock, ".lock") {
			continue
		}
		// A held lock means a live build whose owner record a waiting session
		// is about to print: the acquire probe is the liveness test, and it
		// touches nothing but the record.
		release, free := TryAcquireFileLock(lock)
		if !free {
			continue
		}
		release()
		out = append(out, GCCandidate{Path: path, Size: info.Size(),
			Reason: "orphan owner record, idle " + formatDays(now.Sub(info.ModTime())), Kind: GCKindTempLitter})
	}
	return out
}

// gcStaleGateDirs finds the gate's own per-repo directories whose recorded
// origin no longer exists. A directory with no origin.txt (created before
// the record existed, or by something else entirely) is UNKNOWN and is left
// alone: guessing there deletes a warm target somebody is about to use.
func gcStaleGateDirs(base string) []GCCandidate {
	var out []GCCandidate
	// Only fail-first worktrees are gate-owned now: the gates build into the
	// repo's own target dir, so there is no gate cache to prune.
	for _, kind := range []string{"failfirst-wt"} {
		entries, err := os.ReadDir(filepath.Join(base, kind))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(base, kind, e.Name())
			origin, err := os.ReadFile(filepath.Join(path, gcOriginFile))
			if err != nil {
				continue
			}
			root := strings.TrimSpace(string(origin))
			if root == "" {
				continue
			}
			// Only a PROVEN-missing repo qualifies: any other Stat error
			// (permission, a disconnected drive, a path too long) means "I
			// could not look", and deleting on that basis takes out a live
			// gate worktree.
			if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
				continue
			}
			_, size := dirNewestAndSize(path)
			out = append(out, GCCandidate{
				Path:   path,
				Size:   size,
				Reason: fmt.Sprintf("gate %s for %s, which no longer exists", kind, root),
				Kind:   GCKindGateDir,
			})
		}
	}
	return out
}

// gcOrphanWorktreeDirs finds build-only leftovers beside repoRoot's
// registered external worktrees: same parent directory, not registered, and
// containing nothing but target/ (plus dotfiles). A registered worktree is
// never a candidate, and neither is a directory still holding source.
func gcOrphanWorktreeDirs(repoRoot string) []GCCandidate {
	registered := gitWorktreePaths(repoRoot)
	parents := map[string]bool{}
	for _, path := range registered {
		if insideDir(repoRoot, path) {
			continue // the main checkout and anything nested in it
		}
		parents[filepath.Dir(path)] = true
	}
	var out []GCCandidate
	for parent := range parents {
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(parent, e.Name())
			if registered[pathKey(path)] != "" || !onlyBuildDirInside(path) {
				continue
			}
			_, size := dirNewestAndSize(path)
			out = append(out, GCCandidate{
				Path:   path,
				Size:   size,
				Reason: "orphan build dir — no worktree registered here anymore",
				Kind:   GCKindOrphanWorktree,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// gitWorktreePaths is the set of paths git itself considers registered
// worktrees of repoRoot, cleaned for comparison. Empty when git cannot
// answer -- and an empty set means NOTHING is proposed for deletion, which
// is the safe direction: with no registry there is no way to tell an orphan
// from a live worktree.
// pathKey normalises a path for comparison: case-folded on Windows, where
// one directory routinely appears as D:\... and d:\..., and a case-SENSITIVE
// compare made a registered worktree read as an orphan build dir.
func pathKey(p string) string {
	clean := filepath.Clean(p)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

func gitWorktreePaths(repoRoot string) map[string]string {
	out, err := git(repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	paths := map[string]string{}
	for line := range strings.Lines(out) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree "); ok {
			paths[pathKey(rest)] = filepath.Clean(rest)
		}
	}
	return paths
}

// onlyBuildDirInside reports whether dir contains a target/ directory and
// nothing else that isn't a dotfile -- the shape of a worktree git removed
// and a build directory that survived it.
func onlyBuildDirInside(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	target := false
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if name == "target" && e.IsDir() {
			target = true
			continue
		}
		return false
	}
	return target
}

// ApplyGC removes each candidate, returning the bytes freed and the paths it
// REFUSED. A refusal is never silent: the whole point of the protected list
// is that a caller cannot talk the sweep into deleting an artifact dir.
func ApplyGC(cands []GCCandidate) (freed int64, refused []string) {
	for _, c := range cands {
		if gcProtected(c.Path) && !namesItsOwnArtifacts(c.Kind) {
			refused = append(refused, c.Path)
			continue
		}
		if err := os.RemoveAll(c.Path); err != nil {
			refused = append(refused, fmt.Sprintf("%s (%v)", c.Path, err))
			continue
		}
		freed += c.Size
	}
	return freed, refused
}

// ApplyGCFor applies a sweep for repo, respecting the one interlock the
// categories differ on: an incremental cache belongs to a target dir a build
// may be using right now, so those are deleted only while HOLDING a build
// slot for it, and are skipped (returned as skipped, not an error) when
// every slot is busy — the next sweep gets them. Everything else belongs to
// a repo or worktree that is already gone, so it never waits.
func ApplyGCFor(repo string, cands []GCCandidate) (freed int64, refused []string, skipped int) {
	// Anything that IS a cargo target dir — an incremental cache inside one,
	// an orphan lane build dir, a gate target — is deleted only while this
	// process holds that target's build slot. Otherwise a RemoveAll walks a
	// directory another session is compiling into.
	byTarget := map[string][]GCCandidate{}
	var free []GCCandidate
	for _, c := range cands {
		if target := gcTargetInterlock(repo, c); target != "" {
			byTarget[target] = append(byTarget[target], c)
			continue
		}
		free = append(free, c)
	}
	freed, refused = ApplyGC(free)
	for target, group := range byTarget {
		slot, release, ok := TryAcquireBuildSlot(target)
		if !ok {
			skipped += len(group)
			continue
		}
		WriteBuildSlotOwner(slot, gcOwnerCommand, repo)
		gFreed, gRefused := ApplyGC(group)
		RemoveBuildSlotOwner(slot)
		release()
		freed += gFreed
		refused = append(refused, gRefused...)
	}
	return freed, refused, skipped
}

// gcTargetInterlock names the target dir a candidate belongs to, or "" when
// deleting it cannot race a build.
func gcTargetInterlock(repo string, c GCCandidate) string {
	switch c.Kind {
	case GCKindIncremental:
		return ResolveCargoTargetDir(repo)
	case GCKindOrphanWorktree:
		return filepath.Join(c.Path, "target")
	case GCKindGateDir:
		return c.Path
	case GCKindMutants, GCKindDepsMember, GCKindDepsThirdParty:
		// These live INSIDE the target dir: a build mid-way must not lose an
		// rlib it is about to link.
		return ResolveCargoTargetDir(repo)
	default:
		return ""
	}
}

// gcProtected reports whether any component of path names a build-artifact
// directory that must survive. Component-wise, not just the base name: a
// candidate is a directory, and one holding deps/ as its LAST component is
// the case that matters.
// namesItsOwnArtifacts reports whether a category selects individual files
// inside a build directory rather than the directory itself.
func namesItsOwnArtifacts(k GCKind) bool {
	switch k {
	case GCKindDepsMember, GCKindDepsThirdParty, GCKindMutants:
		return true
	default:
		return false
	}
}

func gcProtected(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(path)), "/") {
		if gcProtectedNames[part] {
			return true
		}
	}
	return false
}

// RenderGC formats a scan (or a sweep) for a terminal: one line per
// candidate with path, size and reason, then a total. A dry run ends with
// the exact command that acts on it; an applied run reports what it freed.
// gcTierNames labels the categories whose RISK differs, so a reader can see
// where the gigabytes come from instead of one lump sum.
var gcTierNames = map[GCKind]string{
	GCKindIncremental:    "incremental caches",
	GCKindDepsMember:     "workspace artifacts",
	GCKindDepsThirdParty: "third-party artifacts",
	GCKindMutants:        "mutants trees",
}

func writeTierTotals(b *strings.Builder, cands []GCCandidate) {
	totals := map[GCKind]int64{}
	for _, c := range cands {
		if _, named := gcTierNames[c.Kind]; named {
			totals[c.Kind] += c.Size
		}
	}
	for _, k := range []GCKind{GCKindIncremental, GCKindDepsMember, GCKindDepsThirdParty, GCKindMutants} {
		if totals[k] > 0 {
			fmt.Fprintf(b, "  %-22s %9s\n", gcTierNames[k], formatBytes(totals[k]))
		}
	}
}

func RenderGC(cands []GCCandidate, applied bool, freed int64) string {
	if len(cands) == 0 {
		return "aphrollo gate gc: nothing reclaimable\n"
	}
	width := 0
	for _, c := range cands {
		if len(c.Path) > width {
			width = len(c.Path)
		}
	}
	var b strings.Builder
	var total int64
	for _, c := range cands {
		fmt.Fprintf(&b, "%-*s  %9s  %s\n", width, c.Path, formatBytes(c.Size), c.Reason)
		total += c.Size
	}
	if applied {
		// Candidates are not deletions: a run whose target dir was busy
		// deletes nothing, and counting the list read as work that happened.
		// The caller reports what it skipped and refused.
		fmt.Fprintf(&b, "freed %s\n", formatBytes(freed))
		return b.String()
	}
	writeTierTotals(&b, cands)
	fmt.Fprintf(&b, "%s reclaimable in %d directories — run `aphrollo gate gc --apply` to free it\n", formatBytes(total), len(cands))
	return b.String()
}

// ParseGCAge parses --older-than. Days is the unit a build cache is
// reasoned about in and Go's duration syntax has none, so "3d" is spelled
// out here; everything else falls through to time.ParseDuration. A negative
// or unparseable age is an error, never a silent default -- it would
// otherwise sweep everything.
func ParseGCAge(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty age")
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			// Zero selects every cache there is, which is a cold rebuild of
			// the workspace dressed up as disk hygiene.
			return 0, fmt.Errorf("invalid age %q (must be greater than zero)", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid age %q (must be greater than zero)", s)
	}
	return d, nil
}

// dirNewestAndSize walks a directory once for both facts a candidate needs:
// the most recent FILE modification inside it (is it idle?) and its total
// size (is it worth reclaiming?). Files only: a directory's own mtime moves
// when an entry is added or removed, including by a cleanup that left the
// cache itself untouched, so it says nothing about whether the cache is in
// use. An unreadable entry is skipped -- a permission error somewhere deep
// must not make a whole sweep fail.
func dirNewestAndSize(dir string) (newest time.Time, size int64) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		size += info.Size()
		return nil
	})
	return newest, size
}

// formatBytes renders a size the way an operator reads one.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// formatDays renders an idle time in whole days (or hours below one), the
// granularity the age threshold is expressed in.
func formatDays(d time.Duration) string {
	if days := int(d.Hours() / 24); days >= 1 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
