package postedit

import (
	"encoding/json"
	"path/filepath"
)

// worktreeAdvisoryInput is the subset of a PreToolUse payload the worktree
// advisory needs: which file the edit targets and the session it belongs to.
type worktreeAdvisoryInput struct {
	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
}

// WorktreeAdvisory warns — at most once per session — when an edit targets a
// file inside a repo's MAIN clone rather than a linked git worktree. The
// one-PR = one-branch = one-worktree flow expects edits to land in a worktree
// prepared by `aphrollo workspace prepare`; editing the main clone directly
// invites a stale base and claim contention. It is advisory only — it never
// blocks (the edit-time gate stays near-zero-false-positive, per tdd.go) — and
// silent whenever it cannot tell: a non-edit tool, a path under no git repo, a
// path already inside a worktree, or git unavailable all Allow.
//
// It keys off the EDITED FILE'S path, not the process cwd: the agents runner
// resets a coder turn's cwd to the project home (the main clone) every turn,
// so cwd would false-positive on every legitimate worktree edit. The file path
// is where the change actually lands.
func WorktreeAdvisory(raw []byte) Decision {
	var in worktreeAdvisoryInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return Decision{Action: Allow}
	}
	if !gatedEditTools[in.ToolName] {
		return Decision{Action: Allow}
	}
	path := in.ToolInput.FilePath
	if path == "" {
		path = in.ToolInput.NotebookPath
	}
	if path == "" {
		return Decision{Action: Allow}
	}

	dir := filepath.Dir(path)
	gitDir := gitOut(dir, "rev-parse", "--git-dir")
	commonDir := gitOut(dir, "rev-parse", "--git-common-dir")
	if gitDir == "" || commonDir == "" {
		return Decision{Action: Allow} // not a git repo / git missing — don't nag
	}
	if !sameGitDir(dir, gitDir, commonDir) {
		return Decision{Action: Allow} // a linked worktree — the happy path
	}
	// A main clone. Warn the first time this session, then stay silent.
	if !markWorktreeWarned(in.SessionID) {
		return Decision{Action: Allow}
	}
	return Decision{Action: Warn, Reason: worktreeWarnReason(path)}
}

// sameGitDir reports whether --git-dir and --git-common-dir resolve to the same
// directory. They match in a main clone and diverge in a linked worktree, whose
// git-dir is <common-dir>/worktrees/<name>. git may report either path relative
// to the directory it ran in, so both are resolved against dir before compare.
func sameGitDir(dir, gitDir, commonDir string) bool {
	return resolveDir(dir, gitDir) == resolveDir(dir, commonDir)
}

func resolveDir(base, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

func worktreeWarnReason(path string) string {
	return "editing " + filepath.Base(path) + " in a main clone, not a linked git worktree. " +
		"The one-PR = one-branch = one-worktree flow expects `aphrollo workspace prepare <repo> <branch>` " +
		"first — editing the main clone risks a stale base and claim contention. (Shown once per session.)"
}
