package cli

import (
	"fmt"
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
	dir := harnessConfigDir()
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
