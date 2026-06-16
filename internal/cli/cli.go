// Package cli implements the aphrollo command-line surface: argument parsing,
// subcommand dispatch, and rendering tool output to stdout/stderr. Exit codes:
// 0 success, 1 runtime error, 2 usage error.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/dev"
	"github.com/aphrollo/aphrollo-tools/internal/guardrail"
	"github.com/aphrollo/aphrollo-tools/internal/refactor"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/workspace"
)

const rootUsage = `usage: aphrollo <command> [args]

Commands:
  refactor    Language-server-backed code transformations
  outline     List a file's symbols (kinds + line ranges) without reading it
  show        Print the source of one named symbol in a file
  workspace   Prepare/claim/list/remove git worktrees (safe.directory + deps + dev-tier)
  dev         Dev-tier control plane: up/down/restart/status/logs
  guardrail   PreToolUse policy hook for coder/devops sessions
  tdd         Autonomous TDD gates (Claude + git hooks)

Run "aphrollo refactor" for refactor subcommands.
`

// Run dispatches args (excluding the program name) and returns a process exit
// code. All output is written to the provided writers, and stdin is read from
// the provided reader, never directly from the process streams, so the entry
// point and tests share one path.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, rootUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, rootUsage)
		return 0
	case "refactor":
		return runRefactor(args[1:], stdout, stderr)
	case "outline":
		return runOutline(args[1:], stdout, stderr)
	case "show":
		return runShow(args[1:], stdout, stderr)
	case "workspace":
		return runWorkspace(args[1:], stdout, stderr)
	case "dev":
		return runDev(args[1:], stdout, stderr)
	case "guardrail":
		return runGuardrail(args[1:], stdin, stdout, stderr)
	case "tdd":
		return runTDD(args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aphrollo: unknown command %q\n\n%s", args[0], rootUsage)
		return 2
	}
}

const refactorUsage = `usage: aphrollo refactor <subcommand> [args]

Subcommands:
  rename-symbol     Rename a symbol and all its references across the project
  find-references   List every reference to a symbol across the project
`

func runRefactor(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, refactorUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, refactorUsage)
		return 0
	case "rename-symbol":
		return runRenameSymbol(args[1:], stdout, stderr)
	case "find-references":
		return runFindReferences(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aphrollo refactor: unknown subcommand %q\n\n%s", args[0], refactorUsage)
		return 2
	}
}

func runRenameSymbol(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rename-symbol", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		file    = fs.String("file", "", "path to a file containing the symbol (required)")
		line    = fs.Int("line", 0, "1-based line of the symbol (required)")
		col     = fs.Int("col", 0, "1-based UTF-16 column of the symbol")
		symbol  = fs.String("symbol", "", "symbol name to locate on the line (alternative to --col)")
		newName = fs.String("new-name", "", "new name for the symbol (required)")
		apply   = fs.Bool("apply", false, "write changes to disk (default: print diff only)")
	)
	if err := fs.Parse(args); err != nil {
		return 2 // flag already printed the error + usage
	}

	switch {
	case *file == "":
		fmt.Fprintln(stderr, "aphrollo: --file is required")
		return 2
	case *line < 1:
		fmt.Fprintln(stderr, "aphrollo: --line (1-based) is required")
		return 2
	case *newName == "":
		fmt.Fprintln(stderr, "aphrollo: --new-name is required")
		return 2
	case *col == 0 && *symbol == "":
		fmt.Fprintln(stderr, "aphrollo: provide --col or --symbol to locate the rename target")
		return 2
	}

	res, err := refactor.Rename(context.Background(), refactor.RenameRequest{
		File:    *file,
		Line:    *line,
		Col:     *col,
		Symbol:  *symbol,
		NewName: *newName,
		Apply:   *apply,
	})
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}

	for _, f := range res.Files {
		fmt.Fprint(stdout, f.Diff)
	}
	if res.Applied {
		fmt.Fprintf(stdout, "\napplied to %d file(s)\n", len(res.Files))
	}
	return 0
}

const guardrailUsage = `usage: aphrollo guardrail <subcommand>

Subcommands:
  pretooluse   Evaluate a Claude Code PreToolUse hook payload from stdin

Intended to be wired as a PreToolUse hook for coder/devops sessions. Reads the
hook JSON on stdin; on a blocked command it exits 2 with a deny envelope, on a
noisy command it exits 0 with advisory context, otherwise it is silent.
`

func runGuardrail(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		w := stderr
		code := 2
		if len(args) > 0 {
			w, code = stdout, 0
		}
		fmt.Fprint(w, guardrailUsage)
		return code
	}
	if args[0] != "pretooluse" {
		fmt.Fprintf(stderr, "aphrollo guardrail: unknown subcommand %q\n\n%s", args[0], guardrailUsage)
		return 2
	}

	raw, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: reading hook input: %v\n", err)
		return 1
	}
	decision, err := guardrail.DecideFromHookInput(raw)
	if err != nil {
		// Fail open: a parse error must not wedge the session.
		fmt.Fprintf(stderr, "aphrollo guardrail: %v (allowing)\n", err)
		return 0
	}
	payload, code := guardrail.Render(decision)
	if len(payload) > 0 {
		stdout.Write(payload)
	}
	return code
}

const tddUsage = `usage: aphrollo tdd <subcommand>

Subcommands:
  pretooluse        Evaluate a Claude Code PreToolUse edit payload from stdin
  posttooluse       Run related tests after an edit and report RED/GREEN
  userpromptsubmit  Handle the /tdd command and re-inject a RED reminder
  sessionend        Drop the session's state file
  precommit         Git pre-commit gate: fail-first + mechanical (run in the repo)
  prepush           Git pre-push gate: adversarial review of the push diff
  install           Install the git-hook shims into a repo (--repo, --apply)
  init              Set up TDD: session hooks in settings.json + the global git gate (--no-git, --uninstall)

Autonomous TDD gates. pretooluse reads the hook JSON on stdin; on a smell in a
test file (real-time sleep, tautological assertion, focused/disabled test) it
exits 2 with a deny envelope, and warns on a suppression; otherwise it is
silent. posttooluse runs the project's related tests after an edit and surfaces
a failure summary (silent unless RED). userpromptsubmit intercepts
/tdd [status|off|on|reset] and otherwise re-injects the last RED outcome.
sessionend cleans up the per-session state file. precommit verifies fail-first,
blocks a newly-added suppression, and runs the suite; prepush reviews the
cumulative diff. Both exit non-zero to block. Source edits always flow.
`

// postEditTimeout bounds a PostToolUse suite run so a hung test can't wedge the
// session. precommitTimeout is longer: the full suite runs at commit time.
const (
	postEditTimeout  = 60 * time.Second
	precommitTimeout = 300 * time.Second
	prepushTimeout   = 120 * time.Second
)

// runTDD dispatches the TDD hook subcommands. Like the guardrail hook, every
// path reads from the provided reader and a parse error fails OPEN (exit 0) so
// a malformed payload can never wedge the session.
func runTDD(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		w, code := stderr, 2
		if len(args) > 0 {
			w, code = stdout, 0
		}
		fmt.Fprint(w, tddUsage)
		return code
	}
	if args[0] == "install" {
		return runTDDInstall(args[1:], stdout, stderr)
	}
	if args[0] == "init" {
		return runTDDInit(args[1:], stdout, stderr)
	}

	// precommit/prepush are git hooks: no stdin, exit non-zero to block.
	if args[0] == "precommit" || args[0] == "prepush" {
		root := tdd.RepoRoot(".")
		if root == "" {
			return 0 // not in a git repo — nothing to gate
		}
		var res tdd.GateResult
		if args[0] == "precommit" {
			res = tdd.Precommit(root, tdd.RunSuite(precommitTimeout))
		} else {
			res = tdd.Prepush(root, tdd.ClaudeReviewer(prepushTimeout))
		}
		// Surface the note (e.g. a fail-open skip) even when allowing — the gate
		// is never silent about why it did or didn't run.
		if res.Message != "" {
			fmt.Fprintln(stderr, res.Message)
		}
		if res.Blocked {
			return 1
		}
		return 0
	}

	switch args[0] {
	case "pretooluse", "posttooluse", "userpromptsubmit", "sessionend":
	default:
		fmt.Fprintf(stderr, "aphrollo tdd: unknown subcommand %q\n\n%s", args[0], tddUsage)
		return 2
	}

	raw, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: reading hook input: %v\n", err)
		return 1
	}

	switch args[0] {
	case "posttooluse":
		// PostToolUse never blocks: it only ever emits advisory context.
		payload, code := tdd.RenderPostToolUse(tdd.PostEdit(raw, tdd.RunSuite(postEditTimeout)))
		if len(payload) > 0 {
			stdout.Write(payload)
		}
		return code
	case "userpromptsubmit":
		// Handles the /tdd command and the RED reminder; never errors the turn.
		payload, code := tdd.RenderPrompt(tdd.HandlePrompt(raw))
		if len(payload) > 0 {
			stdout.Write(payload)
		}
		return code
	case "sessionend":
		tdd.EndSession(raw)
		return 0
	}

	decision, err := tdd.DecidePreEdit(raw)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo tdd: %v (allowing)\n", err)
		return 0
	}
	payload, code := tdd.RenderPreToolUse(decision)
	if len(payload) > 0 {
		stdout.Write(payload)
	}
	return code
}

// runTDDInstall writes the git-hook shims into a single repo. Like the
// workspace mutating commands, it defaults to a dry-run and requires --apply.
func runTDDInstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		repo  = fs.String("repo", ".", "repository to install the hooks into")
		apply = fs.Bool("apply", false, "write the hooks (default: print the plan and stop)")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root := tdd.RepoRoot(*repo)
	if root == "" {
		fmt.Fprintf(stderr, "aphrollo: %s is not inside a git repository\n", *repo)
		return 1
	}
	plan, err := tdd.BuildInstallPlan(root)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, plan.Render(*apply))
	if !*apply {
		return 0
	}
	if err := plan.Apply(); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

// runTDDInit wires (or, with --uninstall, removes) the aphrollo tdd session
// hooks in a Claude config dir's settings.json. It is the native replacement
// for the retired claude-code-tdd install.sh: idempotent, backs up any existing
// file, and resolves the config dir + invoked binary from sensible defaults.
func runTDDInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		configDir   = fs.String("config-dir", "", "Claude config dir (default: $CLAUDE_CONFIG_DIR or ~/.claude)")
		binPath     = fs.String("bin", "", "aphrollo binary the hooks invoke (default: this executable)")
		gitHooksDir = fs.String("git-hooks-dir", "", "git hooks dir for the global gate (default: $XDG_CONFIG_HOME/git/hooks or ~/.config/git/hooks)")
		noGit       = fs.Bool("no-git", false, "skip the git pre-commit/pre-push gate; wire session hooks only")
		uninstall   = fs.Bool("uninstall", false, "remove the hooks instead of installing them")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dir := *configDir
	if dir == "" {
		dir = defaultClaudeDir()
	}
	binName := *binPath
	if binName == "" {
		binName = defaultBinPath()
	}

	changed, err := tdd.InitSettings(dir, binName, *uninstall)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	path := filepath.Join(dir, "settings.json")
	switch {
	case !changed:
		fmt.Fprintf(stdout, "aphrollo tdd: session hooks already up to date in %s\n", path)
	case *uninstall:
		fmt.Fprintf(stdout, "aphrollo tdd: removed session hooks from %s\n", path)
	default:
		fmt.Fprintf(stdout, "aphrollo tdd: wired session hooks in %s\n", path)
	}

	if *noGit {
		return 0
	}
	gdir := *gitHooksDir
	if gdir == "" {
		gdir = defaultGitHooksDir()
	}
	gchanged, err := tdd.InitGitGate(gdir, binName, *uninstall)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	switch {
	case !gchanged:
		fmt.Fprintf(stdout, "aphrollo tdd: git gate already up to date (%s)\n", gdir)
	case *uninstall:
		fmt.Fprintf(stdout, "aphrollo tdd: removed git gate from %s\n", gdir)
	default:
		fmt.Fprintf(stdout, "aphrollo tdd: installed git gate in %s (core.hooksPath)\n", gdir)
	}
	return 0
}

// defaultGitHooksDir is where the global git gate's shims live:
// $XDG_CONFIG_HOME/git/hooks, else ~/.config/git/hooks.
func defaultGitHooksDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "git", "hooks")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "git", "hooks")
	}
	return filepath.Join(home, ".config", "git", "hooks")
}

// defaultClaudeDir resolves the Claude config dir the way the CLI hooks do:
// $CLAUDE_CONFIG_DIR if set, else ~/.claude.
func defaultClaudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// defaultBinPath is the absolute path of the running aphrollo binary, so the
// installed hooks invoke the same binary that wrote them.
func defaultBinPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "/usr/local/bin/aphrollo"
	}
	if abs, err := filepath.Abs(exe); err == nil {
		return abs
	}
	return exe
}

func runFindReferences(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("find-references", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		file       = fs.String("file", "", "path to a file containing the symbol (required)")
		line       = fs.Int("line", 0, "1-based line of the symbol (required)")
		col        = fs.Int("col", 0, "1-based UTF-16 column of the symbol")
		symbol     = fs.String("symbol", "", "symbol name to locate on the line (alternative to --col)")
		includeDfn = fs.Bool("include-declaration", true, "include the declaration in results")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	switch {
	case *file == "":
		fmt.Fprintln(stderr, "aphrollo: --file is required")
		return 2
	case *line < 1:
		fmt.Fprintln(stderr, "aphrollo: --line (1-based) is required")
		return 2
	case *col == 0 && *symbol == "":
		fmt.Fprintln(stderr, "aphrollo: provide --col or --symbol to locate the target")
		return 2
	}

	refs, err := refactor.FindReferences(context.Background(), refactor.RefRequest{
		File:               *file,
		Line:               *line,
		Col:                *col,
		Symbol:             *symbol,
		IncludeDeclaration: *includeDfn,
	})
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}

	for _, r := range refs {
		fmt.Fprintf(stdout, "%s:%d:%d: %s\n", r.Path, r.Line, r.Col, strings.TrimSpace(r.Text))
	}
	return 0
}

const outlineUsage = `usage: aphrollo outline <file>

Prints the file's symbol outline — kind, name, and 1-based line range, nested by
containment — so an agent can map a file's shape without reading its contents.
`

func runOutline(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, outlineUsage)
		return 0
	}
	if len(args) != 1 {
		fmt.Fprint(stderr, outlineUsage)
		return 2
	}

	syms, err := refactor.Outline(context.Background(), args[0])
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, refactor.RenderOutline(syms))
	return 0
}

const showUsage = `usage: aphrollo show <file> <symbol>

Prints the source of the named symbol in the file, located via the language
server, so an agent can read one definition without reading the whole file.
`

func runShow(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, showUsage)
		return 0
	}
	if len(args) != 2 {
		fmt.Fprint(stderr, showUsage)
		return 2
	}

	src, err := refactor.Show(context.Background(), args[0], args[1])
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, src)
	return 0
}

const workspaceUsage = `usage: aphrollo workspace <subcommand> [args]

Lifecycle (create → claim → work → ship → clean up):
  prepare <repo> <branch>   Mark git-safe, create the worktree, install deps —
                            dry-run by default; pass --apply to execute
  claim <repo> <branch>     Put a prepared worktree on the dev tier so it is
                            viewable (dry-run; --apply to run). Wraps the
                            privileged aphrollo-dev claim fence.
  unclaim [repo] [branch]   Repoint the dev tier back at the main clone + restart
                            (dry-run; --apply). Inverse of claim.
  list <repo>               List the repo's git worktrees
  remove <repo> <branch>    Remove a prepared worktree (dry-run; --apply to run)
  prune [repo]              Drop admin records of deleted worktrees (dry-run; --apply)

Git verbs (act on the CURRENT worktree, or pass [repo] [branch] to target one):
  commit -m <msg>           Stage (-A) + commit, honoring the TDD gate; reports
                            sha + delta (dry-run; --apply; --no-verify; --staged-only)
  push                      git push -u origin HEAD; reports ahead-count + URL
                            (dry-run; --apply; --force-with-lease)
  pr                        Open (or reuse) a GitHub PR for the branch
                            (dry-run; --apply; --base, --title, --body, --draft)
  ship -m <msg>             commit → push → pr in one shot (dry-run; --apply)

The worktree lands at <repo-parent>/.worktrees/<repo-name>/<branch-slug> — the
same layout aphrollo-dev uses, so a prepared worktree can later be claimed. The
git verbs default to the worktree you are standing in; an explicit <repo>
<branch> targets the prepared worktree from outside it.
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
	case "prepare":
		return runWorkspacePrepare(args[1:], stdout, stderr)
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
	default:
		fmt.Fprintf(stderr, "aphrollo workspace: unknown subcommand %q\n\n%s", args[0], workspaceUsage)
		return 2
	}
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

func runWorkspaceCommit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("commit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		msg        = fs.String("m", "", "commit message (required)")
		apply      = fs.Bool("apply", false, "execute the commit (default: print the plan and stop)")
		noVerify   = fs.Bool("no-verify", false, "skip the pre-commit gate (the documented false-positive escape)")
		stagedOnly = fs.Bool("staged-only", false, "commit the index as-is instead of git add -A")
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
	c, err := workspace.CommitPlan(t, *msg, !*stagedOnly, *noVerify)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, c.Render(*apply))
	if !*apply {
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
		apply = fs.Bool("apply", false, "execute the push (default: print the plan and stop)")
		force = fs.Bool("force-with-lease", false, "pass --force-with-lease to git push")
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
	p, err := workspace.PushPlan(t, *force)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, p.Render(*apply))
	if !*apply {
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
		apply = fs.Bool("apply", false, "open the PR (default: print the plan and stop)")
		base  = fs.String("base", "main", "base branch for the PR")
		title = fs.String("title", "", "PR title (default: filled from the commits)")
		body  = fs.String("body", "", "PR body")
		draft = fs.Bool("draft", false, "open the PR as a draft")
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
	pr, err := workspace.PRPlan(t, *base, *title, *body, *draft)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, pr.Render(*apply))
	if !*apply {
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
		apply      = fs.Bool("apply", false, "commit, push, and open the PR (default: print the plan and stop)")
		noVerify   = fs.Bool("no-verify", false, "skip the pre-commit gate")
		stagedOnly = fs.Bool("staged-only", false, "commit the index as-is instead of git add -A")
		base       = fs.String("base", "main", "base branch for the PR")
		title      = fs.String("title", "", "PR title (default: filled from the commits)")
		body       = fs.String("body", "", "PR body")
		draft      = fs.Bool("draft", false, "open the PR as a draft")
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
		Base:     *base,
		Title:    *title,
		Body:     *body,
		Draft:    *draft,
	})
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, s.Render(*apply))
	if !*apply {
		return 0
	}
	if err := s.Apply(stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

func runWorkspaceUnclaim(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("unclaim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		apply = fs.Bool("apply", false, "execute the unclaim (default: print it and stop)")
		svc   = fs.String("svc", "", "dev service: rlndx|api (default: derived from repo name)")
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
	u, err := workspace.UnclaimPlan(t, *svc)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, u.Render(*apply))
	if !*apply {
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
	apply := fs.Bool("apply", false, "execute the prune (default: show what would be pruned)")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	repo := ""
	switch len(pos) {
	case 0:
	case 1:
		repo = pos[0]
	default:
		fmt.Fprintln(stderr, "aphrollo: usage: workspace prune [repo]")
		return 2
	}
	p, err := workspace.PrunePlan(repo)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	if err := p.Run(*apply, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

// parseFlagsAnywhere parses fs but, unlike flag.Parse, tolerates flags appearing
// after positional args (e.g. `prepare <repo> <branch> --apply`). It returns the
// positional args in order. Flag values are set on fs as usual.
func parseFlagsAnywhere(fs *flag.FlagSet, args []string) ([]string, error) {
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
	return pos, nil
}

func runWorkspacePrepare(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prepare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		apply     = fs.Bool("apply", false, "execute the plan (default: print it and stop)")
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
		fmt.Fprintln(stderr, "aphrollo: usage: workspace prepare <repo> <branch>")
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

	fmt.Fprint(stdout, workspace.Render(plan, *apply))
	if !*apply {
		return 0
	}
	if err := workspace.Apply(plan, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

const devUsage = `usage: aphrollo dev <subcommand> [args]

Subcommands:
  up                start the whole dev tier
  down [--all]      stop api+rlndx (--all also stops infra)
  restart <svc>     restart one of: api | rlndx | infra
  status            show dev-tier unit status
  logs [<svc>]      journal for one dev unit, or all (default 200 lines, -n N)

This is a service control plane: up/down/restart execute immediately (like
systemctl). status/logs are read-only. Only the write verbs need privilege —
status works unprivileged, logs via the systemd-journal group, and up/down/
restart via exact-match systemctl sudoers grants (no wildcards).
`

func runDev(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, devUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, devUsage)
		return 0
	case "up":
		if len(rest) != 0 {
			fmt.Fprintf(stderr, "aphrollo: dev up takes no arguments\n")
			return 2
		}
		return devResult(dev.Up(stdout, stderr), stderr)
	case "down":
		all := false
		switch {
		case len(rest) == 0:
		case len(rest) == 1 && rest[0] == "--all":
			all = true
		default:
			fmt.Fprintf(stderr, "aphrollo: usage: dev down [--all]\n")
			return 2
		}
		return devResult(dev.Down(all, stdout, stderr), stderr)
	case "restart":
		if len(rest) != 1 {
			fmt.Fprintf(stderr, "aphrollo: usage: dev restart <api|rlndx|infra>\n")
			return 2
		}
		return devResult(dev.Restart(rest[0], stdout, stderr), stderr)
	case "status":
		if len(rest) != 0 {
			fmt.Fprintf(stderr, "aphrollo: dev status takes no arguments\n")
			return 2
		}
		return devResult(dev.Status(stdout, stderr), stderr)
	case "logs":
		return runDevLogs(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aphrollo dev: unknown subcommand %q\n\n%s", sub, devUsage)
		return 2
	}
}

func runDevLogs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	n := fs.Int("n", 200, "number of journal lines to show")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	svc := ""
	switch len(pos) {
	case 0:
	case 1:
		svc = pos[0]
	default:
		fmt.Fprintf(stderr, "aphrollo: usage: dev logs [<svc>] [-n N]\n")
		return 2
	}
	return devResult(dev.Logs(svc, *n, stdout, stderr), stderr)
}

// devResult maps a dev action error to an exit code. A service/usage error
// (bad svc token) is a usage error (2); a runtime failure is 1.
func devResult(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}
	fmt.Fprintf(stderr, "aphrollo: %v\n", err)
	if strings.Contains(err.Error(), "service not allowed") || strings.Contains(err.Error(), "service required") {
		return 2
	}
	return 1
}

func runWorkspaceClaim(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("claim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		apply = fs.Bool("apply", false, "execute the claim (default: print it and stop)")
		svc   = fs.String("svc", "", "dev service to claim: rlndx|api (default: derived from repo name)")
		into  = fs.String("into", "", "base dir for worktrees (default: <repo-parent>/.worktrees/<repo-name>)")
	)
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 2 {
		fmt.Fprintln(stderr, "aphrollo: usage: workspace claim <repo> <branch>")
		return 2
	}
	claim, err := workspace.ClaimPlan(pos[0], pos[1], *svc, *into)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, claim.Render(*apply))
	if !*apply {
		return 0
	}
	if err := claim.Apply(stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

func runWorkspaceList(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
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
	apply := fs.Bool("apply", false, "execute the removal (default: print it and stop)")
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
	if !*apply {
		fmt.Fprintf(stdout, "would run: %s\nrun again with --apply to execute.\n", cmd.Display)
		return 0
	}
	if err := cmd.Run(stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}
