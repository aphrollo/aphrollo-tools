package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/measure"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const measureStatsUsage = `usage: aphrollo stats [--repo <path>] [--lane <name>] [--week | --since <dur>] [--json] [--briefs]

Prints the pipeline measures folded from the repo's event log (the current
repo by default): lane speed (first event to merge, p50/p90), first-run CI
green by cause and OS, gate wall time per lane, not-tested runs by cause,
edit-to-verdict latency, edits per message, denies by rule, overrides and
wrong blocks (an override within 10 minutes of a deny on the same lane), and
escapes by class. Read-only.

  --lane <name>    only that lane's events
  --week           only the last 7 days
  --since <dur>    only the last <dur> (7d, 12h)
  --json           the report as JSON
  --briefs         instead: the token count of the managed CLAUDE.md block, the
                   tdd skill and each agent brief against the caps (400 for the
                   block and skill, 250 for an agent), over-cap ones marked

'aphrollo gate stats' reports gate.log; this reports the event log.
`

// runStats is `aphrollo stats`: the measures of the repo's event log, or with
// --briefs the length of the texts a session reads.
func runStats(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, measureStatsUsage) }
	repo := fs.String("repo", ".", "repository path")
	lane := fs.String("lane", "", "only this lane's events")
	week := fs.Bool("week", false, "only the last 7 days")
	since := fs.String("since", "", "only the last <dur> (7d, 12h)")
	asJSON := fs.Bool("json", false, "print JSON")
	briefs := fs.Bool("briefs", false, "measure the managed block, skill and agent briefs against the token caps")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if refuseArgs("stats", pos, stderr) {
		return 2
	}
	if _, err := os.Stat(*repo); err != nil {
		fmt.Fprintf(stderr, "aphrollo stats: --repo: %v\n", err)
		return 2
	}
	if *briefs {
		return printBriefs(*repo, *asJSON, stdout, stderr)
	}
	window, err := statsWindow(*week, *since)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo stats: %v\n", err)
		return 2
	}
	r := measure.Compute(tdd.ReadEvents(*repo), time.Now().UTC(), measure.Options{Lane: *lane, Window: window})
	if *asJSON {
		return printJSON(r, stdout, stderr)
	}
	fmt.Fprint(stdout, r.Text())
	return 0
}

// statsWindow is the span --week or --since names, 0 for the whole log.
func statsWindow(week bool, since string) (time.Duration, error) {
	switch {
	case week && since != "":
		return 0, errors.New("--week and --since both set a window: pass one")
	case week:
		return 7 * 24 * time.Hour, nil
	case since != "":
		return tdd.ParseGCAge(since)
	}
	return 0, nil
}

func printBriefs(repo string, asJSON bool, stdout, stderr io.Writer) int {
	var in []measure.Brief
	for _, b := range tdd.Briefs(tdd.RepoRoot(repo)) {
		in = append(in, measure.Brief{Name: b.Name, Subagent: b.Subagent, Bytes: len(b.Text)})
	}
	lines := measure.CheckBriefs(in)
	if asJSON {
		return printJSON(lines, stdout, stderr)
	}
	fmt.Fprint(stdout, measure.BriefsText(lines))
	return 0
}

func printJSON(v any, stdout, stderr io.Writer) int {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo stats: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s\n", data)
	return 0
}
