package escape

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// gitworldRepoKey names the repository a checkout belongs to: its shared git
// directory, so every lane of it answers the same. A path that is no repository
// stands for itself.
func gitworldRepoKey(repo string) string {
	if repo != "" {
		if out, err := git(repo, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
			if common := strings.TrimSpace(out); common != "" {
				return normalizeRepoSpelling(common)
			}
		}
	}
	// A lane pruned since cannot be asked: <parent>/.worktrees/<repo>/<lane> is
	// the lane of <parent>/<repo>.
	if lanes := filepath.Dir(repo); filepath.Base(filepath.Dir(lanes)) == ".worktrees" {
		return normalizeRepoSpelling(filepath.Join(filepath.Dir(filepath.Dir(lanes)), filepath.Base(lanes)))
	}
	return normalizeRepoSpelling(repo)
}

// gitworldChangedParts is the labels of the parts a gitworld evidence names
// ("the branches changed:" and the like), sorted and without the lines gained
// and lost under them. Evidence with no such line is its own fallback.
func gitworldChangedParts(evidence, fallback string) string {
	seen := map[string]bool{}
	for line := range strings.SplitSeq(strings.ReplaceAll(evidence, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			continue
		}
		if label, _, ok := strings.Cut(line, " changed"); ok && label != "" {
			seen[label] = true
		}
	}
	if len(seen) == 0 {
		return fallback
	}
	labels := make([]string, 0, len(seen))
	for label := range seen {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return strings.Join(labels, ",")
}

// NoteGitWorldEscape records that a mutation runner's test processes changed
// the git state of the real repository at root, or the operator's global git
// config, while the runner ran: evidence that a check which should have kept
// tests inside their own repositories did not (#1043). runner names the
// runner (test map, commit-time run, measurement, proof) and evidence what
// changed. It is deduped on the runner and what changed, like every automatic
// escape, and silent on failure: the runner has already refused its result.
func NoteGitWorldEscape(root, runner, evidence string, w io.Writer) {
	stage := "gitworld:" + runner
	recordEscapeOnce(stage, EscapeOptions{
		Reason:   fmt.Sprintf("the %s changed the git state of %s or the global git config while it ran", runner, root),
		Kind:     EscapeKind,
		Evidence: evidence,
		Repo:     root,
		Check:    stage,
	}, w)
}
