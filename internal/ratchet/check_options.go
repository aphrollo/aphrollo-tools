package ratchet

// Options configures one `ratchet check` run.
type Options struct {
	// Root is the consuming repo.
	Root string
	// Only, when set, runs exactly one law by name.
	Only string
	// Laws, when non-empty, runs exactly the laws it names, in one scan. A
	// name no law carries is not an error: the caller picked the set from
	// the laws it loaded.
	Laws []string
	// Proposed overlays in-memory content for files being edited, keyed by
	// repo-relative slash path. It is how the pre-edit hook judges a write
	// that has not happened yet — and why a run carrying one never tightens a
	// baseline: the tree it measured does not exist.
	Proposed map[string]string
	// Files, when non-empty, narrows the scan to these repo-relative paths.
	// The pre-edit path uses it: one file's laws, not the tree's.
	Files []string
	// Tracked, when non-empty, is the ONLY set of paths the whole-tree walk
	// may consider — the commit gate passes `git ls-files`, because it judges
	// what is IN the commit and an untracked file is part of no commit. A
	// shared checkout is full of other people's scaffolding, and rejecting a
	// merge over a file nobody is committing is a rejection nobody can clear.
	// Everything else about the run is unchanged: scope floors and stale
	// registry entries are still whole-tree questions.
	Tracked []string
	// TrackedIgnored is the subset of Tracked that .gitignore also matches. A
	// repo can ignore a whole extension and still track those files; the disk
	// walk hands one to a law only when it declared `ignore_gitignore`, and
	// the tracked set carries the same flag so the two agree.
	TrackedIgnored []string
	// Tighten writes every baseline down to what this run measured.
	Tighten bool
	// Base is a git ref symbol-removed judges the tree against; empty skips it.
	// It is also the pre-image ref a `[scope] changed = "staged"` law reads
	// from (HEAD at commit time — the same ref, because "staged" means
	// exactly what symbol-removed already compares HEAD to).
	Base string
	// BaseTree overrides Base's git read; nil derives it, a fixture supplies one.
	BaseTree BaseReader
	// StagedFiles is the input set a `[scope] changed = "staged"` law judges:
	// the paths this commit stages, repo-relative slash paths. The commit
	// gate already computes this (baselineguard.go's stagedFiles) for its own
	// use; it is handed in here rather than re-derived, so there is exactly
	// one place that decides what "staged" means.
	StagedFiles []string
	// Renames maps a changed file's path to the path it was renamed FROM, for
	// the renames in StagedFiles or LaneFiles. A diff-scoped law reads a
	// renamed file's pre-image at its old path, so a pure move reads as no
	// change rather than a whole new file. The caller computes it from the
	// same rename-detecting diff that listed the changed set.
	Renames map[string]string
	// LaneFiles is the input set a `[scope] changed = "lane"` law judges: the
	// paths the current lane has changed since its merge-base. Also computed
	// once by the caller (precommit_fmtscope.go's laneChangedPaths).
	LaneFiles []string
	// LaneBase is the git ref a "lane"-scoped law's PRE-image reads from: the
	// lane's merge-base, resolved by the caller exactly once — mirrors Base,
	// which answers the same question for "staged" (HEAD).
	LaneBase string
	// LaneBaseTree overrides LaneBase's git read; nil derives it, a fixture
	// supplies one.
	LaneBaseTree BaseReader
	// GraphTree, when set, supplies the tree the dep-graph laws run their
	// graph query in; nil runs it over Root. The commit gate hands in a
	// checkout of the index, so an unstaged manifest edit never decides a
	// commit. Called at most once per run, and only when a dep-graph law is
	// judged.
	GraphTree func() (GraphTree, error)
	// GraphOverlay is content the Go dependency-graph laws read in place of
	// the file on disk (repo-relative slash path to text; a path the disk
	// lacks is a file the edit would add). Only the working-tree query honors
	// it: a GraphTree supplies its own checkout.
	GraphOverlay map[string]string
	// SkipGraphLaws leaves every dependency-graph law unjudged: the caller
	// knows this run's edit cannot change the graph, so a `go list` or
	// `cargo metadata` would only re-answer what the last run said.
	SkipGraphLaws bool
	// GraphCacheDir holds the dependency-graph cache alone, for a run that
	// must not touch the per-file scan cache (a narrowed scan rewrites it to
	// the files it saw). Empty falls back to CacheDir.
	GraphCacheDir string
	// CacheDir holds the per-file scan cache; empty disables caching.
	CacheDir string
	// CommitMessage is the commit message text this run is judging, when
	// there is one — a hunk-regex law's `name_group` mode reads a
	// `Removes-test: <name>: <why>` trailer from it, because the removed
	// line it excuses no longer exists anywhere for an in-file comment to
	// sit beside. Empty admits no trailer, which is "no escape given".
	CommitMessage string
}
