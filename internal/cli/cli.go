// Package cli implements the aphrollo command-line surface: argument parsing,
// subcommand dispatch, and rendering tool output to stdout/stderr. Exit codes:
// 0 success, 1 runtime error, 2 usage error.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/guardrail"
	"github.com/aphrollo/aphrollo-tools/internal/refactor"
)

const rootUsage = `usage: aphrollo <command> [args]

Commands:
  refactor    Language-server-backed code transformations
  guardrail   PreToolUse policy hook for coder/devops sessions

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
	case "guardrail":
		return runGuardrail(args[1:], stdin, stdout, stderr)
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
