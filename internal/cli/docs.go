package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/aphrollo/aphrollo-tools/internal/docs"
)

func runDocs(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, docsUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, docsUsage)
		return 0
	case "check":
		return runDocsCheck(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aphrollo docs: unknown subcommand %q\n\n%s", args[0], docsUsage)
		return 2
	}
}

func runDocsCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// A single positional arg naming a directory is the repo root to scan;
	// otherwise the positionals are pathspecs narrowing the cwd repo.
	root := "."
	paths := fs.Args()
	if len(paths) > 0 {
		if info, err := os.Stat(paths[0]); err == nil && info.IsDir() {
			root, paths = paths[0], paths[1:]
		}
	}
	failed, err := docs.Check(root, paths, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: %v\n", err)
		return 1
	}
	if failed {
		return 1
	}
	return 0
}
