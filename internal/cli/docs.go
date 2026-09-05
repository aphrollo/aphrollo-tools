package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/aphrollo/aphrollo-tools/internal/docs"
)

const docsUsage = `usage: aphrollo docs <subcommand> [args]

Subcommands:
  check [path...]   Verify every repo path a tracked doc cites still resolves.
                    Exit 1 on any unresolved reference. Read-only.

Scans tracked *.md (git ls-files) under the repo root (default: cwd repo);
narrow to specific pathspecs by passing them. The extraction and resolution
rule is the ratchet engine's own doc-path-resolves matcher — this command is
its CLI surface, nothing more: a repo that declares its own
.ratchet/laws/doc_reference_exists.toml is judged by that law, else by the
built-in default (a markdown link target or an inline-code token that looks
like a repo-relative path, resolved relative to the citing file's own
directory, then the repo root). Reports every miss as:

  file:line: unresolved reference: <path>

The bar is zero. There is no baseline file, no allowlist, no suppression comment
— a rule with an escape hatch decays. A doc that cites a path that no longer
exists silently misdrives every agent session that loads it; this catches that.
`

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
