package cli

import (
	"fmt"
	"strings"
)

// The shared-stash hazard (#384): refs/stash is ONE ref for the whole repo,
// not per-worktree like everything else (index, HEAD, MERGE_HEAD) — so an
// unqualified `git stash pop`/`apply` in one lane worktree can silently take
// another lane's entry, and a stash pop conflict sets no MERGE_HEAD, so
// `git merge --abort` (the obvious recovery reflex) does not undo it either.
// This guard only engages when the repo actually has more than one linked
// worktree: a single checkout owns refs/stash outright and the hazard does
// not exist for it.
//
// Two refusals, both "before the lock, before git runs" like the discard
// wall and the primary-checkout rule:
//
//   - `stash pop`/`stash apply` on an entry that belongs to a DIFFERENT
//     branch is refused. Ownership here is not a convention this file
//     invents: git itself stamps every stash commit's subject with the
//     branch it was made on — "On <branch>: <message>" with `-m`, or
//     "WIP on <branch>: <sha> <subject>" without one — so the check reads
//     git's own record rather than trusting a message format nothing
//     enforces.
//   - a bare `stash`/`stash push`/`stash save` with no explicit message is
//     refused too: the branch is always on the entry either way, but only a
//     message says what it HOLDS, and "makes ownership legible" is this
//     guard's whole point.

// stashKnownSubs are stash's own subcommand words. An operand that is not one
// of these, on the tail of `git stash`, is a flag or pathspec for the
// implicit `push` form (`git stash -u`, `git stash -- path`), not a
// subcommand of its own.
var stashKnownSubs = map[string]bool{
	"pop": true, "apply": true, "push": true, "save": true, "list": true,
	"show": true, "drop": true, "clear": true, "branch": true, "create": true,
	"store": true,
}

// stashRefusalLine is stash's half of the "before git runs" refusal chain in
// runGitShim. line is non-empty only when refuse is true.
func stashRefusalLine(realGit string, rest []string, workDir string) (line string, refuse bool) {
	if len(rest) == 0 || rest[0] != "stash" {
		return "", false
	}
	tail := rest[1:]
	sub, args := "push", tail
	if len(tail) > 0 && stashKnownSubs[tail[0]] {
		sub, args = tail[0], tail[1:]
	}
	switch sub {
	case "pop", "apply", "push", "save":
	default:
		return "", false // list/show/drop/clear/branch/create/store: not this guard's business
	}
	if !multipleWorktrees(realGit, workDir) {
		return "", false
	}
	branch := gitShimOut(realGit, workDir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" {
		return "", false // can't name a lane at all — nothing to compare against
	}
	if sub == "pop" || sub == "apply" {
		return stashPopRefusal(realGit, workDir, branch, sub, args)
	}
	return stashPushRefusal(branch, sub, args)
}

// multipleWorktrees reports whether the repo workDir belongs to has more than
// one linked worktree — the precondition for refs/stash actually being
// shared with anyone.
func multipleWorktrees(realGit, workDir string) bool {
	out := gitShimOut(realGit, workDir, "worktree", "list", "--porcelain")
	if out == "" {
		return false
	}
	n := 0
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			n++
		}
	}
	return n > 1
}

// stashPushRefusal refuses a push/save that carries no explicit message.
func stashPushRefusal(branch, sub string, args []string) (string, bool) {
	if _, ok := stashPushMessage(sub, args); ok {
		return "", false
	}
	return fmt.Sprintf(
		"gate: an unlabelled `git stash` is refused — refs/stash is shared across every worktree of this repo, and only a message says what an entry holds (issue #384). Use: git stash push -m %q",
		branch+": <what>",
	), true
}

// stashPushMessage extracts a push/save invocation's message: `-m`/`--message`
// (with an attached `=` form) for push, or the joined non-flag operands for
// the deprecated `save`, which takes its message positionally.
func stashPushMessage(sub string, args []string) (string, bool) {
	if sub == "save" {
		var words []string
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				words = append(words, a)
			}
		}
		if len(words) == 0 {
			return "", false
		}
		return strings.Join(words, " "), true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-m" || a == "--message":
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", false
		case strings.HasPrefix(a, "--message="):
			return strings.TrimPrefix(a, "--message="), true
		}
	}
	return "", false
}

// stashTargetRef is the stash entry a pop/apply names: an explicit
// `stash@{N}` operand, or `stash@{0}` (the top of the stack) by default.
func stashTargetRef(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return "stash@{0}"
}

// stashOwningBranch reads the branch a stash entry was made on out of git's
// OWN subject line, which it stamps on every stash commit regardless of
// whether the caller supplied a message: "On <branch>: <message>" for
// `stash push -m`, "WIP on <branch>: <sha> <subject>" for the autogenerated
// default. Colons are not legal in a git branch name, so cutting at the
// first ": " isolates the branch cleanly even when the caller's own message
// contains one.
func stashOwningBranch(subject string) (branch string, ok bool) {
	rest, matched := strings.CutPrefix(subject, "WIP on ")
	if !matched {
		rest, matched = strings.CutPrefix(subject, "On ")
	}
	if !matched {
		return "", false
	}
	branch, _, ok = strings.Cut(rest, ": ")
	return branch, ok
}

// stashPopRefusal refuses a pop/apply whose target entry was made on a
// DIFFERENT branch than the one running it now. A ref that does not resolve
// (an empty stash, a typo'd index) or a subject this file cannot parse is
// left to git's own error/behavior — nothing here to protect that this file
// can also verify.
func stashPopRefusal(realGit, workDir, branch, sub string, args []string) (string, bool) {
	ref := stashTargetRef(args)
	subject := gitShimOut(realGit, workDir, "log", "-1", "--format=%s", ref)
	if subject == "" {
		return "", false
	}
	owner, ok := stashOwningBranch(subject)
	if !ok || owner == branch {
		return "", false
	}
	return fmt.Sprintf(
		"gate: refusing `git stash %s %s` — refs/stash is shared across every worktree, and this entry (%q) belongs to lane %q, not %q (issue #384). Pop it from its own lane, or confirm by hand first: git stash show -p %s",
		sub, ref, subject, owner, branch, ref,
	), true
}
