package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/sqlc"
)

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
