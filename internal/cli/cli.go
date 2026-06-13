// Package cli implements the aphrollo command-line surface: argument parsing,
// subcommand dispatch, and rendering tool output to stdout/stderr. Exit codes:
// 0 success, 1 runtime error, 2 usage error.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/refactor"
)

const rootUsage = `usage: aphrollo <command> [args]

Commands:
  refactor    Language-server-backed code transformations

Run "aphrollo refactor" for refactor subcommands.
`

// Run dispatches args (excluding the program name) and returns a process exit
// code. All output is written to the provided writers, never directly to the
// process streams, so the entry point and tests share one path.
func Run(args []string, stdout, stderr io.Writer) int {
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
	default:
		fmt.Fprintf(stderr, "aphrollo: unknown command %q\n\n%s", args[0], rootUsage)
		return 2
	}
}

const refactorUsage = `usage: aphrollo refactor <subcommand> [args]

Subcommands:
  rename-symbol   Rename a symbol and all its references across the project
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
