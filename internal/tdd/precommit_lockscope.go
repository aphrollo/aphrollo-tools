package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Cargo.lock is by far the most common workspace-manifest change — every
// dependency bump touches only it — and the one least often warranting a
// whole-workspace check: a bump moves the locked version of a handful of
// packages, and the crates that can be affected are the ones depending on
// them, directly or transitively. This file answers that question by
// diffing the lockfile itself and reusing dependentsOf (clippyscope.go),
// the same reverse-dependency walk the clippy stage already sizes itself
// with, rather than writing a second one (issue #423). A Cargo.toml or
// .cargo/config.toml change is a different matter — either really can
// reshape how everything in the workspace builds — and keeps the wide
// scope untouched.

// lockfileScope narrows workspaceManifestCheckStage's `-p` selection to the
// packages a Cargo.lock-only change can actually affect. nil means "run the
// whole workspace instead": wsManifestHit names a Cargo.toml or
// .cargo/config.toml change (lockfileOnlyHit is false) or the lockfile diff
// itself could not be read or parsed — the safe direction on a diff failure
// is the wider scope, never a narrower one that silently skips a broken
// package, and every fallback is printed so the widening is never silent.
// An empty, non-nil slice means the diff succeeded and found nothing any
// workspace crate depends on: a real narrowing to zero, not a failure.
func lockfileScope(gateName, repoRoot, ws string, wsManifestHit []string) []string {
	if !lockfileOnlyHit(wsManifestHit) {
		return nil
	}
	rel := filepath.ToSlash(wsManifestHit[0])
	// The staged set's own base (mergescope.go): HEAD, or the incoming trunk
	// tip during a trunk sync into a lane.
	before, ok := gitBlob(repoRoot, stagedBaseRev(repoRoot)+":"+rel)
	if !ok {
		fmt.Fprintf(os.Stderr, "gate %s: %s has no HEAD revision to diff against → workspace-wide check\n", gateName, rel)
		return nil
	}
	after, ok := gitBlob(repoRoot, ":"+rel)
	if !ok {
		fmt.Fprintf(os.Stderr, "gate %s: %s has no staged content to diff → workspace-wide check\n", gateName, rel)
		return nil
	}
	beforePkgs, err := parseCargoLock(before)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gate %s: HEAD's %s did not parse (%v) → workspace-wide check\n", gateName, rel, err)
		return nil
	}
	afterPkgs, err := parseCargoLock(after)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gate %s: staged %s did not parse (%v) → workspace-wide check\n", gateName, rel, err)
		return nil
	}
	moved := movedLockPackages(beforePkgs, afterPkgs)
	if len(moved) == 0 {
		// The file changed (whitespace, comment, reordering, checksum
		// refresh with no version move) but no package's locked version
		// actually moved -- nothing downstream can have broken.
		return []string{}
	}
	// The graph is built from BOTH revisions, not just the staged one: a
	// package REMOVED between them has its dependents recorded only in the
	// before-graph (the after-graph has no trace of who used to need it),
	// and one ADDED has them only in the after-graph. Unioning is the safe
	// direction — at worst a dependent that no longer applies stays in
	// scope one extra commit.
	affected := dependentsOf(lockDependencyGraph(beforePkgs, afterPkgs), moved)
	affected = append(affected, moved...)
	members, err := cargoWorkspaceDepsFn(ws)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gate %s: workspace package graph unreadable (%v) → workspace-wide check\n", gateName, err)
		return nil
	}
	var scope []string
	for _, p := range affected {
		if _, isMember := members[p]; isMember {
			scope = append(scope, p)
		}
	}
	// dedupeSorted returns nil for an empty input, but nil here means
	// "workspace-wide" to the caller -- a real narrowing to zero workspace
	// dependents must come back as the empty, non-nil slice that reads as
	// "skip the check", never as the wide-scope fallback signal.
	scope = dedupeSorted(scope)
	if scope == nil {
		scope = []string{}
	}
	return scope
}

// lockfileOnlyHit reports whether every workspace-manifest file this commit
// staged IS ws's own Cargo.lock — isWorkspaceOwnManifestFile already proved
// each entry is one of the three canonical files, so a basename check is
// enough to tell Cargo.lock apart from the other two here.
func lockfileOnlyHit(wsManifestHit []string) bool {
	if len(wsManifestHit) == 0 {
		return false
	}
	for _, f := range wsManifestHit {
		if filepath.Base(f) != "Cargo.lock" {
			return false
		}
	}
	return true
}

// lockPackage is one `[[package]]` entry from a Cargo.lock: its name and the
// dependency names it declares. Cargo.lock disambiguates a dependency that
// resolves to more than one locked version by appending " <version>" to the
// array entry; that suffix is stripped at parse time since dependentsOf
// walks the graph by name alone, the same identity `-p` already selects by.
type lockPackage struct {
	name    string
	version string
	deps    []string
}

// parseCargoLock reads a Cargo.lock document into its [[package]] entries. A
// line scanner, the same shape tomlStringsIn (runner.go) already uses for a
// TOML string array: `[[package]]` starts a new entry (closing whatever
// came before it), `name`/`version` are plain scalars, and `dependencies =
// [...]` is the one multi-line array this reader needs, tracked with the
// identical stripQuoted-guarded close tomlStringsIn's own array key uses.
func parseCargoLock(data string) ([]lockPackage, error) {
	if !strings.Contains(data, "[[package]]") {
		return nil, fmt.Errorf("no [[package]] entries found")
	}
	var pkgs []lockPackage
	var cur *lockPackage
	inArray := false
	flush := func() {
		if cur != nil {
			pkgs = append(pkgs, *cur)
			cur = nil
		}
	}
	for line := range strings.Lines(data) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[[package]]" {
			flush()
			cur = &lockPackage{}
			inArray = false
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			// A different table ([[patch.unused]] and similar) ends this
			// package's own fields.
			flush()
			inArray = false
			continue
		}
		if inArray {
			cur.deps = append(cur.deps, lockDepNames(trimmed)...)
			if strings.Contains(stripQuoted(trimmed), "]") {
				inArray = false
			}
			continue
		}
		k, val, found := strings.Cut(trimmed, "=")
		if !found {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(k) {
		case "name":
			cur.name = strings.Trim(val, `"`)
		case "version":
			cur.version = strings.Trim(val, `"`)
		case "dependencies":
			cur.deps = append(cur.deps, lockDepNames(val)...)
			if strings.HasPrefix(val, "[") && !strings.Contains(stripQuoted(val), "]") {
				inArray = true
			}
		}
	}
	flush()
	return pkgs, nil
}

// lockDepNames reads the quoted entries on one dependencies-array line (or
// fragment of one) and strips each one down to the bare package name — a
// disambiguated entry reads "name version", never just "name".
func lockDepNames(s string) []string {
	var out []string
	for _, w := range quotedWords(s) {
		name, _, _ := strings.Cut(w, " ")
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// movedLockPackages names every package whose set of locked versions
// differs between before and after: added, removed, or bumped. A version
// SET rather than a single value, so a package resolved at more than one
// version in the same lockfile is still compared correctly.
func movedLockPackages(before, after []lockPackage) []string {
	beforeV := lockVersionsByName(before)
	afterV := lockVersionsByName(after)
	names := map[string]bool{}
	for n := range beforeV {
		names[n] = true
	}
	for n := range afterV {
		names[n] = true
	}
	var moved []string
	for n := range names {
		if !sameVersionSet(beforeV[n], afterV[n]) {
			moved = append(moved, n)
		}
	}
	return dedupeSorted(moved)
}

func lockVersionsByName(pkgs []lockPackage) map[string]map[string]bool {
	m := map[string]map[string]bool{}
	for _, p := range pkgs {
		if m[p.name] == nil {
			m[p.name] = map[string]bool{}
		}
		m[p.name][p.version] = true
	}
	return m
}

func sameVersionSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for v := range a {
		if !b[v] {
			return false
		}
	}
	return true
}

// lockDependencyGraph turns one or more parsed Cargo.lock snapshots into
// forward edges (package -> the packages it depends on), exactly the shape
// dependentsOf already walks backward from a seed set. Deduped and sorted so
// the graph never depends on which snapshot's copy of an edge won.
func lockDependencyGraph(pkgSets ...[]lockPackage) map[string][]string {
	g := map[string][]string{}
	for _, pkgs := range pkgSets {
		for _, p := range pkgs {
			g[p.name] = append(g[p.name], p.deps...)
		}
	}
	for k, v := range g {
		g[k] = dedupeSorted(v)
	}
	return g
}
