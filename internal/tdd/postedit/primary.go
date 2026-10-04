package postedit

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	igit "github.com/aphrollo/aphrollo-tools/internal/git"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
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

// primaryTrunk is the branch a primary checkout is expected to hold: the repo's
// trunk, as the client reads it (origin/HEAD, else a configured or conventional
// branch). Not a setting: the whole rule is "this checkout stays on trunk and
// receives merges", and a repo whose trunk cannot be told never matches, which
// is the fail-open direction.
// The client keeps a trunk it found for the life of the process. That is right
// for a hook, which lives for one tool call; a long-lived caller would have to
// ask a new client.
func primaryTrunk(c *igit.Client) string {
	return strings.TrimPrefix(c.Trunk(), "origin/")
}

// primaryCheckoutPolicy is the name a refusal is COUNTED under in gate.log.
const primaryCheckoutPolicy = "primary-checkout"

// PrimaryCheckoutState describes the checkout dir sits in when the rule
// applies to it at all: applies is true only when dir IS the primary checkout
// AND the repo has at least one linked worktree. Every question is asked of
// git rather than of the path's spelling — a worktree can live anywhere,
// including inside the primary checkout's own tree.
func PrimaryCheckoutState(dir string) (root, branch, trunk string, applies bool) {
	dir = existingAncestorDir(dir)
	if dir == "" {
		return "", "", "", false
	}
	c := gitx.HookClient(dir)
	if c == nil {
		return "", "", "", false // not a git repo — say nothing
	}
	if c.IsLinkedWorktree() {
		return "", "", "", false // a linked worktree: the place work belongs
	}
	if !hasLinkedWorktree(c.CommonDir()) {
		return "", "", "", false // an ordinary clone has no primary/lane split to keep
	}
	head, err := c.Head()
	if err != nil {
		return "", "", "", false
	}
	branch = head.Branch
	if head.Detached {
		branch = "HEAD" // what `rev-parse --abbrev-ref HEAD` prints of a detached HEAD
	}
	return gitx.HookRoot(dir), branch, primaryTrunk(c), true
}

// PrimaryMergeOnly reports whether dir sits in a repo's primary checkout that
// is merge-only right now, and returns that checkout's root. A primary
// checkout parked on some other branch does not match: refusing work there
// would leave nowhere at all to work, so `gate doctor` names that state
// instead.
func PrimaryMergeOnly(dir string) (root string, ok bool) {
	root, branch, trunk, applies := PrimaryCheckoutState(dir)
	if !applies || !heldBranch(branch, trunk) {
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
		shellPath(path) + " " + trunkOf(root) +
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
	noteLanded("")
	var in primaryGateInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return Decision{}
	}
	if PrimaryEditsAllowed(in.SessionID) {
		// This session's `gate allow primary` waiver already covers the
		// whole call, but a Bash/PowerShell command still has to reach git
		// as a SEPARATE subprocess a moment later, where the git queue
		// shim's own copy of the merge-only rule cannot always read this
		// session's identity back out of its own environment. Recording
		// every git invocation the command runs as spent lets the shim
		// honor the waiver its own refusal names (#894, same shape as
		// #857's discard-bash-spent split).
		if bashLikeTools[in.ToolName] {
			markPrimaryBashSpentForCommand(in.ToolInput.Command)
		}
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
		root, ok := primaryProbe(filepath.Dir(path))
		if !ok {
			return Decision{}
		}
		noteLanded(root)
		return primaryBlock(root)
	case bashLikeTools[in.ToolName]:
		return bashPrimaryDecision(in.Cwd, in.ToolInput.Command)
	}
	return Decision{}
}

// primaryProbe is how a dir is asked whether it is a merge-only primary
// checkout; a seam so a test can count the resolutions a hook makes.
var primaryProbe = PrimaryMergeOnly

// primaryLanded is the primary checkout root this hook call found a write
// landing in, "" when none: what the wall resolved when it blocked, or what
// PrimaryLanding resolved for a waived call. A hook is one process and one call,
// so it is read back by whoever records what the wall did, with no second look.
var primaryLanded struct {
	sync.Mutex
	root string
}

func noteLanded(root string) {
	primaryLanded.Lock()
	primaryLanded.root = root
	primaryLanded.Unlock()
}

func landedRoot() string {
	primaryLanded.Lock()
	defer primaryLanded.Unlock()
	return primaryLanded.root
}

// PrimaryLanding is the root of the primary checkout the call writes into, "" when
// it writes into none, for a hook that records what the wall did and never
// decides. A call the wall blocked is answered from what the wall resolved. A call
// a primary-edits waiver let through is the one the wall never looked at: it is
// resolved here, once per directory its writes name (a shell command can name many
// writes into one). A call with no waiver that the wall passed lands nowhere.
func PrimaryLanding(raw []byte) string {
	if root := landedRoot(); root != "" {
		return root
	}
	var in primaryGateInput
	if err := json.Unmarshal(raw, &in); err != nil || !PrimaryEditsAllowed(in.SessionID) {
		return ""
	}
	var paths []string
	switch {
	case gatedEditTools[in.ToolName]:
		if p := cmp.Or(in.ToolInput.FilePath, in.ToolInput.NotebookPath); p != "" {
			paths = []string{p}
		}
	case bashLikeTools[in.ToolName]:
		paths = bashWriteTargets(in.ToolInput.Command, in.Cwd)
	}
	seen := map[string]bool{}
	for _, p := range paths {
		dir := filepath.Dir(p)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if root, ok := primaryProbe(dir); ok {
			return root
		}
	}
	return ""
}

// bashPrimaryDecision judges a shell command by every path it would write,
// each against its OWN directory — exactly like an Edit/Write is judged by
// its own file path — rather than by whether the shell's cwd itself sits in
// a primary checkout. That is what lets a `cd` out of the primary through
// and catches an absolute path into one even from a worktree's cwd.
func bashPrimaryDecision(cwd, cmd string) Decision {
	for _, p := range bashWriteTargets(cmd, cwd) {
		if root, ok := primaryProbe(filepath.Dir(p)); ok {
			noteLanded(root)
			return primaryBlockForPath(root, p)
		}
	}
	return Decision{}
}

// markPrimaryBashSpentForCommand records every git invocation cmd runs --
// each top-level segment, and recursively the script of a `<shell> -c`
// segment and the body of each `$(...)` substitution, exactly the traversal
// scanCommand performs for the discard wall (discardbash.go) -- as one this
// session's already-active `gate allow primary` waiver approved. It does not
// itself judge whether any of them is the checkout, switch, commit, reset,
// merge, pull, cherry-pick or rebase the shim's own primaryRefusedVerb would
// actually refuse: over-recording a read-only verb costs nothing, since the
// shim only ever consults a spent record on a command it would otherwise
// refuse anyway.
func markPrimaryBashSpentForCommand(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	scanCommand(cmd, func(words []string) bool {
		if verb, rest, ok := gitVerb(words); ok {
			markPrimaryBashSpent(append([]string{verb}, rest...))
		}
		return false
	})
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

// trunkOf is the trunk of the repository root holds, or "<trunk>" when it
// cannot be told: the line is still a command to adapt, never a wrong name.
func trunkOf(root string) string {
	if c := gitx.HookClient(root); c != nil {
		if t := primaryTrunk(c); t != "" {
			return t
		}
	}
	return "<trunk>"
}

// heldBranch reports whether a primary checkout on branch is the one the wall
// guards. The repo's trunk is guarded; so are main and master, the names the
// wall guarded before the trunk was read, whatever the trunk resolves to: a
// trunk that cannot be told, or an origin/HEAD that is stale or odd, never
// takes the wall off a checkout on either. The wall fails closed.
func heldBranch(branch, trunk string) bool {
	return branch == "main" || branch == "master" || (trunk != "" && branch == trunk)
}
