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
// Refused: the two forms that CREATE a branch there, the two that move HEAD
// to a non-main branch, a `commit` that is not concluding a merge, a `merge`
// that would fast-forward and a `pull` that could write a merge commit (a
// fast-forward moves main with no premergecommit hook firing at all — the
// whole point of the primary checkout), `cherry-pick` and `rebase` (both land
// foreign commits on main with no hook either), and a `reset --hard <ref>`
// that moves main to somewhere else. `--abort`/`--continue`/`--quit`/`--skip`
// on a cherry-pick or rebase already in progress pass, as does a bare
// `reset --hard` (discards uncommitted changes, moves nothing) and a `reset`
// with no `--hard`.
//
// Updating main from its OWN upstream is not a merge at all and passes:
// `fetch`, `remote update`, and `pull --ff-only`, whose commits arrived
// through a pull request that already fired every hook that judges them.
// Everything else — `worktree`, `log`, `status` — is exactly what the primary
// checkout is for and passes straight through.

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
	case "merge":
		// isPlainMerge already excludes --abort/--continue/--quit: those
		// conclude or cancel a merge already in progress rather than start
		// one that could fast-forward.
		return isPlainMerge(rest) && !hasArg(rest[1:], "--no-ff")
	case "pull":
		// `--ff-only` is the one pull that cannot invent history: it either
		// fast-forwards main onto the upstream main a pull request already
		// merged into — every hook having fired upstream — or it refuses.
		// Without it, refusing left a PR-only repository no way to update its
		// primary checkout at all (issue #153).
		return !hasArg(rest[1:], "--no-ff") && !hasArg(rest[1:], "--ff-only")
	case "cherry-pick", "rebase":
		return !hasAnyArg(rest[1:], sequencerConcludeFlags)
	case "reset":
		return resetHardMovesMain(rest[1:])
	}
	return false
}

// sequencerConcludeFlags are the forms that resume or cancel a cherry-pick or
// rebase already in progress rather than start one that would land foreign
// commits on main with no hook to catch it.
var sequencerConcludeFlags = map[string]bool{
	"--abort": true, "--continue": true, "--quit": true, "--skip": true,
}

// hasArg reports whether flag is one of args, verbatim.
func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// hasAnyArg reports whether any of args is a key of set.
func hasAnyArg(args []string, set map[string]bool) bool {
	for _, a := range args {
		if set[a] {
			return true
		}
	}
	return false
}

// resetHardMovesMain reports whether a `reset` invocation both discards the
// working tree (`--hard`) AND names a ref to move to. A bare `reset --hard`
// (no ref) only discards uncommitted changes — it moves nothing — and a
// `reset` with no `--hard` at all is left alone regardless of its ref, per
// the rule's own scope: this is about main's tip landing somewhere with no
// hook, not about every way to inspect or stage a diff against another ref.
func resetHardMovesMain(args []string) bool {
	hard := false
	movesRef := false
	for _, a := range args {
		if a == "--hard" {
			hard = true
			continue
		}
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		movesRef = true
	}
	return hard && movesRef
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

// concludingAMerge reports whether this commit is finishing an integration
// rather than authoring a change. All three of the gate's own
// MergeInProgressRefs count: a conflicted cherry-pick and a conflicted revert
// leave the identical situation a conflicted merge does, are concluded the
// identical way, and live in THIS checkout -- so refusing them offers an
// escape (open a lane) that cannot help. The reflog action is what git itself
// sets when a merge drives the commit, and it covers the squash/`--no-commit`
// shapes that leave no ref behind.
func concludingAMerge(realGit, workDir string) bool {
	for _, ref := range tdd.MergeInProgressRefs {
		if refResolves(realGit, workDir, ref) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(os.Getenv("GIT_REFLOG_ACTION")), "merge")
}

// refResolves reports whether ref both exists and names a valid object in
// workDir -- git's own answer, which is what `-q --verify` is for.
func refResolves(realGit, workDir, ref string) bool {
	cmd := exec.Command(realGit, "rev-parse", "-q", "--verify", ref)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), tdd.GitQueuedEnv+"=1")
	return cmd.Run() == nil
}
