package ratchet

// Options configures one `ratchet check` run.
type Options struct {
	// Root is the consuming repo.
	Root string
	// Only, when set, runs exactly one law by name.
	Only string
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
	Base string
	// BaseTree overrides Base's git read; nil derives it, a fixture supplies one.
	BaseTree BaseReader
	// CacheDir holds the per-file scan cache; empty disables caching.
	CacheDir string
}
