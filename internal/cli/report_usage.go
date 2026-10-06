package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/config"
	"github.com/aphrollo/aphrollo-tools/internal/git"
	"github.com/aphrollo/aphrollo-tools/internal/report"
)

// harnessConfigDir is where the agent harness keeps its transcripts: its
// config dir from the environment, else ~/.claude. "" when neither is known.
// harnessConfigDirFn is the lookup a test replaces, so no test reads the
// operator's real transcripts.
var harnessConfigDirFn = harnessConfigDir

func harnessConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".claude")
	}
	return ""
}

// repoName is the repository's name as the transcripts' working directories
// resolve it: the directory holding the common git dir, so every lane of a
// repo is the one repo. A checkout with no git dir is named by its directory.
func repoName(root string) string {
	if common := config.CommonDir(root); common != "" {
		return filepath.Base(filepath.Dir(common))
	}
	return filepath.Base(root)
}

// scanUsage reads the harness's local transcripts for this repo over the
// window. Read-only, run when the report runs and never before.
func scanUsage(root string, window time.Duration, now time.Time) *report.UsageFacts {
	dir := harnessConfigDirFn()
	if dir == "" {
		return nil
	}
	o := report.ScanOptions{ConfigDir: dir, Repo: repoName(root), Now: now}
	if window > 0 {
		o.Since = now.Add(-window)
	}
	facts := report.ScanUsage(o)
	return &facts
}

// parseCompareAt reads --compare-at: a date (2026-10-01), a time (RFC 3339) or a
// commit, whose commit date is the day.
func parseCompareAt(root, v string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	c, err := git.New(root, git.Options{})
	if err == nil {
		var out string
		if out, err = c.Output("show", "-s", "--format=%cI", v+"^{commit}"); err == nil {
			if t, perr := time.Parse(time.RFC3339, strings.TrimSpace(out)); perr == nil {
				return t, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a date (2026-10-01), a time (RFC 3339) or a commit of this repo", v)
}

// openBrowserFn opens a file in the default browser; a test replaces it. It is
// the platform seam of openbrowser_windows.go and openbrowser_other.go.
var openBrowserFn = openInBrowser

// reportPagePath is where `report web` writes unless --out says: the git
// common dir's aphrollo-report directory, one page per ISO week.
func reportPagePath(root, title string) string {
	dir := config.CommonDir(root)
	if dir == "" {
		dir = root
	}
	return filepath.Join(dir, "aphrollo-report", "report-"+strings.TrimPrefix(title, "Report ")+".html")
}

// writeReportPage renders the report as HTML, overwrites the week's page, prints
// its path and opens it. A browser that will not open is not a failure: the
// path is already printed. No server, no listener, nothing left running.
func writeReportPage(root string, rep report.Report, out string, open bool, stdout, stderr io.Writer) int {
	page, err := report.RenderHTML(rep)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo report web: %v\n", err)
		return 1
	}
	path := out
	if path == "" {
		path = reportPagePath(root, rep.Title)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(stderr, "aphrollo report web: %v\n", err)
		return 1
	}
	if err := os.WriteFile(path, page, 0o644); err != nil {
		fmt.Fprintf(stderr, "aphrollo report web: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	if open {
		if err := openBrowserFn(path); err != nil {
			fmt.Fprintf(stderr, "aphrollo report web: could not open the page (%v): open the path above\n", err)
		}
	}
	return 0
}
