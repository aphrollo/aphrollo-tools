package tdd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Disk hygiene for the build caches this binary's own gates create and use.
// Three kinds of directory qualify, and nothing else ever does:
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

// gcProtectedNames are the build-artifact directories the sweep must never
// consider, whatever category proposed them.
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
}

// AllGCScopes is the manual command's scope: everything.
func AllGCScopes() GCScope {
	return GCScope{Incremental: true, GateDirs: true, OrphanWorktrees: true, TempLitter: true}
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
	if scope.TempLitter {
		out = append(out, gcTempLitter(lockDir(), time.Now())...)
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

// gcTempLitter finds aphrollo's own leavings in the lock dir: lock files and
// owner records whose lock nobody holds, and the compiled stub dirs a test
// binary builds. A lock that is currently HELD is a running build and is
// never proposed — the acquire attempt IS the liveness test, because a lock
// file's mtime says nothing about whether a process holds it. The residual
// race (a build takes the lock between the probe and the delete) is bounded
// by running this daily against files idle for a day; the lock file is
// recreated on demand either way.
func gcTempLitter(dir string, now time.Time) []GCCandidate {
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
		if err != nil || now.Sub(info.ModTime()) < tempLitterAge {
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
		lock := strings.TrimSuffix(path, ".owner")
		if !strings.HasSuffix(lock, ".lock") {
			continue
		}
		release, free := TryAcquireFileLock(lock)
		if !free {
			continue
		}
		release()
		out = append(out, GCCandidate{Path: path, Size: info.Size(),
			Reason: "unheld lock file, idle " + formatDays(now.Sub(info.ModTime())), Kind: GCKindTempLitter})
	}
	return out
}

// gcStaleGateDirs finds the gate's own per-repo directories whose recorded
// origin no longer exists. A directory with no origin.txt (created before
// the record existed, or by something else entirely) is UNKNOWN and is left
// alone: guessing there deletes a warm target somebody is about to use.
func gcStaleGateDirs(base string) []GCCandidate {
	var out []GCCandidate
	for _, kind := range []string{"cargo-target", "failfirst-wt"} {
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
			if _, err := os.Stat(root); err == nil {
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
	for path := range registered {
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
			if registered[filepath.Clean(path)] || !onlyBuildDirInside(path) {
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
func gitWorktreePaths(repoRoot string) map[string]bool {
	out, err := git(repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	paths := map[string]bool{}
	for line := range strings.Lines(out) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree "); ok {
			paths[filepath.Clean(rest)] = true
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
		if gcProtected(c.Path) {
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
	var incremental, rest []GCCandidate
	for _, c := range cands {
		if c.Kind == GCKindIncremental {
			incremental = append(incremental, c)
			continue
		}
		rest = append(rest, c)
	}
	freed, refused = ApplyGC(rest)
	if len(incremental) == 0 {
		return freed, refused, 0
	}
	target := ResolveCargoTargetDir(repo)
	slot, release, ok := TryAcquireBuildSlot(target)
	if !ok {
		return freed, refused, len(incremental)
	}
	defer release()
	cwd := repo
	WriteBuildSlotOwner(slot, gcOwnerCommand, cwd)
	defer RemoveBuildSlotOwner(slot)
	incFreed, incRefused := ApplyGC(incremental)
	return freed + incFreed, append(refused, incRefused...), 0
}

// gcProtected reports whether any component of path names a build-artifact
// directory that must survive. Component-wise, not just the base name: a
// candidate is a directory, and one holding deps/ as its LAST component is
// the case that matters.
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
func RenderGC(cands []GCCandidate, applied bool, freed int64) string {
	if len(cands) == 0 {
		return "aphrollo tdd gc: nothing reclaimable\n"
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
		fmt.Fprintf(&b, "freed %s in %d directories\n", formatBytes(freed), len(cands))
		return b.String()
	}
	fmt.Fprintf(&b, "%s reclaimable in %d directories — run `aphrollo tdd gc --apply` to free it\n", formatBytes(total), len(cands))
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
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid age %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid age %q", s)
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
