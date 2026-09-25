package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const configUsage = `usage: aphrollo config [--repo <dir>]

Prints the opt-in feature table: each key, its value in --repo (default the
working directory's repo), what turning it on costs, and how to turn it on.
Read-only; the first install in a repo prints the same table once.
`

// runConfig prints the feature table with the repo's current values. A
// directory outside any repo is judged as itself, so the defaults still show.
func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, configUsage)
		return 0
	}
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", ".", "repo whose declared values the table shows")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := tdd.RepoRoot(*repo)
	if root == "" {
		root = *repo
	}
	fmt.Fprint(stdout, tdd.RenderFeatures(root))
	return 0
}
