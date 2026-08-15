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
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/dev"
	"github.com/aphrollo/aphrollo-tools/internal/guardrail"
	"github.com/aphrollo/aphrollo-tools/internal/refactor"
	"github.com/aphrollo/aphrollo-tools/internal/sqlc"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/workspace"
)

const rootUsage = `usage: aphrollo <command> [args]

Commands:
  refactor    Rename a symbol and all its references across the project (LSP-backed)
  find        List every reference to a symbol across the project (LSP-backed, read-only)
  outline     List a file's symbols (kinds + line ranges) without reading it
  show        Print the source of one named symbol in a file
  workspace   Create/claim/list/remove git worktrees (safe.directory + deps + dev-tier)
  dev         Dev-tier control plane: up/down/restart/status/logs
  guardrail   PreToolUse policy hook for coder/devops sessions
  tdd         Autonomous TDD gates (Claude + git hooks)
  sqlc        Guard sqlc-generated code against drift (check / scoped regen)
`

// commandTimeout bounds a single language-server-backed command end to end —
// the initialize handshake and graceful shutdown included, which sit OUTSIDE
// the per-request loading-retry budget. A hung-but-alive server (rust-analyzer
// cold start is the canonical case) keeps its stdout open, so without a deadline
// the JSON-RPC read loop never unblocks and the CLI wedges forever.
const commandTimeout = 120 * time.Second

// commandContext derives the context for a refactor command: cancelled on the
// first interrupt (Ctrl-C) and bounded by commandTimeout so nothing hangs
// indefinitely. The returned cancel func must be deferred.
func commandContext() (context.Context, context.CancelFunc) {
	ctx, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt)
	ctx, cancelTimeout := context.WithTimeout(ctx, commandTimeout)
	return ctx, func() {
		cancelTimeout()
		stopSignal()
	}
}

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
	case "find":
		return runFindReferences(args[1:], stdout, stderr)
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
	case "sqlc":
		return runSqlc(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aphrollo: unknown command %q\n\n%s", args[0], rootUsage)
		return 2
	}
}

const refactorUsage = `usage: aphrollo refactor --file F --line N (--symbol S | --col C) --new-name X [--apply]

Renames a symbol and all its references across the project via the language
server. Dry-run by default — prints the unified diff; pass --apply to write.
`

// runRefactor IS the rename: flags parse directly on the verb (no subcommand).
// It stays dry-run-by-default + --apply (only workspace verbs execute by default).
func runRefactor(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, refactorUsage)
		return 0
	}
	fs := flag.NewFlagSet("refactor", flag.ContinueOnError)
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

	ctx, cancel := commandContext()
	defer cancel()
	res, err := refactor.Rename(ctx, refactor.RenameRequest{
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
  sessionstart      Inject the build-skill nudge at session start
  pretooluse        Evaluate a Claude Code PreToolUse edit payload from stdin
  posttooluse       Run related tests after an edit and report RED/GREEN
  userpromptsubmit  Handle the /tdd command and re-inject a RED reminder
  sessionend        Drop the session's state file
  precommit         Git pre-commit gate: fail-first + mechanical (run in the repo)
  premergecommit    Git pre-merge-commit gate: mechanical ONLY, no fail-first/anti-cheat
  prepush           No-op (mechanical-only mode); kept for back-compat with a
                    lingering pre-push shim. Never blocks.
  install           Install the git-hook shims into a repo (--repo, --apply)
  init              Set up TDD: session hooks in settings.json + the global git gate (--no-git, --uninstall)

Autonomous TDD gates. pretooluse reads the hook JSON on stdin; on a smell in a
test file (real-time sleep, tautological assertion, focused/disabled test) it
exits 2 with a deny envelope, and warns on a suppression; otherwise it is
silent. posttooluse runs the project's related tests after an edit and surfaces
a failure summary (silent unless RED). userpromptsubmit intercepts
/tdd [status|off|on|reset] and otherwise re-injects the last RED outcome.
sessionend cleans up the per-session state file. precommit verifies fail-first,
blocks a newly-added suppression, and runs the suite, exiting non-zero to block.
premergecommit runs ONLY the mechanical stage over the merge's staged files —
no fail-first (a fresh test's RED/GREEN belongs to the authoring commit,
already proven by precommit there) and no anti-cheat suppression scan (same
reasoning) — so a git merge, which never fires pre-commit, still proves the
COMBINED result compiles and passes before it lands. prepush is a
mechanical-only no-op (adversarial review lives in the separate reviewer
agent now), kept only so a lingering pre-push shim exits cleanly. Source
edits always flow.
`

// postEditTimeout bounds a PostToolUse suite run so a hung test can't wedge the
// session. Bumped 60s -> 100s 2026-08-15 (build-infra-fix task A2): a scoped
// per-edit cargo build on a Bevy-sized crate routinely blew the old 60s
// budget on a cold cache, which — before PostEdit became loud on every
// outcome — silently read as "nothing to report" instead of the TIMEOUT it
// actually was. precommitTimeout is longer: the commit gate runs the staged
// crates' suites (and the fail-first worktree build), and on heavy-dependency
// repos a first-warm build alone can pass five minutes; a timeout fails open,
// so the ceiling only caps how long a commit can stall, never what it
// proves.
//
// Both alias the canonical tdd.Default*Timeout constants (single source of
// truth, tdd package) rather than redeclaring the numbers here — a 2026-08-15
// review found init.go's PostToolUse hook-TEMPLATE timeout had silently
// drifted to 90s after this Go-side value moved to 100s, so the Claude Code
// harness was killing the hook process from OUTSIDE before RunSuite's own
// context deadline ever fired. init.go's template is now DERIVED from
// tdd.DefaultPostEditTimeout too, so the two can't drift apart again.
const (
	postEditTimeout  = tdd.DefaultPostEditTimeout
	precommitTimeout = tdd.DefaultPrecommitTimeout
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

	// precommit/premergecommit/prepush are git hooks: no stdin, exit non-zero
	// to block.
	if args[0] == "precommit" || args[0] == "premergecommit" || args[0] == "prepush" {
		// prepush is a mechanical no-op: the tdd gate is mechanical-only and
		// adversarial review lives in the separate reviewer agent, not this
		// binary. It NEVER blocks. We keep the subcommand so a pre-push shim
		// present on a box exits cleanly.
		if args[0] == "prepush" {
			fmt.Fprintln(stderr, "tdd prepush: mechanical-only, no-op")
			return 0
		}
		root := tdd.RepoRoot(".")
		if root == "" {
			return 0 // not in a git repo — nothing to gate
		}
		// premergecommit runs ONLY the mechanical stage: a git merge never
		// fires pre-commit, so nothing else has proven the COMBINED tree
		// still compiles and passes — fail-first and the anti-cheat scan are
		// both judgments about how a change was AUTHORED, already settled by
		// precommit on the commits being merged.
		var res tdd.GateResult
		if args[0] == "premergecommit" {
			res = tdd.Mechanical(root, tdd.RunSuite(precommitTimeout))
		} else {
			res = tdd.Precommit(root, tdd.RunSuite(precommitTimeout))
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
	case "sessionstart", "pretooluse", "posttooluse", "userpromptsubmit", "sessionend":
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
	case "sessionstart":
		// SessionStart only ever emits advisory context; it never blocks.
		payload, code := tdd.RenderSessionStart(tdd.HandleSessionStart(raw))
		if len(payload) > 0 {
			stdout.Write(payload)
		}
		return code
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
	// When the content gate allows the edit, fall through to the worktree
	// advisory: a once-per-session nudge when the edit lands in a main clone
	// rather than a prepared worktree. A content Block/Warn takes precedence.
	if decision.Action == tdd.Allow {
		decision = tdd.WorktreeAdvisory(raw)
	}
	payload, code := tdd.RenderPreToolUse(decision)
	if len(payload) > 0 {
		stdout.Write(payload)
	}
	return code
}

// runTDDInstall writes the git-hook shims into a single repo. tdd/refactor
// mutations kept the older dry-run-by-default + --apply model, so this defaults
// to a dry-run and requires --apply; only the workspace verbs inverted to
// execute-by-default with --dry.
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
	plan, err := tdd.BuildInstallPlan(root, defaultBinPath())
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
		noGit       = fs.Bool("no-git", false, "skip the git pre-commit gate; wire session hooks only")
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

const findUsage = `usage: aphrollo find --file F --line N (--symbol S | --col C) [--include-declaration]

Lists every reference to a symbol across the project via the language server
(read-only). Each line is path:line:col: text.
`

// runFindReferences is the top-level `find` verb: flags parse directly on it.
// Read-only — the old `refactor find-references`, promoted to top level.
func runFindReferences(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, findUsage)
		return 0
	}
	fs := flag.NewFlagSet("find", flag.ContinueOnError)
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

	ctx, cancel := commandContext()
	defer cancel()
	refs, err := refactor.FindReferences(ctx, refactor.RefRequest{
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

	ctx, cancel := commandContext()
	defer cancel()
	syms, err := refactor.Outline(ctx, args[0])
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

	ctx, cancel := commandContext()
	defer cancel()
	src, err := refactor.Show(ctx, args[0], args[1])
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, src)
	return 0
}

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
  push                      git push -u origin HEAD AND ensure a DRAFT PR exists
                            (open if absent, reuse if present); reports
                            ahead-count, PR, CI (--dry; --force-with-lease).
  submit -m <summary>       Push (idempotent), then — only if CI is green — flip
                            the draft PR to in-review and set the PR body to the
                            summary (the in_progress → review handoff). CI red or
                            pending: not flipped, non-zero, re-callable (--dry).
                            Per-worktree, one repo at a time: acts on the cwd
                            worktree's PR — there is no ticket-level submit.
                            push/ship opened that draft; submit flips it to ready.

Worktree lifecycle:
  claim <repo> <branch>     Put a prepared worktree on the dev tier so it is
                            viewable. Wraps the privileged aphrollo-dev claim
                            fence. (api: runs goose up on the dev DB;
                            --no-migrate to skip.) (--dry).
  unclaim [repo] [branch]   Repoint the dev tier back at the main clone + restart.
                            Inverse of claim (--dry).
  list <repo>               List the repo's git worktrees (read-only).
  remove <repo> <branch>    Remove a prepared worktree AND delete its local
                            branch (--dry). Idempotent: an already-gone worktree
                            or branch is a [skip], so re-running is a no-op.
  prune [repo]              Sweep the repo's worktrees and remove the merged ones:
                            a worktree goes only if its PR is MERGED, the tree is
                            CLEAN, and it is not the cwd. Others are skipped with a
                            reason (open PR / no PR / dirty / current). Folds in the
                            stale admin-record prune (--dry lists; --force removes a
                            dirty merged tree too).
  prune <repo> <branch>    Per-ticket form: remove exactly that one ticket's
                            worktree. Idempotent — re-running on an already-gone
                            worktree is a no-op success ("already gone"), so a
                            post-merge cleanup can re-run safely. Leaves the local
                            branch in place (that is the remove verb's job).
                            (--dry; --force).

Operator / outside-use verbs (pass [repo] [branch] to target a worktree):
  update                    Rebase the cwd worktree onto origin/<default> and, on a
                            clean rebase, force-push (with lease) to refresh the PR.
                            Conflict: left in progress, non-zero, with resolve hints.
                            cwd-only (--dry reports the behind-count).
  sync <repo>               Fetch + fast-forward the base clone's LOCAL default
                            branch to origin/<default> — the non-destructive
                            "catch the clone up after a merge" primitive. Strict
                            FF only: a dirty or diverged clone is left untouched,
                            exit 0 with the reason. Idempotent (--dry previews).
  diff                      Print the branch's PR diff vs origin/<default>
                            (read-only; --stat for the diffstat).
  verify                    Run the affected app's {test, typecheck, lint} trio —
                            the typecheck/lint the commit gate does NOT cover
                            (--dry lists the commands; default runs them, stops
                            at the first failure).
  status                    One terse line: PR state (merged/open/draft),
                            mergeability gate, and a pass/total check tally
                            (read-only).
  merge                     Merge the branch's PR via gh, honoring CI/mergeable
                            (--dry; --squash|--merge|--rebase, --keep-branch).

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
	case "update":
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
// primitive. Takes an explicit <repo>. --dry previews the fetch + fast-forward
// without mutating the local branch.
func runWorkspaceSync(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dry := fs.Bool("dry", false, "preview the fetch + fast-forward and stop (default: execute)")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "aphrollo: usage: workspace sync <repo>")
		return 2
	}
	if err := workspace.Sync(pos[0], *dry, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

func runWorkspaceVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dry := fs.Bool("dry", false, "print the plan and stop (default: execute)")
	into := fs.String("into", "", "base dir for worktrees (with positional <repo> <branch>)")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	t, ok := resolveVerbTarget(pos, *into, stderr)
	if !ok {
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = t.Worktree
	}
	v, err := workspace.BuildVerify(t, cwd)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	apply := !*dry
	fmt.Fprint(stdout, v.Render(apply))
	if !apply {
		return 0
	}
	if err := v.Apply(stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
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

func runWorkspaceCommit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("commit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		msg        = fs.String("m", "", "commit message (required)")
		dry        = fs.Bool("dry", false, "print the plan and stop (default: execute)")
		noVerify   = fs.Bool("no-verify", false, "skip the pre-commit gate (the documented false-positive escape)")
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
	c, err := workspace.CommitPlan(t, *msg, !*stagedOnly, *noVerify)
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
		base  = fs.String("base", "main", "base branch for the PR")
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
		stagedOnly = fs.Bool("staged-only", false, "commit the index as-is instead of git add -A")
		base       = fs.String("base", "main", "base branch for the PR")
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
	into := fs.String("into", "", "base dir for worktrees (default: <repo-parent>/.worktrees/<repo-name>)")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	switch len(pos) {
	case 0:
		return pruneSweep("", !*dry, *force, stdout, stderr)
	case 1:
		return pruneSweep(pos[0], !*dry, *force, stdout, stderr)
	case 2:
		return pruneTicket(pos[0], pos[1], *into, !*dry, *force, stdout, stderr)
	default:
		fmt.Fprintln(stderr, "aphrollo: usage: workspace prune [repo] | workspace prune <repo> <branch>")
		return 2
	}
}

// pruneTicket resolves repo+branch and idempotently removes that one ticket's
// worktree (safe to re-run: an already-gone worktree is a no-op success).
func pruneTicket(repo, branch, into string, apply, force bool, stdout, stderr io.Writer) int {
	p, err := workspace.PruneTicketPlan(repo, branch, into)
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

// pruneSweep resolves the repo and runs the merged-worktree sweep for `prune`.
func pruneSweep(repo string, apply, force bool, stdout, stderr io.Writer) int {
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
	dry := fs.Bool("dry", false, "print the plan and stop (default: execute)")
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
	if *dry {
		fmt.Fprintf(stdout, "would run: %s\nrun again without --dry to execute.\n", cmd.Display)
		return 0
	}
	if err := cmd.Run(stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	return 0
}

const sqlcUsage = `usage: aphrollo sqlc <subcommand> [args]

Subcommands:
  check                 Regenerate every sqlc config into a temp dir and diff the
                        output against the committed tree; exit non-zero on drift
                        in a GATED config. Intended for CI on main.
                        (--repo DIR, default cwd)
  regen [config] --scoped
                        Regenerate, then apply ONLY the hunks that derive from a
                        query the working tree changed (vs the base, default
                        origin/main); pre-existing drift is reported, not applied.
                        Dry-run by default; --apply writes. (--repo DIR, --base REF)

Gating. Some generated trees are intentionally hand-post-edited, so a clean regen
always differs (aphrollo-api's sqlcgen — see its sqlc.yaml header). Mark those
"reported-only" in a committed sidecar .aphrollo-sqlc.yaml at the repo root:

  configs:
    - file: sqlc.yaml      # post-edited → reported-only, never fails check
      clean: false
    - file: sqlc-ai.yaml   # meant to be clean → gated (the default)
      clean: true

A config absent from the sidecar defaults to gated (clean: true), so a new config
can't silently skip the gate.

The whole-schema models.go gotcha. sqlc emits models.go from the ENTIRE migrations
schema, so any unrelated migration (a new column, a new table) changes models.go
even when your query is untouched — that is why a plain "sqlc generate" pollutes
your diff with a backlog of drift (e.g. CrmTicket* structs, AiEventLog.SrcOff).
"check" surfaces it; "regen --scoped" classifies it as PRE-EXISTING DRIFT and
leaves it for a separate PR, applying only your query's hunks.
`

func runSqlc(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, sqlcUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, sqlcUsage)
		return 0
	case "check":
		return runSqlcCheck(args[1:], stdout, stderr)
	case "regen":
		return runSqlcRegen(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aphrollo sqlc: unknown subcommand %q\n\n%s", args[0], sqlcUsage)
		return 2
	}
}

func runSqlcCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", ".", "repository to check (default: cwd)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfgs, err := sqlc.DiscoverConfigs(*repo)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	if len(cfgs) == 0 {
		fmt.Fprintf(stderr, "aphrollo: no sqlc config files found under %s\n", *repo)
		return 1
	}
	results, err := sqlc.Check(cfgs)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	if sqlc.RenderCheck(stdout, results) {
		return 1
	}
	return 0
}

func runSqlcRegen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("regen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		repo   = fs.String("repo", ".", "repository to regen in (default: cwd)")
		base   = fs.String("base", "origin/main", "git ref to compare queries against for in-scope detection")
		scoped = fs.Bool("scoped", true, "apply only in-scope (changed-query) hunks; leave drift")
		apply  = fs.Bool("apply", false, "write the in-scope hunks (default: print the plan and stop)")
	)
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	if !*scoped {
		fmt.Fprintln(stderr, "aphrollo: regen only supports --scoped (run sqlc directly for a full regen)")
		return 2
	}
	var only string
	switch len(pos) {
	case 0:
	case 1:
		only = pos[0]
	default:
		fmt.Fprintln(stderr, "aphrollo: usage: sqlc regen [config] --scoped")
		return 2
	}
	cfgs, err := sqlc.DiscoverConfigs(*repo)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	cfgs = filterConfigs(cfgs, only)
	if len(cfgs) == 0 {
		fmt.Fprintf(stderr, "aphrollo: no matching sqlc config under %s\n", *repo)
		return 1
	}
	for _, cfg := range cfgs {
		res, err := sqlc.RegenScoped(cfg, *base)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, res.Render(*apply))
		if *apply {
			if err := res.Apply(); err != nil {
				fmt.Fprintf(stderr, "aphrollo: %v\n", err)
				return 1
			}
		}
	}
	return 0
}

// filterConfigs returns just the config whose name matches `only` (a base name
// like "sqlc-ai.yaml"), or all configs when only is empty.
func filterConfigs(cfgs []sqlc.Config, only string) []sqlc.Config {
	if only == "" {
		return cfgs
	}
	var out []sqlc.Config
	for _, c := range cfgs {
		if c.Name == only {
			out = append(out, c)
		}
	}
	return out
}
