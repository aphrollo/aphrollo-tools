package cli

import (
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

const reportUsage = `usage: aphrollo report [--repo <path>] [--since <dur>] [--compare-at <date|sha>] [--json] [--by-version] [--issue [--dry]]
       aphrollo report web [--out <path>] [--no-open] [--repo <path>] [--since <dur>] [--compare-at <date|sha>]

The weekly continuous-improvement report, folded from the repo's event log (the
current repo by default; --since 7d unless given). Seven sections, each number
with the event seqs behind it (replay one with 'aphrollo why <seq>'):

  1. friction per rule: denies, overrides, refusals, not-tested runs, time lost
     (the gate time the agent waited on, apart from time the gate ran while it
     kept working)
  2. wrong-block candidates: override rate per rule, the shadow's would-be
     wrong blocks, stand-downs
  3. escapes by class and the stage that should have caught them
  4. the A/B and the shadow per arm and language, held-out counts, budget
     drops and the 30-lanes-per-arm status
  5. the token cost of the injected texts and the biggest gate lines
  6. proposals, each naming the rule, the numbers and the change; the report
     only proposes, it never applies one
  7. session usage, read from the agent harness's local transcripts
     ($CLAUDE_CONFIG_DIR or ~/.claude, projects/*/*.jsonl and the subagent
     files): tokens per day, lane, role (coordinator or subagent) and model,
     the top sessions, the notional cost (from a price table in code, at read
     time), and what share of the input aphrollo's hook text is, by hook event
     and by gate-line kind. Aggregate numbers only: no prompt, code, tool text
     or injected text is ever copied; a line it cannot read is counted and said

Plain 'report' is read-only and prints the text; --json prints the model.

  web        write the same report as one self-contained HTML page (inline CSS and
             SVG, no script, nothing fetched) to <git common dir>/aphrollo-report/
             report-<ISO week>.html, print the path and open it in the browser:
             'aphrollo report web [--out <path>] [--no-open]'; no server is started
  --compare-at  split the window at a date (2026-10-01) or a commit (its commit
                date) and compare session usage before and after

  --issue    open ONE issue titled 'Report <ISO week>' in the repo's own remote
             with the report as its body, close the earlier report issues with
             a comment linking it, and, the first time both A/B arms hold 30
             lanes, open one 'A/B ready: <repo>' issue (the resume signal).
             Idempotent: a second run in the week is a [skip]
  --dry      with --issue, print what would be opened and open nothing

The daily gc sweep files the --issue report itself once a week (a stamp in the
git common dir); 'report = false' in aphrollo.toml, trellis.toml or the user's
config turns that off.
`

// reportStampFile is the git-common-dir file whose mtime is the last report issue.
const reportStampFile = "aphrollo-report-last"

// reportEvery is how long after the last report issue the gc sweep files the next.
const reportEvery = 7 * 24 * time.Hour

// reportDefaultWindow is the span a report covers unless --since says otherwise.
const reportDefaultWindow = 7 * 24 * time.Hour

// runReport is `aphrollo report`.
func runReport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, reportUsage) }
	var (
		repo      = fs.String("repo", ".", "repository path")
		since     = fs.String("since", "7d", "the window the report covers (7d, 12h)")
		asJSON    = fs.Bool("json", false, "print the report model as JSON")
		byVersion = fs.Bool("by-version", false, "add a section that reads each binary version of the window on its own")
		issue     = fs.Bool("issue", false, "open the week's report issue")
		compareAt = fs.String("compare-at", "", "compare session usage before and after a date or commit")
		out       = fs.String("out", "", "report web: where to write the page")
		noOpen    = fs.Bool("no-open", false, "report web: write the page and do not open it")
		mutFlag   = addMutFlags(fs)
	)
	pos, err := mutFlag.parse(fs, "report", args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, reportUsage)
			return 0
		}
		return 2
	}
	web := len(pos) > 0 && pos[0] == "web"
	if web {
		pos = pos[1:]
	}
	if refuseArgs("report", pos, stderr) {
		return 2
	}
	if !web && (*out != "" || *noOpen) {
		fmt.Fprintln(stderr, "aphrollo report: --out and --no-open belong to 'report web'")
		return 2
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if *issue && (set["since"] || set["compare-at"]) {
		fmt.Fprintln(stderr, "aphrollo report: --issue files the default 7-day report and takes neither --since nor --compare-at; print the report without --issue to see a window")
		return 2
	}
	if web && *issue {
		fmt.Fprintln(stderr, "aphrollo report web: --issue files the text report; run it without web")
		return 2
	}
	if _, err := os.Stat(*repo); err != nil {
		fmt.Fprintf(stderr, "aphrollo report: --repo: %v\n", err)
		return 2
	}
	window, err := tdd.ParseGCAge(*since)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo report: --since: %v\n", err)
		return 2
	}
	root := repoRootOf(*repo)
	now := time.Now().UTC()
	var cmpAt time.Time
	if *compareAt != "" {
		if cmpAt, err = parseCompareAt(root, *compareAt); err != nil {
			fmt.Fprintf(stderr, "aphrollo report: --compare-at: %v\n", err)
			return 2
		}
		if start := now.Add(-window).Truncate(24 * time.Hour); !cmpAt.After(start) || cmpAt.After(now) {
			fmt.Fprintf(stderr, "aphrollo report: --compare-at %s lies outside the window (%s to now): widen --since\n", *compareAt, start.Format("2006-01-02"))
			return 2
		}
	}
	if *issue {
		return deliverReport(root, !mutFlag.execute(), stdout, stderr)
	}
	rep := buildReport(root, window, now, cmpAt, false, *byVersion)
	if web {
		return writeReportPage(root, rep, *out, !*noOpen, !mutFlag.execute(), stdout, stderr)
	}
	if *asJSON {
		return printJSON(rep, stdout, stderr)
	}
	fmt.Fprint(stdout, rep.Text())
	return 0
}

// buildReport folds the repo's event log and measures its injected texts.
func buildReport(root string, window time.Duration, now, compareAt time.Time, abReady, byVersion bool) report.Report {
	var briefs []measure.Brief
	for _, b := range tdd.Briefs(tdd.RepoRoot(root)) {
		briefs = append(briefs, measure.Brief{Name: b.Name, Subagent: b.Subagent, Bytes: len(b.Text)})
	}
	events := tdd.ReadEvents(root)
	return report.Build(report.Input{
		Events: events, Now: now, Window: window,
		Repo: repoName(root), RepoKey: tddarm.RepoKey(root), Briefs: measure.CheckBriefs(briefs),
		Usage: scanUsage(root, window, now, events), CompareAt: compareAt, ABReadyIssued: abReady, ByVersion: byVersion,
	})
}
