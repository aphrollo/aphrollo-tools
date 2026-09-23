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
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/guardrail"
	"github.com/aphrollo/aphrollo-tools/internal/refactor"
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
  gate        Autonomous TDD + law gates (Claude + git hooks); tdd is a silent alias
  status      One call for "what is running right now, in this checkout" — deferred
              edit jobs, every build slot's holder, and the mutation run; alias for
              gate status so a caller who knows nothing need not know which
              subsystem to ask (--wait blocks on this checkout's own work)
  install     Wire the whole gate (session hooks, global git gate) and a repo's
              git-hook shims in one run — merges gate init + gate install --apply
  issue       Open one labelled issue against the repo's GitHub remote and print its URL
  feedback    Report a defect in the gate itself to the tool's own tracker; alias for gate feedback
  ratchet     Judge a repo against its declared code laws (.ratchet/laws/*.toml)
  sqlc        Guard sqlc-generated code against drift (check / scoped regen)
  docs        Guard doc-cited repo paths against dangling references (check)
  check       Judge the tree: ratchet laws, docs, sqlc drift, the install doctor,
              and (if declared) the app trio — one line per guard
  version     Print the commit and build time this binary was stamped with
  update      Fetch, build ./cmd/aphrollo from origin/main in a temporary worktree, swap it in, sweep stale copies, re-run init (--repo, --bin, --no-init)
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
	// `tdd` is the pre-rename spelling, kept silent for one release so a hook
	// or shim installed before the rename keeps working until init rewrites it.
	case "gate", "tdd":
		return runGate(args[1:], stdin, stdout, stderr)
	// `status` is `gate status` typed at the top level (issue #435): a caller
	// who knows nothing about which subsystem owns a wait should not have to
	// learn `gate` first to ask "what is running". Same flags, same report —
	// runGateStatus IS the implementation, never a second one that could
	// disagree with it.
	case "status":
		return runGateStatus(args[1:], stdout, stderr)
	case "install":
		return runInstall(args[1:], stdout, stderr)
	case "issue":
		return runGateIssue(args[1:], stdout, stderr)
	case "feedback":
		return runGateFeedback(args[1:], stdout, stderr)
	case "ratchet":
		return runRatchet(args[1:], stdout, stderr)
	case "sqlc":
		return runSqlc(args[1:], stdout, stderr)
	case "docs":
		return runDocs(args[1:], stdout, stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "update":
		return runUpdate(args[1:], stdout, stderr)
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
