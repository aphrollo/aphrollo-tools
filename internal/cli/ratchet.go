package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const ratchetUsage = `usage: aphrollo ratchet <subcommand>

Subcommands:
  check    Judge the tree against .ratchet/laws/*.toml (--repo, --only, --proposed
           file=contentfile, --format text|json, --no-tighten, --no-cache)

A law is DATA: .ratchet/laws/<name>.toml names a scope, a matcher and a
severity. check compares what it measures to the law's checked-in baseline —
a ceiling that only ever goes down — and exits 1 when a deny law regressed.
--proposed overlays content that is not on disk yet, which is how the pre-edit
hook denies a write before it lands.
`

func runRatchet(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		w, code := stderr, 2
		if len(args) > 0 {
			w, code = stdout, 0
		}
		fmt.Fprint(w, ratchetUsage)
		return code
	}
	switch args[0] {
	case "check":
		return runRatchetCheck(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aphrollo ratchet: unknown subcommand %q\n\n%s", args[0], ratchetUsage)
		return 2
	}
}

// proposedFlag collects repeated --proposed file=contentfile pairs.
type proposedFlag map[string]string

func (p proposedFlag) String() string { return "" }

func (p proposedFlag) Set(v string) error {
	path, contentFile, ok := strings.Cut(v, "=")
	if !ok || path == "" || contentFile == "" {
		return fmt.Errorf("--proposed takes <repo-relative-path>=<file holding the proposed content>")
	}
	data, err := os.ReadFile(contentFile)
	if err != nil {
		return fmt.Errorf("reading proposed content %s: %w", contentFile, err)
	}
	p[strings.ReplaceAll(path, `\`, "/")] = string(data)
	return nil
}

func runRatchetCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		repo      = fs.String("repo", ".", "repository to check")
		only      = fs.String("only", "", "run exactly one law by name")
		format    = fs.String("format", "text", "text or json")
		noTighten = fs.Bool("no-tighten", false, "never write a baseline down (report only)")
		noCache   = fs.Bool("no-cache", false, "ignore the per-file scan cache")
		proposed  = proposedFlag{}
	)
	fs.Var(proposed, "proposed", "judge <path>=<contentfile> instead of what is on disk (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "aphrollo ratchet: --format is text or json, got %q\n", *format)
		return 2
	}

	root := *repo
	if r := tdd.RepoRoot(root); r != "" {
		root = r
	}
	if !ratchet.HasLaws(root) {
		if *format == "json" {
			fmt.Fprintln(stdout, `{"laws":0,"findings":[]}`)
		} else {
			fmt.Fprintf(stdout, "ratchet: no laws in %s (%s)\n", root, ratchet.LawsDir)
		}
		return 0
	}

	opts := ratchet.Options{
		Root:     root,
		Only:     *only,
		Proposed: proposed,
		Tighten:  !*noTighten,
	}
	if !*noCache {
		opts.CacheDir = tdd.StateDir()
	}
	res, err := ratchet.Check(opts)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo ratchet: %v\n", err)
		return 1
	}

	if *format == "json" {
		data, err := json.Marshal(res)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo ratchet: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		for _, line := range res.Lines() {
			fmt.Fprintln(stdout, line)
		}
		fmt.Fprintln(stdout, ratchetSummary(res))
	}
	if res.Blocked() {
		return 1
	}
	return 0
}

// ratchetSummary is the one line a clean run prints: a gate that says nothing
// is indistinguishable from a gate that never ran.
func ratchetSummary(res ratchet.Result) string {
	summary := fmt.Sprintf("ratchet: %d law(s), %d file(s), %d regression(s)",
		res.Laws, res.FilesScanned, len(res.Findings))
	if len(res.Tightened) > 0 {
		summary += fmt.Sprintf(" — tightened %s", strings.Join(res.Tightened, ", "))
	}
	return summary
}
