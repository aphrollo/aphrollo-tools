package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/workspace"
)

func runWorkspaceCommit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("commit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		msg        = fs.String("m", "", "commit message (required)")
		dry        = fs.Bool("dry", false, "print the plan and stop (default: execute)")
		noVerify   = fs.Bool("no-verify", false, "skip the pre-commit gate (the documented false-positive escape)")
		reason     = fs.String("reason", "", "required with --no-verify: why the gate is being skipped")
		stagedOnly = fs.Bool("staged-only", false, "commit the index as-is instead of git add -A")
	)
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	t, ok := resolveCwdTarget(pos, stderr)
	if !ok {
		return 2
	}
	c, err := workspace.CommitPlan(t, *msg, !*stagedOnly, *noVerify, *reason)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	apply := !*dry
	fmt.Fprint(stdout, c.Render(apply))
	if !apply {
		return 0
	}
	if err := c.Apply(stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

func runWorkspacePush(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		dry   = fs.Bool("dry", false, "print the plan and stop (default: execute)")
		force = fs.Bool("force-with-lease", false, "pass --force-with-lease to git push")
	)
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	t, ok := resolveCwdTarget(pos, stderr)
	if !ok {
		return 2
	}
	p, err := workspace.PushPlan(t, *force)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	apply := !*dry
	fmt.Fprint(stdout, p.Render(apply))
	if !apply {
		return 0
	}
	if err := p.Apply(stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

func runWorkspacePR(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		dry   = fs.Bool("dry", false, "print the plan and stop (default: execute)")
		base  = fs.String("base", "", "base branch for the PR (default: the repo's resolved default branch)")
		title = fs.String("title", "", "PR title (default: filled from the commits)")
		body  = fs.String("body", "", "PR body")
		ready = fs.Bool("ready", false, "open the PR ready for review instead of as a draft")
		into  = fs.String("into", "", "base dir for worktrees (with positional <repo> <branch>)")
	)
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	t, ok := resolveVerbTarget(pos, *into, stderr)
	if !ok {
		return 2
	}
	pr, err := workspace.PRPlan(t, *base, *title, *body, !*ready)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	apply := !*dry
	fmt.Fprint(stdout, pr.Render(apply))
	if !apply {
		return 0
	}
	if err := pr.Apply(stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

func runWorkspaceShip(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ship", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		msg        = fs.String("m", "", "commit message (required)")
		dry        = fs.Bool("dry", false, "print the plan and stop (default: execute)")
		noVerify   = fs.Bool("no-verify", false, "skip the pre-commit gate")
		reason     = fs.String("reason", "", "required with --no-verify: why the gate is being skipped")
		stagedOnly = fs.Bool("staged-only", false, "commit the index as-is instead of git add -A")
		base       = fs.String("base", "", "base branch for the PR (default: the repo's resolved default branch)")
		title      = fs.String("title", "", "PR title (default: filled from the commits)")
		body       = fs.String("body", "", "PR body")
		ready      = fs.Bool("ready", false, "open the PR ready for review instead of as a draft")
		into       = fs.String("into", "", "base dir for worktrees (with positional <repo> <branch>)")
	)
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	t, ok := resolveVerbTarget(pos, *into, stderr)
	if !ok {
		return 2
	}
	s, err := workspace.ShipPlan(t, workspace.ShipRequest{
		Message:  *msg,
		StageAll: !*stagedOnly,
		NoVerify: *noVerify,
		Reason:   *reason,
		Base:     *base,
		Title:    *title,
		Body:     *body,
		Draft:    !*ready,
	})
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	apply := !*dry
	fmt.Fprint(stdout, s.Render(apply))
	if !apply {
		return 0
	}
	if err := s.Apply(stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

func runWorkspaceSubmit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("submit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		msg = fs.String("m", "", "PR summary to set as the body on the in-review handoff")
		dry = fs.Bool("dry", false, "print the plan and stop (default: execute)")
	)
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	t, ok := resolveCwdTarget(pos, stderr)
	if !ok {
		return 2
	}
	s, err := workspace.SubmitPlan(t, *msg)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	apply := !*dry
	fmt.Fprint(stdout, s.Render(apply))
	if !apply {
		return 0
	}
	if err := s.Apply(stdout, stderr); err != nil {
		// submit exits non-zero on a CI block/hold; the receipt is already on
		// stdout, so surface only a terse stderr note (not the full error again).
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

func runWorkspaceUnclaim(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("unclaim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		dry  = fs.Bool("dry", false, "print the plan and stop (default: execute)")
		svc  = fs.String("svc", "", "dev service: rlndx|api (default: derived from repo name)")
		into = fs.String("into", "", "base dir for worktrees (with positional <repo> <branch>)")
	)
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	t, ok := resolveVerbTarget(pos, *into, stderr)
	if !ok {
		return 2
	}
	u, err := workspace.UnclaimPlan(t, *svc)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	apply := !*dry
	fmt.Fprint(stdout, u.Render(apply))
	if !apply {
		return 0
	}
	if err := u.Apply(stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

func runWorkspacePrune(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dry := fs.Bool("dry", false, "print the plan and stop (default: execute)")
	force := fs.Bool("force", false, "remove even a dirty worktree")
	stale := fs.String("stale", "", "sweep detached, PR-less, idle worktrees older than this (e.g. 3d, 72h); sweep form only")
	into := fs.String("into", "", "base dir for worktrees (default: <repo-parent>/.worktrees/<repo-name>)")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	if *stale != "" && len(pos) == 2 {
		fmt.Fprintln(stderr, "aphrollo: --stale applies to the sweep form only: workspace prune [repo] --stale <dur>")
		return 2
	}
	switch len(pos) {
	case 0:
		return pruneSweep("", !*dry, *force, *stale, stdout, stderr)
	case 1:
		return pruneSweep(pos[0], !*dry, *force, *stale, stdout, stderr)
	case 2:
		// Per-ticket form: exactly `remove <repo> <branch> --keep-branch` — same
		// target function, so the two verbs can never drift apart.
		removeArgs := []string{pos[0], pos[1], "--keep-branch"}
		if *dry {
			removeArgs = append(removeArgs, "--dry")
		}
		if *force {
			removeArgs = append(removeArgs, "--force")
		}
		if *into != "" {
			removeArgs = append(removeArgs, "--into", *into)
		}
		return runWorkspaceRemove(removeArgs, stdout, stderr)
	default:
		fmt.Fprintln(stderr, "aphrollo: usage: workspace prune [repo] | workspace prune <repo> <branch>")
		return 2
	}
}

// pruneSweep resolves the repo and runs `prune`'s sweep: the merged-PR sweep
// by default, or the --stale idle-detached sweep when staleArg is set.
func pruneSweep(repo string, apply, force bool, staleArg string, stdout, stderr io.Writer) int {
	if staleArg != "" {
		dur, err := workspace.ParseStaleDuration(staleArg)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo: %v\n", err)
			return 2
		}
		s, err := workspace.StaleSweepPlan(repo, dur)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo: %v\n", err)
			return 1
		}
		if err := s.Run(apply, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "aphrollo: %v\n", err)
			return 1
		}
		return 0
	}
	p, err := workspace.PrunePlan(repo)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	p.Force = force
	if err := p.Run(apply, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

func runWorkspaceCreate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		dry       = fs.Bool("dry", false, "print the plan and stop (default: execute)")
		into      = fs.String("into", "", "base dir for worktrees (default: <repo-parent>/.worktrees/<repo-name>)")
		noInstall = fs.Bool("no-install", false, "skip the dependency-install step")
		noSafeDir = fs.Bool("no-safe-dir", false, "skip marking repo/worktree as git-safe")
		reinstall = fs.Bool("reinstall", false, "run the install step even if deps already exist")
	)
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 2 {
		fmt.Fprintln(stderr, "aphrollo: usage: workspace create <repo> <branch>")
		return 2
	}

	plan, err := workspace.BuildPlan(workspace.Request{
		Repo:      pos[0],
		Branch:    pos[1],
		Into:      *into,
		NoInstall: *noInstall,
		NoSafeDir: *noSafeDir,
		Reinstall: *reinstall,
	})
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}

	apply := !*dry
	fmt.Fprint(stdout, workspace.Render(plan, apply))
	if !apply {
		return 0
	}
	if err := workspace.Apply(plan, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

func runWorkspaceClaim(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("claim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		dry       = fs.Bool("dry", false, "print the plan and stop (default: execute)")
		svc       = fs.String("svc", "", "dev service to claim: rlndx|api (default: derived from repo name)")
		into      = fs.String("into", "", "base dir for worktrees (default: <repo-parent>/.worktrees/<repo-name>)")
		noMigrate = fs.Bool("no-migrate", false, "skip the api dev-DB goose-up step")
	)
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 2 {
		fmt.Fprintln(stderr, "aphrollo: usage: workspace claim <repo> <branch>")
		return 2
	}
	claim, err := workspace.ClaimPlan(pos[0], pos[1], *svc, *into, *noMigrate)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	apply := !*dry
	fmt.Fprint(stdout, claim.Render(apply))
	if !apply {
		return 0
	}
	if err := claim.Apply(stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

func runWorkspaceList(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(stderr, "aphrollo: usage: workspace list <repo>")
		return 2
	}
	out, err := workspace.List(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, out)
	return 0
}

func runWorkspaceRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dry := fs.Bool("dry", false, "print the plan and stop (default: execute)")
	keepBranch := fs.Bool("keep-branch", false, "keep the local branch (default: delete it)")
	force := fs.Bool("force", false, "remove even a dirty worktree")
	into := fs.String("into", "", "base dir for worktrees (default: <repo-parent>/.worktrees/<repo-name>)")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 2 {
		fmt.Fprintln(stderr, "aphrollo: usage: workspace remove <repo> <branch>")
		return 2
	}
	cmd, err := workspace.RemovePlan(pos[0], pos[1], *into)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	cmd.KeepBranch = *keepBranch
	cmd.Force = *force
	if *dry {
		fmt.Fprintf(stdout, "would run: %s\nrun again without --dry to execute.\n", cmd.Display())
		return 0
	}
	if err := cmd.Run(stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}
