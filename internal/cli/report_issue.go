package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/config"
	"github.com/aphrollo/aphrollo-tools/internal/git"
	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/integrate/host/github"
	"github.com/aphrollo/aphrollo-tools/internal/report"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// The git-common-dir files of the weekly filing: the backoff stamp is the last
// failed filing, so a host that is down is asked once a day and not at every
// sweep; the refused file holds the line of an undercover refusal.
const (
	reportBackoffFile = "aphrollo-report-backoff"
	reportRefusedFile = "aphrollo-report-refused"
	reportBackoff     = 24 * time.Hour
)

// reportTrackerFn is the repo's issue host and the name the A/B ready issue
// carries; ok is false when the repo has no issue host (no GitHub origin).
// bounded puts the adapter's default 60 s on each gh call, as the unattended
// path does; a typed --issue waits as long as the person at the terminal will.
// A test replaces it with a Fake.
var reportTrackerFn = func(root string, bounded bool) (report.Tracker, string, bool) {
	c, err := git.New(root, git.Options{})
	if err != nil {
		return nil, "", false
	}
	_, name, ok := github.OwnerRepo(c.RemoteURL("origin"))
	if !ok {
		return nil, "", false
	}
	timeout := time.Duration(-1)
	if bounded {
		timeout = 0
	}
	return github.New(github.Options{Dir: root, Timeout: timeout}), name, true
}

// repoRootOf is the repository root of path: the checkout's top directory from
// any subdirectory of it, else the absolute path (a directory git does not own).
func repoRootOf(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if root := tdd.RepoRoot(abs); root != "" {
		return root
	}
	return abs
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

// deliverOptions are the options both delivery paths share: the labels, the
// account the host acts as and the repo's undercover check, which runs on every
// title and body before anything is published.
func deliverOptions(root, name string, tr report.Tracker, now time.Time, dry bool) report.DeliverOptions {
	o := report.DeliverOptions{Repo: name, Labels: reportLabels(root), Dry: dry, Now: now,
		Refuse: func(title, body string) string {
			return undercoverTextRefusal(root, [2]string{"report issue title", title}, [2]string{"report issue body", body})
		}}
	if id, ok := tr.(host.Identity); ok {
		o.Author, _ = id.Whoami()
	}
	return o
}

// deliverReport is the typed `report --issue`: it opens the week's report as an
// issue, the default 7-day window, and stamps the week it was done.
func deliverReport(root string, dry bool, stdout, stderr io.Writer) int {
	tr, name, ok := reportTrackerFn(root, false)
	if !ok {
		fmt.Fprintf(stderr, "aphrollo report: no issue host: %s has no GitHub origin, or gh is not on PATH\n", root)
		return 1
	}
	now := time.Now().UTC()
	lines, err := report.Deliver(tr, func(abReady bool) report.Report {
		return buildReport(root, reportDefaultWindow, now, time.Time{}, abReady, false)
	}, deliverOptions(root, name, tr, now, dry))
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

func commonFile(root, name string) string {
	if dir := config.CommonDir(root); dir != "" {
		return filepath.Join(dir, name)
	}
	return ""
}

func touch(path, text string, now time.Time) {
	if path == "" {
		return
	}
	if os.WriteFile(path, []byte(text), 0o600) == nil {
		_ = os.Chtimes(path, now, now)
	}
}

func stampReport(root string, now time.Time) {
	touch(commonFile(root, reportStampFile), now.UTC().Format(time.RFC3339), now)
}

// youngerThan is whether the file exists and was written less than d before now.
func youngerThan(path string, d time.Duration, now time.Time) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && now.Sub(info.ModTime()) < d
}

// reportDue is whether a week has passed since the last report issue and the
// last failure was more than a day ago.
func reportDue(root string, now time.Time) bool {
	path := commonFile(root, reportStampFile)
	if path == "" {
		return false
	}
	if youngerThan(path, reportEvery, now) {
		return false
	}
	return !youngerThan(commonFile(root, reportBackoffFile), reportBackoff, now)
}

// weeklyReport files the week's report issue from the daily gc sweep: once
// 7 days have passed since the last, in a repo with an issue host that has not
// set report = false. It is silent, as the sweep is, and cheap: the issues are
// listed first and the report, with its transcript scan, is built only if
// something is to be opened. A failure writes a one-day backoff, and a refusal
// of the undercover check leaves its line in the git common dir. It reports
// whether it filed.
func weeklyReport(root string, now time.Time) bool {
	if root == "" {
		return false
	}
	root = repoRootOf(root)
	if !config.ForDir(root).Get("report").Value.B || !reportDue(root, now) {
		return false
	}
	tr, name, ok := reportTrackerFn(root, true)
	if !ok {
		return false
	}
	_, err := report.Deliver(tr, func(abReady bool) report.Report {
		return buildReport(root, reportDefaultWindow, now.UTC(), time.Time{}, abReady, false)
	}, deliverOptions(root, name, tr, now, false))
	if err != nil {
		touch(commonFile(root, reportBackoffFile), now.UTC().Format(time.RFC3339), now)
		if errors.Is(err, report.ErrRefused) {
			touch(commonFile(root, reportRefusedFile), err.Error()+"\n", now)
		}
		return false
	}
	stampReport(root, now)
	return true
}
