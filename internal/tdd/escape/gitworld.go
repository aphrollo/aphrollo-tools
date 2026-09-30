package escape

import (
	"fmt"
	"io"
)

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
