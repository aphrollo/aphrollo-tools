package cli

import (
	"os"
	"os/exec"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// The git half of the merge-only rule. Denying the Edit tool closes one door;
// `git checkout -b` in the primary checkout walks through the other one, and
// the damage is worse — every linked worktree resolves its refs through that
// checkout's common dir, so moving its HEAD moves the ground under every lane.
//
// Only four invocations are refused: the two that CREATE a branch there, the
// two that move HEAD to a non-main branch, and a `commit` that is not
// concluding a merge. Everything else — `merge`, `pull`, `fetch`, `reset`,
// `worktree`, `log`, `status` — is exactly what the primary checkout is for
// and passes straight through.

// primaryRefusalLine returns the one-line refusal for an invocation that would
// take the primary checkout off main, "" when the invocation is fine. It is
// asked BEFORE any lock is taken and before git runs: a refusal that arrives
// after the branch moved has refused nothing.
func primaryRefusalLine(realGit string, rest []string, workDir string) string {
	if len(rest) == 0 || tdd.PrimaryEditsAllowed("") {
		return ""
	}
	if !primaryRefusedVerb(realGit, rest, workDir) {
		return ""
	}
	root, ok := tdd.PrimaryMergeOnly(workDir)
	if !ok {
		return ""
	}
	return "gate: " + tdd.PrimaryMergeOnlyReason(root)
}

// primaryRefusedVerb classifies the invocation itself, before the (more
// expensive) question of whether this checkout is a merge-only primary one.
func primaryRefusedVerb(realGit string, rest []string, workDir string) bool {
	switch rest[0] {
	case "checkout":
		return leavesMainBranch(realGit, workDir, rest[1:], true)
	case "switch":
		return leavesMainBranch(realGit, workDir, rest[1:], false)
	case "commit":
		return !concludingAMerge(realGit, workDir)
	}
	return false
}

// branchCreatingFlags are the checkout/switch forms that make a new branch.
var branchCreatingFlags = map[string]bool{
	"-b": true, "-B": true, "-c": true, "-C": true,
	"--orphan": true, "--create": true, "--force-create": true,
}

// leavesMainBranch reports whether a checkout/switch would create a branch or
// move HEAD to something other than main. pathsPossible is set for `checkout`,
// whose operand may be a pathspec rather than a branch — restoring a file is
// not moving the checkout, so an operand that names no branch is left alone.
func leavesMainBranch(realGit, workDir string, args []string, pathsPossible bool) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break // everything after this is a pathspec
		}
		if branchCreatingFlags[a] {
			return true
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		if a == "main" {
			return false // already where it belongs
		}
		if pathsPossible && !namesABranch(realGit, workDir, a) {
			return false // a pathspec: `git checkout -- x` in long form
		}
		return true
	}
	return false // no operand: `git checkout` alone changes no branch
}

// namesABranch reports whether ref resolves to a local branch or a
// remote-tracking one, which is what separates `git checkout lane/x` from
// `git checkout README.md`.
func namesABranch(realGit, workDir, ref string) bool {
	for _, full := range []string{"refs/heads/" + ref, "refs/remotes/" + ref} {
		cmd := exec.Command(realGit, "show-ref", "--verify", "--quiet", full)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
		if cmd.Run() == nil {
			return true
		}
	}
	return false
}

// concludingAMerge reports whether this commit is finishing a merge rather
// than authoring a change. MERGE_HEAD is the state git leaves mid-merge; the
// reflog action is what git itself sets when a merge drives the commit, and it
// covers the squash/`--no-commit` shapes that leave no MERGE_HEAD behind.
func concludingAMerge(realGit, workDir string) bool {
	if mergeHeadExists(realGit, workDir) {
		return true
	}
	return strings.Contains(strings.ToLower(os.Getenv("GIT_REFLOG_ACTION")), "merge")
}
