package tdd

// #399: internal/ratchet grew a new embedded preset that broke three
// ratchet-init tests in internal/cli, and the precommit mechanical stage
// never ran them — it scopes `go test` to the touched packages alone, and
// nothing staged was under internal/cli. A change can break an IMPORTER just
// as easily as it can break itself, so the scope this file adds is the
// touched packages' own reverse dependents: everything that imports one of
// them, walked transitively, the exact question dependentsOf (clippyscope.go)
// already answers for cargo's analogous "check" stage — reused here rather
// than a second graph walk, over goWorkspaceDeps' (mutants_deps.go) existing
// directory-keyed edges instead of a new one.

// goWorkspaceDepsFn is the graph probe for the Go reverse-dependents widening,
// a package var so a test can state a graph without a real `go list` run —
// mirrors cargoWorkspaceDepsFn in clippyscope.go.
var goWorkspaceDepsFn = goWorkspaceDeps

// goRootPackageKey is "." as goPackageDir/narrowToStaged name the repo-root
// package, versus "" as goWorkspaceDeps (keyed the way TreeState keys
// packages, mutants_deps.go) names the same directory. Both conventions are
// real and neither call site is wrong to use its own; this function and its
// inverse are the one place that reconciles them for this widening.
const goRootPackageKey = "."

func goDepsKey(dir string) string {
	if dir == goRootPackageKey {
		return ""
	}
	return dir
}

func goDirFromDepsKey(key string) string {
	if key == "" {
		return goRootPackageKey
	}
	return key
}

// goReverseScopeExclude names packages the widening below must never ADD to a
// commit's scope, however many hops away they sit from a touched package. A
// commit that touches one of these directly is untouched by this exclusion —
// it still runs the ordinary way, through narrowToStaged's per-file mapping.
//
// internal/workspace is here on measured evidence, not suspicion: it is a
// direct importer of internal/tdd, so the widening would otherwise pull its
// suite into scope for the single most common edit in this repo (a change
// under internal/tdd itself) as well as every change to internal/ratchet,
// internal/docs or internal/buildinfo that reaches it transitively.
// internal/workspace's own suite shells real git/gh subprocesses
// (e2e_test.go, ghnet_test.go, submit_test.go, push_test.go) and, run
// standalone on this box on 2026-09-06, hit Go's own default per-package
// test timeout at 600.346s wall clock — not a slow pass, a hang, in a sandbox
// with no route to github.com. Pulling that suite into scope for every
// internal/tdd-touching commit would turn an intermittent risk on
// internal/workspace's OWN commits into a routine one on everyone else's;
// closing that (a bounded context on whichever subprocess call is missing
// one) is a separate, unmeasured piece of work this fix does not do.
var goReverseScopeExclude = map[string]bool{
	"internal/workspace": true,
}

// goReverseDependents widens touchedDirs (package directories as
// goPackageDir/narrowToStaged name them, "." for the repo root) to every
// package that depends on one of them, transitively, excluding
// goReverseScopeExclude. A graph that cannot be read (no `go list`, a broken
// module) widens to nothing — the safe direction in cost — rather than
// guessing.
func goReverseDependents(root string, touchedDirs []string) []string {
	deps, err := goWorkspaceDepsFn(root)
	if err != nil || len(deps) == 0 {
		return nil
	}
	seeds := make([]string, 0, len(touchedDirs))
	for _, d := range touchedDirs {
		seeds = append(seeds, goDepsKey(d))
	}
	var out []string
	for _, key := range dependentsOf(deps, seeds) {
		dir := goDirFromDepsKey(key)
		if goReverseScopeExclude[dir] {
			continue
		}
		out = append(out, dir)
	}
	return out
}
