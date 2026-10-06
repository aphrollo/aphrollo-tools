package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/config"
	"github.com/aphrollo/aphrollo-tools/internal/git"
	"github.com/aphrollo/aphrollo-tools/internal/integrate/host/github"
	"github.com/aphrollo/aphrollo-tools/internal/measure"
	"github.com/aphrollo/aphrollo-tools/internal/report"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

const reportUsage = `usage: aphrollo report [--repo <path>] [--since <dur>] [--json] [--issue [--dry]]

The weekly continuous-improvement report, folded from the repo's event log (the
current repo by default; --since 7d unless given). Six sections, each number
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

Plain 'report' is read-only and prints the text; --json prints the model.

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

// reportTrackerFn is the repo's issue host and the name the A/B ready issue
// carries; ok is false when the repo has no issue host (no GitHub origin). A
// test replaces it with a Fake.
var reportTrackerFn = func(root string) (report.Tracker, string, bool) {
	c, err := git.New(root, git.Options{})
	if err != nil {
		return nil, "", false
	}
	_, name, ok := github.OwnerRepo(c.RemoteURL("origin"))
	if !ok {
		return nil, "", false
	}
	return github.New(github.Options{Dir: root, Timeout: -1}), name, true
}

// runReport is `aphrollo report`.
func runReport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, reportUsage) }
	var (
		repo    = fs.String("repo", ".", "repository path")
		since   = fs.String("since", "7d", "the window the report covers (7d, 12h)")
		asJSON  = fs.Bool("json", false, "print the report model as JSON")
		issue   = fs.Bool("issue", false, "open the week's report issue")
		mutFlag = addMutFlags(fs)
	)
	pos, err := mutFlag.parse(fs, "report", args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, reportUsage)
			return 0
		}
		return 2
	}
	if refuseArgs("report", pos, stderr) {
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
	root, err := filepath.Abs(*repo)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo report: --repo: %v\n", err)
		return 2
	}
	rep := buildReport(root, window, time.Now().UTC())
	if *issue {
		return deliverReport(root, rep, !mutFlag.execute(), stdout, stderr)
	}
	if *asJSON {
		return printJSON(rep, stdout, stderr)
	}
	fmt.Fprint(stdout, rep.Text())
	return 0
}

// buildReport folds the repo's event log and measures its injected texts.
func buildReport(root string, window time.Duration, now time.Time) report.Report {
	var briefs []measure.Brief
	for _, b := range tdd.Briefs(tdd.RepoRoot(root)) {
		briefs = append(briefs, measure.Brief{Name: b.Name, Subagent: b.Subagent, Bytes: len(b.Text)})
	}
	return report.Build(report.Input{
		Events: tdd.ReadEvents(root), Now: now, Window: window,
		Repo: filepath.Base(root), Briefs: measure.CheckBriefs(briefs),
	})
}

// reportLabels is the labels the report issues carry: the repo's declared
// theme "report" when it declares labels, "report" when it declares none, and
// none when it declares others (an undeclared label is a typo's theme).
func reportLabels(root string) []string {
	declared := tdd.IssueLabels(root)
	if len(declared) == 0 || slices.Contains(declared, "report") {
		return []string{"report"}
	}
	return nil
}

// deliverReport opens the report as an issue and stamps the week it was done.
func deliverReport(root string, rep report.Report, dry bool, stdout, stderr io.Writer) int {
	tr, name, ok := reportTrackerFn(root)
	if !ok {
		fmt.Fprintf(stderr, "aphrollo report: no issue host: %s has no GitHub origin, or gh is not on PATH\n", root)
		return 1
	}
	lines, err := report.Deliver(tr, rep, report.DeliverOptions{Repo: name, Labels: reportLabels(root), Dry: dry})
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo report: %v\n", err)
		return 1
	}
	if !dry {
		stampReport(root, time.Now())
	}
	return 0
}

func reportStampPath(root string) string {
	if dir := config.CommonDir(root); dir != "" {
		return filepath.Join(dir, reportStampFile)
	}
	return ""
}

func stampReport(root string, now time.Time) {
	path := reportStampPath(root)
	if path == "" {
		return
	}
	if os.WriteFile(path, []byte(now.UTC().Format(time.RFC3339)), 0o600) == nil {
		_ = os.Chtimes(path, now, now)
	}
}

// reportDue is whether a week has passed since the last report issue.
func reportDue(root string, now time.Time) bool {
	path := reportStampPath(root)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err != nil || now.Sub(info.ModTime()) >= reportEvery
}

// weeklyReport files the week's report issue from the daily gc sweep: once
// 7 days have passed since the last, in a repo with an issue host that has not
// set report = false. It is silent, as the sweep is: a failure files nothing
// and the next sweep tries again. It reports whether it filed.
func weeklyReport(root string, now time.Time) bool {
	if root == "" || !config.ForDir(root).Get("report").Value.B || !reportDue(root, now) {
		return false
	}
	tr, name, ok := reportTrackerFn(root)
	if !ok {
		return false
	}
	rep := buildReport(root, reportDefaultWindow, now.UTC())
	if _, err := report.Deliver(tr, rep, report.DeliverOptions{Repo: name, Labels: reportLabels(root)}); err != nil {
		return false
	}
	stampReport(root, now)
	return true
}
