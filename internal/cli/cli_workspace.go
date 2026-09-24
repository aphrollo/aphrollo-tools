package cli

import (
	"flag"
	"fmt"
	"io"
	"slices"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/workspace"
)

const workspaceUsage = `usage: aphrollo workspace <subcommand> [args]

Mutating verbs EXECUTE BY DEFAULT; pass --dry to print the plan and stop. Every
verb is idempotent — re-running on a half-built state finishes the job without
clobbering it, so a re-driven turn never double-pushes or double-opens a PR.

Core coder flow (create · commit · push · submit), all from the worktree's cwd:
  create <repo> <branch>    Mark git-safe, create the worktree, install deps.
                            Run from the main clone (--dry to preview).
  commit -m <msg>           Stage (-A) + commit the CURRENT worktree, honoring the
                            TDD gate; reports sha + delta (--dry; --no-verify;
                            --staged-only).
  push                      git push -u origin HEAD; reuse the branch's PR state
                            in the receipt if one is already OPEN (--dry;
                            --force-with-lease). Never opens a PR — submit does.
  submit -m <summary>       Push (idempotent), then hand off: open a READY PR
                            when none exists, flip a legacy/in-flight draft to
                            in-review, or no-op [skip] an already-ready one —
                            and set the PR body to the summary (the in_progress
                            → review handoff). Unconditional on CI state;
                            re-callable (--dry). Per-worktree, one repo at a time:
                            acts on the cwd worktree's PR — there is no
                            ticket-level submit. submit is the sole PR opener, so
                            CI fires exactly once, at the handoff.

Worktree lifecycle:
  claim <repo> <branch>     Put a prepared worktree on the dev tier so it is
                            viewable. Wraps the privileged aphrollo-dev claim
                            fence. (api: runs goose up on the dev DB;
                            --no-migrate to skip.) (--dry).
  unclaim [repo] [branch]   Repoint the dev tier back at the main clone + restart.
                            Inverse of claim (--dry).
  list <repo>               List the repo's git worktrees: path, branch (or
                            "detached"), age since the last commit, dirty-file
                            count, and PR state ("none" when there isn't one,
                            "?" when the lookup failed or timed out) — the PR
                            lookups run concurrently, capped per worktree, so
                            one slow one never holds up the rest (read-only).
  remove <repo> <branch>    Remove a prepared worktree AND delete its local
                            branch (--dry; --keep-branch to leave the branch;
                            --force to drop a dirty worktree). Idempotent: an
                            already-gone worktree or branch is a [skip], so
                            re-running is a no-op.
  prune [repo]              Sweep the repo's worktrees and remove the merged ones:
                            a worktree goes only if its PR is MERGED, the tree is
                            CLEAN, and it is not the cwd. Others are skipped with a
                            reason (open PR / no PR / dirty / current). Folds in the
                            stale admin-record prune (--dry lists; --force removes a
                            dirty merged tree too; --stale <dur> instead sweeps
                            detached, PR-less, idle worktrees past that age — a
                            positive duration only, e.g. 3d or 36h).
  prune <repo> <branch>    Per-ticket form: same as remove <repo> <branch>
                            --keep-branch — removes exactly that one ticket's
                            worktree, idempotently, and leaves the local branch
                            in place (--dry; --force forwards to remove too).

Operator / outside-use verbs (pass [repo] [branch] to target a worktree):
  update                    Rebase the cwd worktree onto origin/<default> and, on a
                            clean rebase, force-push (with lease) to refresh the PR.
                            Conflict: left in progress, non-zero, with resolve hints.
                            cwd-only (--dry reports the behind-count). Alias: rebase.
  sync [repo]               Fetch + fast-forward the base clone's LOCAL default
                            branch to origin/<default> — the non-destructive
                            "catch the clone up after a merge" primitive. No <repo>
                            resolves the cwd's repo (the same rule commit uses).
                            Strict FF only: a dirty or diverged clone is left
                            untouched, exit 0 with the reason. Idempotent (--dry
                            previews).
  diff                      Print the branch's PR diff vs origin/<default>
                            (read-only; --stat for the diffstat).
  verify                    Legacy name — prints "workspace verify is now aphrollo
                            check" and runs it.
  status                    One terse line: PR state (merged/open/draft),
                            mergeability gate, and a pass/total check tally
                            (read-only).
  merge                     Merge the branch's PR via gh, honoring CI/mergeable
                            (--dry; --squash|--merge|--rebase, --keep-branch).
                            --wait first waits for every check on the PR's
                            current head (--timeout, default 90m); --wait <pr>...
                            merges those PRs in order from their lanes, one at
                            a time, stopping at the first refusal.

The worktree lands at <repo-parent>/.worktrees/<repo-name>/<branch-slug> — the
same layout aphrollo-dev uses, so a created worktree can later be claimed. The
coder verbs (commit/push/submit) and update act on the worktree you stand in;
verify/status/diff/merge also accept an explicit <repo> <branch> to target one
from outside.

A typical loop: create <repo> <branch> → cd in → edit/test → commit -m "…" →
push → submit -m "…" → (review) → merge → prune.
`

func runWorkspace(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, workspaceUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, workspaceUsage)
		return 0
	case "create":
		return runWorkspaceCreate(args[1:], stdout, stderr)
	case "claim":
		return runWorkspaceClaim(args[1:], stdout, stderr)
	case "unclaim":
		return runWorkspaceUnclaim(args[1:], stdout, stderr)
	case "list":
		return runWorkspaceList(args[1:], stdout, stderr)
	case "remove":
		return runWorkspaceRemove(args[1:], stdout, stderr)
	case "prune":
		return runWorkspacePrune(args[1:], stdout, stderr)
	case "commit":
		return runWorkspaceCommit(args[1:], stdout, stderr)
	case "push":
		return runWorkspacePush(args[1:], stdout, stderr)
	case "pr":
		return runWorkspacePR(args[1:], stdout, stderr)
	case "ship":
		return runWorkspaceShip(args[1:], stdout, stderr)
	case "submit":
		return runWorkspaceSubmit(args[1:], stdout, stderr)
	case "status":
		return runWorkspaceStatus(args[1:], stdout, stderr)
	case "diff":
		return runWorkspaceDiff(args[1:], stdout, stderr)
	case "update", "rebase":
		return runWorkspaceUpdate(args[1:], stdout, stderr)
	case "sync":
		return runWorkspaceSync(args[1:], stdout, stderr)
	case "verify":
		return runWorkspaceVerify(args[1:], stdout, stderr)
	case "merge":
		return runWorkspaceMerge(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aphrollo workspace: unknown subcommand %q\n\n%s", args[0], workspaceUsage)
		return 2
	}
}

func runWorkspaceStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	into := fs.String("into", "", "base dir for worktrees (with positional <repo> <branch>)")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	t, ok := resolveVerbTarget(pos, *into, stderr)
	if !ok {
		return 2
	}
	line, err := workspace.Status(t)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, line)
	return 0
}

// runWorkspaceDiff prints the target branch's PR diff against the default remote
// branch. Read-only — no --dry. Targets the cwd worktree, or an explicit
// <repo> <branch>.
func runWorkspaceDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stat := fs.Bool("stat", false, "print the diffstat summary instead of the full patch")
	into := fs.String("into", "", "base dir for worktrees (with positional <repo> <branch>)")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	t, ok := resolveVerbTarget(pos, *into, stderr)
	if !ok {
		return 2
	}
	if err := workspace.Diff(t, *stat, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

// runWorkspaceUpdate rebases the cwd worktree onto origin/<default> and, on a
// clean rebase, force-pushes to refresh the PR. cwd-only. --dry reports the
// behind-count without mutating; a conflict exits non-zero with the rebase left
// in progress.
func runWorkspaceUpdate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dry := fs.Bool("dry", false, "report the behind-count and stop (default: rebase + push)")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	t, ok := resolveCwdTarget(pos, stderr)
	if !ok {
		return 2
	}
	if err := workspace.Update(t, *dry, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

// runWorkspaceSync fast-forwards a base clone's LOCAL default branch to the
// remote tip after a merge — the non-destructive "catch the clone up to origin"
// primitive. No <repo> resolves the caller's cwd repo (the same rule commit
// uses). --dry previews the fetch + fast-forward without mutating the local
// branch.
func runWorkspaceSync(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dry := fs.Bool("dry", false, "preview the fetch + fast-forward and stop (default: execute)")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	var repo string
	switch len(pos) {
	case 0:
	case 1:
		repo = pos[0]
	default:
		fmt.Fprintln(stderr, "aphrollo: usage: workspace sync [repo]")
		return 2
	}
	if err := workspace.Sync(repo, *dry, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

// runCheckFn is the seam `workspace verify` calls through to run the actual
// checks — pointed at the real `aphrollo check` (internal/cli/check.go,
// landed in #388) now that it exists, so a test can still swap it for a spy
// without touching runCheck itself.
var runCheckFn = runCheck

// runWorkspaceVerify is `workspace verify`'s new body: it is a renamed verb,
// not a distinct command any more, so it says so and calls through to the
// same check the name change points at.
func runWorkspaceVerify(args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "workspace verify is now aphrollo check")
	return runCheckFn(args, stdout, stderr)
}

func runWorkspaceMerge(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		dry    = fs.Bool("dry", false, "print the plan and stop (default: execute)")
		squash = fs.Bool("squash", false, "squash-merge (default)")
		mergeC = fs.Bool("merge", false, "create a merge commit")
		rebase = fs.Bool("rebase", false, "rebase-merge")
		keep   = fs.Bool("keep-branch", false, "keep the PR branch (default: delete it)")
		into   = fs.String("into", "", "base dir for worktrees (with positional <repo> <branch>)")
		wait   = fs.Bool("wait", false, "wait for every check on the PR's current head, then merge; with PR numbers, merge them as a serial queue")
		tmo    = fs.Duration("timeout", workspace.DefaultWaitOpts().Timeout, "with --wait: how long to wait for checks before giving up")
	)
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	method := "squash"
	switch {
	case *mergeC && !*squash && !*rebase:
		method = "merge"
	case *rebase && !*squash && !*mergeC:
		method = "rebase"
	case boolCount(*squash, *mergeC, *rebase) > 1:
		fmt.Fprintln(stderr, "aphrollo: choose one of --squash | --merge | --rebase")
		return 2
	}
	if *wait {
		opts := workspace.DefaultWaitOpts()
		opts.Timeout = *tmo
		return runWorkspaceMergeWait(pos, *into, method, !*keep, *dry, opts, stdout, stderr)
	}
	t, ok := resolveVerbTarget(pos, *into, stderr)
	if !ok {
		return 2
	}
	m, err := workspace.MergePlan(t, method, !*keep)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	apply := !*dry
	fmt.Fprint(stdout, m.Render(apply))
	if !apply {
		return 0
	}
	if err := m.Apply(stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	// Housekeeping, best-effort: the merge already landed by the time this
	// runs, so a sweep failure must not fail the merge (issue #144). Every
	// other lane's worktree is a candidate; this one, still running the
	// merge, is excluded regardless of its own branch's state.
	tdd.PruneMergedLanesAfterMerge(t.MainRepo, t.Worktree, stdout, stderr)
	return 0
}

// boolCount counts how many of the given flags are set — used to reject
// mutually-exclusive flag combinations.
func boolCount(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// resolveCwdTarget resolves the worktree a CWD-ONLY coder verb (commit, push,
// submit) acts on: always the worktree the caller stands in. These verbs took no
// positional targeting by design — they operate on the worktree you cd'd into —
// so any positional arg is a usage error with a pointer at the cwd-only contract.
func resolveCwdTarget(pos []string, stderr io.Writer) (*workspace.Target, bool) {
	if len(pos) != 0 {
		fmt.Fprintln(stderr, "aphrollo: this verb is cwd-only — cd into the worktree and pass no positional args")
		return nil, false
	}
	t, err := workspace.ResolveTarget("", "", "")
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return nil, false
	}
	return t, true
}

// resolveVerbTarget resolves the worktree the git verbs act on from up to two
// trailing positional args: none => the cwd worktree; <repo> <branch> => the
// prepared worktree. More than two positionals is a usage error.
func resolveVerbTarget(pos []string, into string, stderr io.Writer) (*workspace.Target, bool) {
	var repo, branch string
	switch len(pos) {
	case 0:
	case 2:
		repo, branch = pos[0], pos[1]
	default:
		fmt.Fprintln(stderr, "aphrollo: pass no positional args (current worktree) or exactly <repo> <branch>")
		return nil, false
	}
	t, err := workspace.ResolveTarget(repo, branch, into)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return nil, false
	}
	return t, true
}

// parseFlagsAnywhere parses fs but, unlike flag.Parse, tolerates flags appearing
// after positional args (e.g. `create <repo> <branch> --dry`). It returns the
// positional args in order. Flag values are set on fs as usual.
//
// A standalone "--" terminates option parsing: every token after it is returned
// as a positional verbatim, even one starting with a dash. Without this, the
// reparse loop would treat a second dash-prefixed positional after "--" as an
// unknown flag and fail.
func parseFlagsAnywhere(fs *flag.FlagSet, args []string) ([]string, error) {
	var tail []string
	if i := slices.Index(args, "--"); i >= 0 {
		tail = append(tail, args[i+1:]...)
		args = args[:i]
	}
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
	return append(pos, tail...), nil
}
