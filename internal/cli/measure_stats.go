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
	"github.com/aphrollo/aphrollo-tools/internal/report"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/tddarm"
)

const measureStatsUsage = `usage: aphrollo stats [--repo <path>] [--lane <name>] [--week | --since <dur>] [--json] [--by-version] [--briefs | --shadow | --ab]

Prints the pipeline measures folded from the repo's event log (the current
repo by default): lane speed (first event to merge, p50/p90), first-run CI
green by cause and OS, gate wall time per lane, not-tested runs by cause,
edit-to-verdict latency, edits per message, denies by rule, overrides and
wrong blocks (an override within 10 minutes of a deny on the same lane), and
escapes by class, and the commit refusals the edit check missed: the (law, file)
pairs a commit refused on a law that no edit-time deny or guide of the same lane
named earlier, over the window (the one to trend to 0). Read-only.

  --lane <name>    only that lane's events
  --week           only the last 7 days
  --since <dur>    only the last <dur> (7d, 12h)
  --json           the report as JSON (the test cost section is in the text, and in report --json)
  --by-version     read each binary version on its own, under a heading per version: a lane
                   goes with the version that opened it (every readout names the versions
                   its window spans)
  --shadow         instead: what the trellis kernel would have decided beside the
                   live hooks, per rule: fires, agreement, would-be blocks and, of
                   those, catches, wrong blocks and passes from what followed on
                   the lane; a rule under 10 fires says so instead of a rate
  --ab             instead: the red-to-green A/B per arm and language: lanes, denies,
                   warnings, overrides, escapes, friction (denies, overrides, time to
                   green), then the pre-registered metrics (escapes per lane,
                   time to green p50, denies per lane): enforce minus warn with a
                   90% bootstrap interval (fixed seed) and the verdict: deciding,
                   decided: enforce better, decided: warn better, no meaningful
                   difference, or max reached (50 lanes an arm). A lane that
                   recorded no arm gets the one its repo and name hash to; a
                   <lane>-merge branch counts as its lane; a pinned lane is in
                   neither arm
  --briefs         instead: the token count of the managed CLAUDE.md block, the
                   tdd skill and each agent brief against the caps (400 for the
                   block and skill, 250 for an agent), over-cap ones marked

'aphrollo gate stats' reports the stage lines of the event log; this reports its measures.
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
	shadowSection := fs.Bool("shadow", false, "print the shadow section: trellis beside aphrollo's live hooks")
	abSection := fs.Bool("ab", false, "print the red-to-green A/B readout per arm")
	byVersion := fs.Bool("by-version", false, "read each binary version of the window on its own")
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
	if *abSection && (*briefs || *shadowSection) {
		fmt.Fprintln(stderr, "aphrollo stats: --ab replaces the report, as --briefs and --shadow do: pass one")
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
	note := horizonNote(*repo)
	events := tdd.ReadEvents(*repo)
	opts := measure.Options{Lane: *lane, Window: window, RepoKey: tddarm.RepoKey(*repo)}
	section := func(evs []tdd.Event) (string, any) {
		now := time.Now().UTC()
		switch {
		case *abSection:
			ab := measure.ComputeAB(evs, now, opts)
			return ab.Text(), ab
		case *shadowSection:
			s := measure.ComputeShadow(evs, now, opts)
			return s.Text(), s
		}
		r := measure.Compute(evs, now, opts)
		return r.Text(), r
	}
	versions := measure.VersionsText(measure.Versions(events, time.Now().UTC(), opts))
	if *byVersion {
		return printByVersion(events, section, versions, note, *asJSON, stdout, stderr)
	}
	text, value := section(events)
	if *asJSON {
		if note != "" && !*abSection {
			fmt.Fprintln(stderr, note)
		}
		fmt.Fprint(stderr, versions)
		return printJSON(value, stdout, stderr)
	}
	fmt.Fprint(stdout, text)
	if !*abSection && !*shadowSection {
		fmt.Fprint(stdout, "\n"+report.BuildTestCost(events, time.Now().UTC(), window).Text())
	}
	fmt.Fprint(stdout, versions)
	if note != "" && !*abSection {
		fmt.Fprintln(stdout, note)
	}
	return 0
}

// printByVersion prints the section once per binary version, oldest first,
// each lane under the version that opened it.
func printByVersion(events []tdd.Event, section func([]tdd.Event) (string, any), versions, note string, asJSON bool, stdout, stderr io.Writer) int {
	type slice struct {
		Version string `json:"version"`
		Report  any    `json:"report"`
	}
	var all []slice
	for _, sp := range measure.SplitByVersion(events) {
		text, value := section(sp.Events)
		if asJSON {
			all = append(all, slice{sp.Version, value})
			continue
		}
		fmt.Fprintf(stdout, "== version %s ==\n%s", sp.Version, text)
	}
	if asJSON {
		fmt.Fprint(stderr, versions)
		return printJSON(all, stdout, stderr)
	}
	fmt.Fprint(stdout, versions)
	if note != "" {
		fmt.Fprintln(stdout, note)
	}
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
