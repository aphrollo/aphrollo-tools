package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The primary checkout is merge-only. In a repo that has any linked worktree,
// the checkout sitting at the git common dir's parent holds `main` and
// receives merges; work happens in linked worktrees. Editing that checkout
// directly is how a lane ends up based on a tree somebody else is mid-merge
// into, how a `git stash` from one session eats another's edit, and how a
// branch gets created on the one checkout every other worktree resolves its
// refs through.
//
// The rule is enforced three ways, all reading the same predicate below: the
// edit-time hook denies a write, the git shim refuses the verbs that would
// move that checkout off main, and `gate doctor` says when it already is.
//
// It is deliberately narrow. It fires ONLY when all three hold — the location
// is the primary checkout, the repo has at least one linked worktree, and the
// primary checkout is on main — so an ordinary single-checkout clone, a lane
// worktree, and a primary checkout parked on a branch are all untouched.

// PrimaryEditsEnv turns the rule off for one process. The second escape is
// `aphrollo gate allow primary`, per session; both are stated in the refusal.
const PrimaryEditsEnv = "APHROLLO_PRIMARY_EDITS"

// primaryBranch is the branch a primary checkout is expected to hold. Not a
// setting: the whole rule is "this checkout stays on main and receives
// merges", and a repo whose trunk is called something else simply never
// matches, which is the fail-open direction.
const primaryBranch = "main"

// primaryCheckoutPolicy is the name a refusal is COUNTED under in gate.log.
const primaryCheckoutPolicy = "primary-checkout"

// PrimaryCheckoutState describes the checkout dir sits in when the rule
// applies to it at all: applies is true only when dir IS the primary checkout
// AND the repo has at least one linked worktree. Every question is asked of
// git rather than of the path's spelling — a worktree can live anywhere,
// including inside the primary checkout's own tree.
func PrimaryCheckoutState(dir string) (root, branch string, applies bool) {
	dir = existingAncestorDir(dir)
	if dir == "" {
		return "", "", false
	}
	gitDir := gitOut(dir, "rev-parse", "--path-format=absolute", "--git-dir")
	commonDir := gitOut(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if gitDir == "" || commonDir == "" {
		return "", "", false // not a git repo, or git is missing — say nothing
	}
	if !sameGitDir(dir, gitDir, commonDir) {
		return "", "", false // a linked worktree: the place work belongs
	}
	if !hasLinkedWorktree(resolveDir(dir, commonDir)) {
		return "", "", false // an ordinary clone has no primary/lane split to keep
	}
	root = RepoRoot(dir)
	if root == "" {
		return "", "", false
	}
	return root, gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD"), true
}

// PrimaryMergeOnly reports whether dir sits in a repo's primary checkout that
// is merge-only right now, and returns that checkout's root. A primary
// checkout parked on some other branch does not match: refusing work there
// would leave nowhere at all to work, so `gate doctor` names that state
// instead.
func PrimaryMergeOnly(dir string) (root string, ok bool) {
	root, branch, applies := PrimaryCheckoutState(dir)
	if !applies || branch != primaryBranch {
		return "", false
	}
	return root, true
}

// hasLinkedWorktree reports whether the repo has at least one LIVE linked
// worktree. git keeps one administrative directory per linked worktree under
// the common dir's `worktrees/`, so the answer is a directory listing rather
// than a second git process on a path the hook is already timing — but a
// `rm -rf` of a lane dir with no `git worktree prune` leaves that admin entry
// behind pointing at nothing, and counting it kept the primary checkout
// merge-only with no lane left to escape to (issue #120).
func hasLinkedWorktree(commonDir string) bool {
	for _, e := range worktreeAdminEntries(commonDir) {
		if !staleWorktreeEntry(filepath.Join(commonDir, "worktrees", e.Name())) {
			return true
		}
	}
	return false
}

// worktreeAdminEntries lists the directories under the common dir's
// `worktrees/`, one per worktree git has ever linked (live or stale). nil
// when there is no such directory at all — an ordinary clone.
func worktreeAdminEntries(commonDir string) []os.DirEntry {
	entries, err := os.ReadDir(filepath.Join(commonDir, "worktrees"))
	if err != nil {
		return nil
	}
	var dirs []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e)
		}
	}
	return dirs
}

// hasStaleWorktreeEntry reports whether the repo has AT LEAST ONE admin
// entry left behind by a worktree removed without `git worktree prune` —
// regardless of whether another entry is still live. It is what the
// merge-only remedy uses to offer the actual fix.
func hasStaleWorktreeEntry(commonDir string) bool {
	for _, e := range worktreeAdminEntries(commonDir) {
		if staleWorktreeEntry(filepath.Join(commonDir, "worktrees", e.Name())) {
			return true
		}
	}
	return false
}

// staleWorktreeEntry reports whether one worktree admin directory's own
// `gitdir` file names a worktree whose directory no longer exists. `gitdir`
// names the worktree's `.git` FILE, one level inside the worktree root, so
// the worktree itself is that file's parent.
func staleWorktreeEntry(adminDir string) bool {
	data, err := os.ReadFile(filepath.Join(adminDir, "gitdir"))
	if err != nil {
		return false // cannot tell; treated as live rather than silently dropped
	}
	gitFile := strings.TrimSpace(string(data))
	if gitFile == "" {
		return false
	}
	_, err = os.Stat(filepath.Dir(gitFile))
	return err != nil
}

// PrimaryMergeOnlyReason is the ONE line every enforcement point prints. It
// names the escape as a runnable command, with the worktree path this repo's
// layout implies: <parent of the checkout>/.worktrees/<checkout name>/<name>.
// When a stale worktree admin entry is ALSO sitting there — a lane dir
// removed by hand, with no `git worktree prune` to clear the record it left
// — it names that fix too, since a session hitting this rule cannot tell a
// live lane from a dead entry that merely still counts as one.
func PrimaryMergeOnlyReason(root string) string {
	path := filepath.Join(filepath.Dir(root), ".worktrees", filepath.Base(root), "<name>")
	reason := "primary checkout is merge-only — git worktree add -b lane/<name> " +
		shellPath(path) + " " + primaryBranch +
		" (override with `aphrollo gate allow primary`, the only one of these that works from inside a turn)"
	if hasStaleWorktreeEntry(commonGitDir(root)) {
		reason += " (a lane dir was removed by hand — git worktree prune clears the stale entry)"
	}
	return reason
}

// primaryGateInput is the slice of a PreToolUse payload the rule reads: which
// file an edit targets, which directory a Bash call runs in, and the session
// whose override may waive it.
type primaryGateInput struct {
	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name"`
	Cwd       string `json:"cwd"`
	ToolInput struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Command      string `json:"command"`
	} `json:"tool_input"`
}

// bashLikeTools are the shell tools judged by every path their command would
// write, not by the tool's own cwd — see bashPrimaryDecision. Python and
// heredoc writes stay out of scope: the shim is the wall this hook is a
// guardrail in front of, and both are rare enough here not to earn a second
// parser.
var bashLikeTools = map[string]bool{"Bash": true, "PowerShell": true}

// PrimaryCheckoutDecision denies a write that would land in a merge-only
// primary checkout. An Edit/Write/NotebookEdit is judged by the TARGET FILE's
// directory, not the process cwd: the agents runner resets a turn's cwd to
// the project home every turn, so cwd would refuse every legitimate worktree
// edit — the file path is where the change actually lands. A Bash or
// PowerShell call has no target until it runs, so it is judged the same
// way: by the directory EACH resolved write target lands in, not by the
// shell's own cwd (issue #118 — a `cd` out of the primary let a write
// through, and an absolute path INTO the primary from a worktree's own cwd
// slipped past unnoticed).
func PrimaryCheckoutDecision(raw []byte) Decision {
	var in primaryGateInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return Decision{}
	}
	if PrimaryEditsAllowed(in.SessionID) {
		return Decision{}
	}
	switch {
	case gatedEditTools[in.ToolName]:
		path := in.ToolInput.FilePath
		if path == "" {
			path = in.ToolInput.NotebookPath
		}
		if path == "" {
			return Decision{}
		}
		root, ok := PrimaryMergeOnly(filepath.Dir(path))
		if !ok {
			return Decision{}
		}
		return primaryBlock(root)
	case bashLikeTools[in.ToolName]:
		return bashPrimaryDecision(in.Cwd, in.ToolInput.Command)
	}
	return Decision{}
}

// bashPrimaryDecision judges a shell command by every path it would write,
// each against its OWN directory — exactly like an Edit/Write is judged by
// its own file path — rather than by whether the shell's cwd itself sits in
// a primary checkout. That is what lets a `cd` out of the primary through
// and catches an absolute path into one even from a worktree's cwd.
func bashPrimaryDecision(cwd, cmd string) Decision {
	for _, p := range bashWriteTargets(cmd, cwd) {
		if root, ok := PrimaryMergeOnly(filepath.Dir(p)); ok {
			return primaryBlockForPath(root, p)
		}
	}
	return Decision{}
}

func primaryBlock(root string) Decision {
	return Decision{Action: Block, Reason: PrimaryMergeOnlyReason(root), Policy: primaryCheckoutPolicy}
}

// primaryBlockForPath is the same denial, naming the path that earned it. A
// shell command carries many paths and the rule fires on one of them; a
// denial that named only a remedy in some repo sent the reader to fix the
// wrong thing, since the remedy names the repo of the offending PATH while
// the command that was typed may have been about something else entirely.
func primaryBlockForPath(root, path string) Decision {
	d := primaryBlock(root)
	d.Reason += " (refused the write to " + shellPath(path) + ")"
	return d
}

// PrimaryEditsAllowed reports whether this process or this session waived the
// rule. The env var covers a script or a deliberate one-off; the session
// override is the same shape as `/tdd off`, so a human who means it says so
// once and the whole session carries it. The git shim passes an empty session
// — a shell invocation belongs to no Claude session — and so reads only the
// env half.
func PrimaryEditsAllowed(session string) bool {
	if os.Getenv(PrimaryEditsEnv) == "1" {
		return true
	}
	return waivedForSession(session, WallPrimary)
}

// setPrimaryEdits persists the per-session waiver on WallPrimary — the
// `/tdd primary-edits on|off` route into the same wall the allow/revoke
// family (Allow/Revoke) already writes.
func setPrimaryEdits(session string, on bool) error {
	return setWaiver(session, WallPrimary, on)
}
