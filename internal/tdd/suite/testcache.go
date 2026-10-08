package suite

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/argvbatch"
)

// A `go test` the gate runs carries -count=1, so a pass is a measurement of
// the tree on disk and not the record of an earlier one (issue #421). That is
// only the whole truth for a test go's cache cannot vouch for. Go keys a
// cached result on the test binary (every compiled source and dependency), the
// cacheable flags, the environment variables the test read and the files it
// opened, so a package whose test depends on nothing else is served the same
// answer it would measure. A repo that knows its tests are like that opts in
// with `test-cache` under [aphrollo], per stage:
//
//	"off"    (the default) every stage keeps -count=1
//	"edit"   the post-edit suite lets go serve unchanged packages
//	"commit" the post-edit suite and the commit's mechanical stage do
//
// The merge and CI never do: the merged tree is tested once, in full, with no
// cache. Mutation keeps its own -count=1. -shuffle is not a cacheable flag, so
// a stage that serves from the cache also runs without it.
//
// A package whose tests go cannot see all the inputs of (they exec git,
// gopls or another binary, or read the network) is named in
// `test-cache-impure`, package patterns such as "./internal/git/...", and runs
// apart with -count=1 even where the cache is on.
const (
	testCacheKey       = "test-cache"
	testCacheImpureKey = "test-cache-impure"
)

// The stages a runner is built for.
const (
	cacheStageEdit   = "edit"
	cacheStageCommit = "commit"
	cacheStageMerge  = "merge"
)

// testCacheServes is the table: which (stage, test-cache setting) lets go's
// cache serve the run. Every pair not listed, the merge's among them, keeps
// -count=1.
var testCacheServes = map[[2]string]bool{
	{cacheStageEdit, "edit"}:     true,
	{cacheStageEdit, "commit"}:   true,
	{cacheStageCommit, "commit"}: true,
}

// withTestCache is r marked to be served by go's cache when repoRoot's
// aphrollo.toml asks for it at this stage, with the repo's impure packages.
// Anything but a `go test` runner is returned as it came.
func withTestCache(r Runner, repoRoot, stage string) Runner {
	if !isGoTestInvocation(r.Cmd, r.Args) {
		return r
	}
	path := filepath.Join(repoRoot, "aphrollo.toml")
	setting, _ := tomlStringIn(path, "[aphrollo]", testCacheKey)
	if !testCacheServes[[2]string{stage, strings.ToLower(strings.TrimSpace(setting))}] {
		return r
	}
	r.Cached = true
	r.Impure = tomlStringsIn(path, "[aphrollo]", testCacheImpureKey)
	return r
}

// impureMatches reports whether package pkg ("./dir") is named by pattern
// ("./dir" or "./dir/...").
func impureMatches(pattern, pkg string) bool {
	pattern, pkg = strings.TrimSuffix(pattern, "/"), strings.TrimSuffix(pkg, "/")
	if base, tree := strings.CutSuffix(pattern, "/..."); tree {
		return pkg == base || strings.HasPrefix(pkg, base+"/")
	}
	return pkg == pattern
}

// splitImpureRuns is r as the runs that give go's cache only the packages it
// can vouch for: the cached run over the rest, then the impure packages with
// their own -count=1. It answers r alone whenever it cannot split without
// guessing or has nothing to split: not a cached run, no impure list, none of
// the packages impure. A list it cannot read back as plain "./dir" packages
// (the whole module, a "..." pattern, an import path) runs whole and uncached,
// and so does a list that is all impure.
func splitImpureRuns(r Runner) []Runner {
	if !r.Cached || len(r.Impure) == 0 || !isGoTestInvocation(r.Cmd, r.Args) {
		return []Runner{r}
	}
	uncached := r
	uncached.Cached, uncached.Impure = false, nil
	pkgs, with := argvbatch.GoTestPackages(r.Cmd, r.Args)
	if pkgs == nil {
		return []Runner{uncached}
	}
	var pure, impure []string
	for _, p := range pkgs {
		if !strings.HasPrefix(p, "./") || strings.Contains(p, "...") {
			return []Runner{uncached}
		}
		if impureListed(r.Impure, p) {
			impure = append(impure, p)
		} else {
			pure = append(pure, p)
		}
	}
	switch {
	case len(impure) == 0:
		return []Runner{r}
	case len(pure) == 0:
		return []Runner{uncached}
	}
	cached := r
	cached.Args = with(pure)
	uncached.Args = with(impure)
	return []Runner{cached, uncached}
}

func impureListed(patterns []string, pkg string) bool {
	for _, p := range patterns {
		if impureMatches(p, pkg) {
			return true
		}
	}
	return false
}

// runImpureSplit is r run as splitImpureRuns cuts it, one after the other under one
// deadline, the way runBatched runs a list too long for a command line. The
// first run that does not pass ends the run and is the verdict.
func runImpureSplit(r Runner, root string, limit time.Duration, budget int, one SuiteRunner) SuiteResult {
	runs := splitImpureRuns(r)
	if len(runs) < 2 {
		return runBatched(runs[0], root, limit, budget, one)
	}
	deadline := time.Now().Add(limit)
	if !r.Deadline.IsZero() && r.Deadline.Before(deadline) {
		deadline = r.Deadline
	}
	var merged SuiteResult
	for _, sub := range runs {
		sub.Deadline = deadline
		res := runBatched(sub, root, time.Until(deadline), budget, one)
		joinResult(&merged, res)
		if !res.Passed || res.TimedOut {
			return merged
		}
	}
	return merged
}

// cachedPkgRe is the line go prints for a package it served from its cache.
var cachedPkgRe = regexp.MustCompile(`(?m)^ok\s+\S+\s+\(cached\)`)

// cachedGoPackages is how many packages of a `go test` run's output go served
// from its cache. The (cached) lines stay in the output: this only counts.
func cachedGoPackages(output string) int {
	return len(cachedPkgRe.FindAllStringIndex(output, -1))
}
