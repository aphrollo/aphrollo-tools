package tdd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Where a mutation run's state lives, and the small shared helpers the rest
// of the package reads it with. These used to sit in the detached job's own
// file, where they were reachable only through a job record; the run is in
// the foreground now and the job record is gone, so they live here.

// mutantsStateDir holds the run's logs and the last measured baseline, beside
// the gate's other state and never in the repo.
func mutantsStateDir() string {
	dir := stateDir()
	if dir == "" {
		return ""
	}
	dir = filepath.Join(dir, "mutants")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return dir
}

// mutantsLogDir is one repo's own corner of it, keyed on the repo so two
// checkouts of two projects never read each other's baseline.
func mutantsLogDir(repoRoot string) string {
	dir := mutantsStateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, projectKey(repoRoot))
}

// primaryCheckoutRoot is the checkout holding the repo's shared git
// directory: `--git-common-dir`'s PARENT names the one `.git` every worktree
// of the repo shares, regardless of which one asked.
func primaryCheckoutRoot(repoRoot string) string {
	common := commonGitDir(repoRoot)
	if common == "" {
		return ""
	}
	dir := filepath.Clean(common)
	if strings.EqualFold(filepath.Base(dir), ".git") {
		dir = filepath.Dir(dir)
	}
	return dir
}

// pidRunningFn is the liveness probe, a seam so a test can describe a dead
// process without having to produce one.
var pidRunningFn = pidRunning

// laneBaseRef is the ref a lane's diff is taken against: the remote's default
// branch when there is one, the local one otherwise.
func laneBaseRef(root string) string {
	for _, ref := range laneBaseCandidates {
		if _, err := git(root, "rev-parse", "--verify", "--quiet", ref); err == nil {
			return ref
		}
	}
	return "HEAD~1"
}

// aphrolloTomlFlag reads one boolean from `[aphrollo]` in <root>/aphrollo.toml.
func aphrolloTomlFlag(root, key string) bool {
	return tomlBoolIn(filepath.Join(root, "aphrollo.toml"), "[aphrollo]", key)
}

// aphrolloTomlString reads one scalar STRING key from `[aphrollo]`.
func aphrolloTomlString(root, key string) (string, bool) {
	return tomlStringIn(filepath.Join(root, "aphrollo.toml"), "[aphrollo]", key)
}

// commonGitDir resolves repoRoot's shared git directory — `git rev-parse
// --path-format=absolute --git-common-dir` — the one directory EVERY worktree
// of repoRoot's repo shares, unlike `--git-dir` which names the invoking
// worktree's own. "" when git cannot say.
func commonGitDir(repoRoot string) string {
	out, err := git(repoRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// normalizeRepoSpelling reduces one spelling of a repo's git directory to a
// canonical, comparable form: forward slashes, no trailing slash, and a bare
// repo ROOT gets `.git` appended, so both ways of naming the same directory
// converge. Casing is left alone; the caller compares case-insensitively (the
// same checkout routinely appears under two Windows drive-letter casings).
func normalizeRepoSpelling(s string) string {
	s = strings.TrimRight(strings.ReplaceAll(s, `\`, "/"), "/")
	if s == "" {
		return s
	}
	if strings.EqualFold(s, ".git") || strings.HasSuffix(strings.ToLower(s), "/.git") {
		return s
	}
	return s + "/.git"
}

// short abbreviates a sha for a message a human reads.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "(none)"
	}
	return sha
}

// fileExists reports whether path is there at all.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// logf writes one line of a run's narrative, and nothing at all when the
// caller kept no log.
func logf(f io.Writer, format string, args ...any) {
	if f == nil {
		return
	}
	fmt.Fprintf(f, format+"\n", args...)
}
