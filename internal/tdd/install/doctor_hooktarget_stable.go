package install

import (
	"fmt"
	"path/filepath"
	"strings"
)

// releaseDirComponent mirrors internal/cli's own marker (gateinit_stablebin.go):
// the literal path component this box's deploy convention
// (deploy/deploy-prod.sh) — and every CI-deployed aphrollo service following
// the same shape — uses for a build directory the very NEXT deploy prunes.
// Kept as its own copy rather than an import: internal/cli already imports
// this package, so the reverse import would cycle.
const releaseDirComponent = "releases"

// underVersionedRelease reports whether path names a file inside a
// versioned-release directory, by literal path component.
func underVersionedRelease(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(path)), "/") {
		if part == releaseDirComponent {
			return true
		}
	}
	return false
}

// doctorHookTargetStable checks that every managed hook — the global git-hook
// shims AND the settings.json session hooks — exec a binary OUTSIDE a
// versioned-release directory: one the very next CI deploy prunes. A path
// there can be perfectly runnable the moment this check reads it and still
// be gone by the next deploy — doctorGitHookBinary's and doctorHookBinary's
// own runnability/identity probes cannot see that, because nothing about the
// file is wrong YET. This is the 2026-09-25 incident: `aphrollo install` ran
// with no --bin, os.Executable() resolved the FULL symlink chain
// (/usr/local/bin/aphrollo -> .../current/aphrollo -> .../releases/<ts>-<sha>/aphrollo),
// and the next deploy pruned that release out from under every shim; every
// gated git/cargo call then silently ran UNGATED.
func doctorHookTargetStable(in DoctorInput) DoctorCheck {
	c := DoctorCheck{Name: "hook target stable", OK: true}
	var bad []string
	if in.GitHooksPath != "" {
		for _, target := range hookTargets(in.GitHooksPath) {
			p := fromShellPath(target.path)
			if underVersionedRelease(p) {
				bad = append(bad, fmt.Sprintf("%s (run by %s)", p, strings.Join(target.hooks, ", ")))
			}
		}
	}
	for _, p := range managedHookBinaries(in.ConfigDir) {
		p = fromShellPath(p)
		if underVersionedRelease(p) {
			bad = append(bad, p+" (session hooks)")
		}
	}
	if len(bad) == 0 {
		return c
	}
	c.OK = false
	c.Detail = fmt.Sprintf(
		"%s — a versioned-release path the very next deploy prunes, after which the gate silently degrades to UNGATED; "+
			"run `aphrollo install` from a shell where `aphrollo` resolves through its stable PATH entry (or pass --bin <stable path> explicitly) to repoint it",
		strings.Join(bad, "; "))
	return c
}
