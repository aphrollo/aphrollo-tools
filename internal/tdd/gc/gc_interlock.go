package gc

import (
	"path/filepath"
	"strings"
)

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
	case GCKindMutantsTarget:
		// A shard's persistent build dir IS a cargo target dir, so the lock
		// that protects it is its own. The repo's resolved target dir is a
		// different directory with a different lock, and holding that one
		// while deleting this one protects nothing at all.
		return c.Path
	case GCKindStrayTarget:
		// The candidate IS a target dir by construction (isCargoTargetDir
		// required both marker files) — interlocked on ITS OWN path, not
		// repo's resolved target: a misresolution (issue #285) is what put
		// a LIVE target dir in this category at all, and a config-set
		// target-dir elsewhere on the box can be building into it right now
		// regardless of what this repo resolves to.
		return c.Path
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
